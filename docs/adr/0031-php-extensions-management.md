# ADR-0031: PHP extensions management — server-wide, live from dpkg, fixed allowlist

**Date**: 2026-04-19
**Status**: accepted
**Deciders**: shuki + Claude
**Related**: ADR-0023 (M9 PHP-FPM pool manager), ADR-0025 (per-user systemd slices)

## Context

M9 shipped per-user PHP-FPM pools with per-version binding and an ini-override allowlist. What it did NOT ship is a UI to enable/disable PHP extensions (apcu, redis, imagick, intl, xdebug, …). Until now, the only way to add an extension to a running PHP was `ssh + apt install php<v>-<ext>`, which is fine for the operator but nothing a customer can self-serve.

Several questions shaped this decision:

1. **Scope** — per-domain? per-pool? per-user? per-PHP-version?
2. **State of truth** — does the panel DB track the enabled set, or does the host (dpkg + `/etc/php/<v>/fpm/conf.d/*.ini`) remain authoritative?
3. **Allowlist vs free-form** — which extensions can an admin install?
4. **Trust boundary** — who re-validates what, and where?

## Decision

**Extensions are managed server-wide per PHP version**, not per-pool or per-user. Enabling `curl` on `8.5` installs `php8.5-curl` (apt) and symlinks it into `/etc/php/8.5/fpm/conf.d/` via `phpenmod -v 8.5 -s fpm curl`. Every `php8.5-fpm` master and every `jabali-fpm@<user>.service` pinned to `8.5` is reloaded synchronously after the apt+phpenmod sequence.

**State is live, not persisted.** No migration, no `php_extensions` table. `php.ext.list` reads live host state via a single `dpkg-query -W 'php<v>-*'` call + a single `filepath.Glob("/etc/php/<v>/fpm/conf.d/*.ini")` per request. No drift vs the OS is possible because the panel does not record a separate opinion.

**The allowlist is hardcoded** in a Go package at repo-root `internal/phpext/` — 63 entries covering the Debian/Sury php8.x-\* catalogue (apcu, bcmath, curl, …, zip). Every entry is classified as built-in (ships in `php<v>-common`, enable/disable only, no apt) or non-built-in (apt install/remove + phpenmod). Bundled packages (e.g. `php<v>-xml` provides dom + simplexml + xml + xmlreader + xmlwriter as five logical entries → one apt pkg) are collapsed by the resolver. The `mysql` entry is a meta-install that maps to the single `php<v>-mysql` package (which ships `mysqli` + `pdo_mysql`); enable/disable on `mysql` itself is rejected as ambiguous.

**The agent re-validates at the trust boundary.** Panel-api pre-validates `:version` via `phpext.ValidVersion` and `:ext` via `phpext.Lookup`; bad input never reaches the Unix socket. The agent re-runs both checks before any `exec.Command`. Both copies import the same package — no drift between validators.

**The apt subprocess is serialized** by a `sync.Mutex` in the agent (`aptMu`) so two concurrent admin actions don't collide on `/var/lib/dpkg/lock-frontend`. phpenmod/systemctl run unserialized.

**Errors are structured** via `agentwire.CodeInvalidArgument` (bad version, unknown ext, built-in install/remove, mysql enable/disable, bad action), `CodeFailedPrecondition` (version not installed, ext not installed for enable, shared-package remove conflict, apt/phpenmod non-zero exit) and `CodeInternal` (dpkg/fs/systemctl unexpected failure). The API translates these to HTTP 400/409/500 via the existing `translateAgentError` helper; a new 409 mapping for `CodeFailedPrecondition` was added in this change.

## Alternatives Considered

### Alternative 1: Per-pool / per-user extensions (custom `extension=` lines in pool conf)
- **Pros**: True isolation — tenant A with xdebug doesn't slow tenant B.
- **Cons**: PHP-FPM shares a single module loader per master; "per-pool" extensions aren't actually a thing the runtime supports (you'd need per-pool masters, not per-pool workers). Would require a fork of php-fpm or per-user FPM masters with their own shared-object paths. Massive complexity for marginal value.
- **Why not**: Out of proportion to user demand. The 90% case is "enable redis on 8.5 for everyone".

### Alternative 2: Persist state in a `php_extensions` table
- **Pros**: Panel DB matches the rest of the architecture (DB is truth, reconciler converges). Fast `GET /extensions` — no dpkg call.
- **Cons**: Two sources of truth — if an admin runs `apt remove php8.5-curl` by hand, the DB says "enabled" and the reality says "gone". Requires a reconciler loop to detect + correct drift. Adds a migration, a model, a repository, a reconciler concern. Zero value over the live-read approach given `dpkg-query` is ~10ms on a warm box.
- **Why not**: Sources of truth should be minimized. The host IS the truth here; pretending otherwise is self-inflicted complexity.

### Alternative 3: Free-form "enter any php package name" input
- **Pros**: Operator flexibility — install experimental extensions without waiting for a panel release.
- **Cons**: Opens arbitrary apt-install to the HTTP layer. Even admin-only, this is a privilege escalation surface (install `php8.5-imap` which pulls in a CVE-vulnerable libc-client). Also: no UX guidance (admin types `xdebug3` which doesn't exist, gets a cryptic apt error).
- **Why not**: The allowlist IS the product here. "Which extensions does this panel support" is a documented, versioned contract, not a user choice.

## Consequences

### Positive

- **Zero migration, zero drift risk.** State on disk and state reported by the API are the same state.
- **Small blast radius.** Feature is 3 new Go files (`php_ext_{list,apply,shell}.go`) + 1 HTTP handler file + 1 UI tab. Plan fits in 6 steps.
- **Security trivial to reason about.** Every ext name reaching `exec.Command` passed through `phpext.Lookup` twice (panel + agent). Every version passed through `phpext.ValidVersion` twice. Subprocess args are structured (never `sh -c`), env is minimal (no inherited `PATH`).
- **Reconciler-free.** apply is synchronous; next list call reads the new state. If an individual per-user `jabali-fpm@<user>` reload fails, we log it — the next list call still shows the right state.

### Negative

- **Extension catalogue updates require a panel release.** Adding `php8.6-uuid` to the allowlist is a code change + version bump. Acceptable — the catalogue moves on a quarterly cadence at most.
- **apt lock contention is per-host, not per-tenant.** Two admins installing different extensions at the same time serialize on `aptMu`. On a 10-tenant box this is ~30s of added latency at most; on a 1000-tenant box we'd need a queue. We don't have 1000-tenant boxes.
- **No rollback of a half-applied state.** apt succeeded + phpenmod failed means the package is on disk but the module isn't enabled. Next list call surfaces the reality; operator retries. The plan documents this in the runbook.

## Implementation

- **Commits**:
  - Step 1 `5e6b2ab` — `internal/phpext/` allowlist package (63 entries, alias resolver, `ValidVersion`/`Lookup`/`ResolvePackages`).
  - Step 2 `8c06612` — `php.ext.list` + `php.ext.apply` agent commands; contract lock in `panel-api/internal/agent/php_ext_contract_test.go` with 6 golden JSON fixtures; `agentwire.CodeFailedPrecondition` + 409 mapping.
  - Step 3 `f345cce` — `/api/v1/admin/php/versions/:version/extensions[/:ext/apply]` HTTP handlers.
  - Step 4 `2c8b3a3` — AntD Tabs UI; `panel-ui/src/shells/admin/php/` with `PHPVersionsPage`, `VersionsTab`, `PHPExtensionsTab`.
- **Plan**: `plans/php-extensions-tab.md` (Opus-reviewed; CRITICAL+HIGH folded in).
- **Runbook**: `docs/runbooks/php-extensions.md`.
