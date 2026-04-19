# PHP Extensions Tab — Admin Page (M9.6)

Add a second tab, **PHP Extensions**, to the existing admin PHP page. The existing tab keeps its current behaviour (install / reload / set default). The new tab lets an admin pick an *installed* PHP version and then install/remove/enable/disable a fixed allowlist of 63 extensions, server-wide.

**Scope:** server-wide per PHP version (not per-user, not per-pool). Enabling `curl` on `8.5` means `apt-get install php8.5-curl` + `phpenmod -v 8.5 -s fpm curl` + reload every `php8.5-fpm` master on the host (plus every `jabali-fpm@<user>` that pins `8.5`).

**Status (2026-04-19):** parked objective from `/blueprint`. Dispatcher can pick up at Step 1. Six steps, ~4 days wall clock.

---

## 0. Objective & non-goals

**Objective.** Admin → PHP page now has two tabs:

1. **PHP Versions** (unchanged) — Installed / Default / FPM columns + Install/Reload actions. Already shipped as `panel-ui/src/shells/admin/php-pools/PHPPoolsList.tsx`.
2. **PHP Extensions** (new) — version dropdown (populated from `/admin/php/versions/status` filtered to `installed=true`) + table of 63 extensions with columns `Extension | Status (enabled ✓ / disabled ✗) | Installed (Yes/No) | Action` + client-side search.

The allowlist (exactly 63, alphabetical):
```
apcu bcmath bz2 calendar ctype curl dba dom enchant exif ffi fileinfo ftp gd
gettext gmp gnupg iconv igbinary imagick imap intl ldap mailparse mbstring
mcrypt memcached mongodb msgpack mysql mysqli mysqlnd odbc opcache pdo
pdo_mysql pdo_pgsql pdo_sqlite pgsql phar posix pspell readline redis shmop
simplexml snmp soap sockets sqlite3 ssh2 sysvmsg sysvsem sysvshm tidy tokenizer
xdebug xml xmlreader xmlwriter xsl yaml zip
```

**Non-goals.**
- Per-user / per-pool overrides (out of scope for this milestone; `php.ini` overrides already exist via M9 `PHPPoolIniOverride`).
- Installing PHP *versions* (already owned by M9 admin UI tab 1).
- Managing extensions *not* in the allowlist (anything custom is an ADR discussion, not a UI action).
- Persisting extension state in the Jabali DB. Source of truth is dpkg + filesystem (`/etc/php/<v>/mods-available/*`). Compute live on every request; surface last apt stderr as `last_error` in the response on failure.

---

## 1. Design decisions (locked)

1. **State is live, not persisted.** No migration. `php.ext.list` shells out to `dpkg-query -W` + scans `/etc/php/<v>/fpm/conf.d/*.ini` (symlinks to `mods-available`). Cheap; one subprocess per call; no drift risk.
2. **Allowlist is a Go source-of-truth** at `internal/phpext/` (repo root, NOT under `panel-api/internal/`) importable by both `panel-api` and `panel-agent`. **Module layout:** this repo is a single Go module (`git.linux-hosting.co.il/shukivaknin/jabali2`); `panel-api/` and `panel-agent/` are subdirectories, not separate modules — no `go.work`, no replace directives. Package MUST live at repo root `internal/` because Go's internal rule restricts `panel-api/internal/*` to `panel-api/...` callers only, which excludes `panel-agent/`. The import path is `git.linux-hosting.co.il/shukivaknin/jabali2/internal/phpext`, usable from both binaries directly. The panel validates requests against the allowlist before calling the agent; the agent re-validates (defence in depth) because the socket is a trust boundary. Map shape: `ext name → Spec{Packages []string, EnableName string, BuiltIn bool}`.
3. **Aliases & bundled packages.** Some "extensions" in the list don't map 1:1 to an apt package:
   - `mysql` → meta-package label. `ResolvePackages("<v>","mysql")` returns `["php<v>-mysql"]` (a single Sury/Debian package that bundles both the `mysqli` and `pdo_mysql` modules). `EnableName="mysqli"`. **Allowed actions on `mysql` are install/remove only**; enable/disable return `CodeInvalidArgument` with message `"ambiguous target; enable/disable mysqli or pdo_mysql directly"`. The UI row for `mysql` hides enable/disable buttons.
   - `mysqlnd`, `posix`, `tokenizer`, `ctype`, `phar`, `calendar`, `exif`, `ftp`, `sockets`, `sysvmsg`, `sysvsem`, `sysvshm`, `fileinfo`, `iconv`, `opcache`, `pdo` → `BuiltIn: true`. Always installed when `php<v>-common` or `php<v>-cli` is present; Action column shows only Enable/Disable, never Install/Remove.
   - `dom`, `simplexml`, `xml`, `xmlreader`, `xmlwriter` → bundled in `php<v>-xml`. One apt action installs all five; enable/disable is per-module via `phpenmod`.
   - All other names map directly to `php<v>-<ext>`.
4. **Enable/disable uses `phpenmod -v <v> -s fpm`** (and `phpdismod`). Tool is part of `php-common` on Debian/Sury; already present on any box with PHP installed. After any apt or phpen/dismod call, agent runs `systemctl reload php<v>-fpm` (if present) **and** reloads all `jabali-fpm@<user>.service` where `/etc/jabali-panel/user-phpver/<user>` pins `<v>`.
5. **Agent commands:**
   - `php.ext.list {version}` → `{ version, extensions: [{name, installed, enabled, built_in, last_error?}] }` (last_error is never persisted here; only surfaced if a previous `apply` in this request cycle failed, which won't apply to `list`).
   - `php.ext.apply {version, ext, action}` where action ∈ `{install, remove, enable, disable}` → `{version, ext, installed, enabled, last_error?}`.
   Both commands validate version matches `^\d+\.\d+$` and `ext` is in the allowlist; agent rejects anything else with `CodeInvalidArgument`.
6. **API endpoints (admin-only):**
   - `GET  /api/v1/admin/php/versions/:version/extensions`
   - `POST /api/v1/admin/php/versions/:version/extensions/:ext/apply` with body `{"action":"install|remove|enable|disable"}`
   Mounted under `RegisterPHPVersionAdminRoutes` (same group as `/versions/status`).
7. **UI.** Rename `PHPPoolsList.tsx` → `PHPVersionsPage.tsx` (use `gitnexus_rename`, not find-and-replace), wrap current JSX in AntD `<Tabs>` with two `items`. Second tab is a new `PHPExtensionsTab` component. Use TanStack Query for data fetching. Search is client-side `Input.Search` filtering the already-loaded array.
8. **Reconciler.** No changes. The `apply` command performs synchronous install + reload and returns the fresh state. If apt takes >5min, the HTTP request times out, but the agent-side install continues; next `list` call reflects reality. That's acceptable — same contract as `php.version.install`.
9. **install.sh.** No new system packages. Sury + `php-common` already install `phpenmod`/`phpdismod`. Add a one-line comment noting that ext packages are installed on-demand by the panel, not at boot.
10. **RBAC.** Admin-only on every route, enforced by `RequireAdmin` middleware on the admin group in `panel-api/internal/server/routes.go`.

---

## 2. Pre-flight (dispatcher, once)

```bash
cd /home/shuki/projects/jabali2-b
git status                                              # must be clean
git fetch origin
git log --oneline origin/main..HEAD | head             # your branch diff vs origin
npx gitnexus analyze                                    # ensure index current; rerun if stale warning
```

Gitea is the remote; `gh` is not authed. Agents use feature branches + commits only; dispatcher merges to `main` and pushes to Gitea manually.

---

## 3. Dependency graph

```
Step 1 (allowlist)  ──┬──→  Step 2 (agent cmds)  ──┐
                      │                             ├──→  Step 4 (UI)  ──→  Step 5 (E2E)
                      └──→  Step 3 (API)  ──────────┘
                                                     ↑
                                           Step 6 (ADR+docs) in parallel from here
```

Parallelism: Steps 2 and 3 can run concurrently (Step 3 uses `MockAgentClient`). Step 6 can start once Step 3 contract is merged. Critical path is 1 → 2 → 4 → 5.

---

## 4. Cross-cutting mandates (apply to every step)

These apply to every step. Do not repeat in step bodies.

- **Feature branch only.** `git checkout -b <step-slug>` before first edit. Never commit to `main`. Never `git push`. Dispatcher pushes.
- **Impact analysis before edits.** Before touching any `.go` / `.tsx` / migration file, run `mcp__gitnexus__impact({target: "<symbol>", direction: "upstream"})` and include the blast radius in your step report. Stop on HIGH/CRITICAL and report back before editing.
- **Detect changes before commit.** Run `mcp__gitnexus__detect_changes` and confirm the symbol set matches the step scope.
- **Renames via gitnexus.** Use `mcp__gitnexus__rename`; never find-and-replace.
- **Tests ≥80% coverage** on new code (`make coverage-check`).
- **No stale comments.** Don't leave `// TODO: remove when X lands` or `// removed in step N` breadcrumbs. Either delete the code or leave it clean.
- **install.sh updated** for any system package that's added (none expected in this plan).
- **Final report per step:** branch name, commit SHAs, `git log main..<branch>` summary, test output snippet, gitnexus impact + detect_changes excerpts.

---

## 5. Steps

### Step 1 — `phpext` allowlist package

**Slug:** `phpext-allowlist`
**Model:** default
**Depends on:** nothing
**Parallel with:** nothing (head)
**Rollback:** `trash internal/phpext/ && git restore --staged . && git checkout -- .` (global rule: `trash`, never `rm -rf`).

**Context brief.** We need a Go package that both `panel-api` and `panel-agent` import to validate and resolve extension names. It holds the canonical 63-entry list, the ext→packages map, and helpers. No external callers yet — this step ships the package + tests only.

**Files to create.**
- `internal/phpext/phpext.go` — types + data + resolver.
- `internal/phpext/phpext_test.go` — table-driven tests.

**Module-path sanity check (do before coding).** `grep -r '^module' go.mod` → confirms single module `git.linux-hosting.co.il/shukivaknin/jabali2`. The package import path from `panel-agent` is `git.linux-hosting.co.il/shukivaknin/jabali2/internal/phpext`. If this ever changes (repo split into multi-module workspace), update Step 2 imports accordingly.

**Data model (write this exactly).**

```go
// internal/phpext/phpext.go
package phpext

// Spec describes one logical extension offered by the admin UI.
type Spec struct {
    // Name is the user-facing identifier (what the UI table shows).
    Name string
    // Packages is the list of apt packages that provide this extension. Empty
    // for BuiltIn. Multiple packages means install/remove acts on all at once
    // (e.g. "mysql" → {"mysqli", "pdo_mysql"}).
    Packages []string
    // EnableName is the phpenmod/phpdismod module name. Usually == Name; for
    // bundled groups it's the underlying ini file's base name.
    EnableName string
    // BuiltIn is true for extensions bundled into php<v>-common / php<v>-cli.
    // Install/Remove actions are hidden for these; only Enable/Disable.
    BuiltIn bool
}

// All returns the full allowlist in alphabetical order. Panel and agent both
// iterate this to render the extensions table.
func All() []Spec { /* 63 entries, hardcoded */ }

// Lookup returns the Spec for name, or ok=false if not in the allowlist.
func Lookup(name string) (Spec, bool) { /* ... */ }

// ResolvePackages returns the concrete apt package names for (version, ext),
// e.g. ("8.5","curl") → ["php8.5-curl"], ("8.5","mysql") → ["php8.5-mysqli",
// "php8.5-pdo-mysql"]. Returns an error if ext is not in the allowlist or if
// version fails the ^\d+\.\d+$ check. Built-ins return (nil, nil).
func ResolvePackages(version, ext string) ([]string, error) { /* ... */ }

// ValidVersion reports whether v matches ^\d+\.\d+$. Kept here so callers
// don't re-roll the regex.
func ValidVersion(v string) bool { /* ... */ }
```

**Map accuracy is load-bearing.** Use this as the ground truth for the bundled/built-in classification:

| Ext name | Packages | EnableName | BuiltIn |
|---|---|---|---|
| `apcu` | `apcu` | `apcu` | false |
| `bcmath` | `bcmath` | `bcmath` | false |
| `bz2` | `bz2` | `bz2` | false |
| `calendar` | — | `calendar` | **true** |
| `ctype` | — | `ctype` | **true** |
| `curl` | `curl` | `curl` | false |
| `dba` | `dba` | `dba` | false |
| `dom` | `xml` | `dom` | false |
| `enchant` | `enchant` | `enchant` | false |
| `exif` | — | `exif` | **true** |
| `ffi` | — | `ffi` | **true** |
| `fileinfo` | — | `fileinfo` | **true** |
| `ftp` | — | `ftp` | **true** |
| `gd` | `gd` | `gd` | false |
| `gettext` | — | `gettext` | **true** |
| `gmp` | `gmp` | `gmp` | false |
| `gnupg` | `gnupg` | `gnupg` | false |
| `iconv` | — | `iconv` | **true** |
| `igbinary` | `igbinary` | `igbinary` | false |
| `imagick` | `imagick` | `imagick` | false |
| `imap` | `imap` | `imap` | false |
| `intl` | `intl` | `intl` | false |
| `ldap` | `ldap` | `ldap` | false |
| `mailparse` | `mailparse` | `mailparse` | false |
| `mbstring` | `mbstring` | `mbstring` | false |
| `mcrypt` | `mcrypt` | `mcrypt` | false |
| `memcached` | `memcached` | `memcached` | false |
| `mongodb` | `mongodb` | `mongodb` | false |
| `msgpack` | `msgpack` | `msgpack` | false |
| `mysql` | `mysql` | `mysqli` | false (meta-install only; enable/disable rejected — see §1.3) |
| `mysqli` | `mysql` | `mysqli` | false |
| `mysqlnd` | — | `mysqlnd` | **true** |
| `odbc` | `odbc` | `odbc` | false |
| `opcache` | — | `opcache` | **true** |
| `pdo` | — | `pdo` | **true** |
| `pdo_mysql` | `mysql` | `pdo_mysql` | false |
| `pdo_pgsql` | `pgsql` | `pdo_pgsql` | false |
| `pdo_sqlite` | `sqlite3` | `pdo_sqlite` | false |
| `pgsql` | `pgsql` | `pgsql` | false |
| `phar` | — | `phar` | **true** |
| `posix` | — | `posix` | **true** |
| `pspell` | `pspell` | `pspell` | false |
| `readline` | `readline` | `readline` | false |
| `redis` | `redis` | `redis` | false |
| `shmop` | — | `shmop` | **true** |
| `simplexml` | `xml` | `simplexml` | false |
| `snmp` | `snmp` | `snmp` | false |
| `soap` | `soap` | `soap` | false |
| `sockets` | — | `sockets` | **true** |
| `sqlite3` | `sqlite3` | `sqlite3` | false |
| `ssh2` | `ssh2` | `ssh2` | false |
| `sysvmsg` | — | `sysvmsg` | **true** |
| `sysvsem` | — | `sysvsem` | **true** |
| `sysvshm` | — | `sysvshm` | **true** |
| `tidy` | `tidy` | `tidy` | false |
| `tokenizer` | — | `tokenizer` | **true** |
| `xdebug` | `xdebug` | `xdebug` | false |
| `xml` | `xml` | `xml` | false |
| `xmlreader` | `xml` | `xmlreader` | false |
| `xmlwriter` | `xml` | `xmlwriter` | false |
| `xsl` | `xsl` | `xsl` | false |
| `yaml` | `yaml` | `yaml` | false |
| `zip` | `zip` | `zip` | false |

Note: the `Packages` column shows the bare suffix; `ResolvePackages` prefixes `php<v>-` and converts `_` to `-` (apt naming). So ext `pdo_mysql` on version `8.5` → `php8.5-mysql` (single apt package that ships both the `mysqli` and `pdo_mysql` modules).

**Tests (minimum).**
- `TestAll_Length` — `len(All()) == 63`
- `TestAll_Alphabetical` — result is strictly increasing
- `TestAll_NoDuplicates`
- `TestLookup_HitAndMiss` — every name in All() is Lookup-able; unknown returns ok=false
- `TestResolvePackages_Direct` — `("8.5","curl")` → `["php8.5-curl"]`
- `TestResolvePackages_BundledXML` — `("8.5","dom")` → `["php8.5-xml"]` (no duplicate even though `xml`, `xmlreader`, `xmlwriter`, `simplexml` also resolve to `php8.5-xml`)
- `TestResolvePackages_BuiltIn` — `("8.5","posix")` → `(nil, nil)`
- `TestResolvePackages_MysqlAlias` — `("8.5","mysql")` → `["php8.5-mysql"]` exactly; and `Lookup("mysql").EnableName == "mysqli"`
- `TestResolvePackages_BadVersion` — `("8","curl")` returns non-nil error
- `TestResolvePackages_UnknownExt` — `("8.5","nope")` returns non-nil error
- `TestValidVersion` — table-driven: `"8.5"→true`, `"8"→false`, `"8.5.1"→false`, `"abc"→false`, `""→false`

**Exit criteria.**
- `make fmt vet lint test` clean.
- `go test -cover ./internal/phpext/...` ≥ 95% (pure data + pure functions; no excuse to miss).
- `gitnexus_detect_changes` shows only files under `internal/phpext/`.

---

### Step 2 — Agent commands `php.ext.list` + `php.ext.apply`

**Slug:** `agent-php-ext-commands`
**Model:** strongest (subprocess orchestration, privileged apt/systemctl, security-sensitive)
**Depends on:** Step 1
**Parallel with:** Step 3
**Rollback:** unregister from `init()` blocks; `git revert` the branch.

**Context brief.** Add two handlers to `panel-agent/internal/commands/`. They shell out to apt, phpenmod/phpdismod, dpkg-query, systemctl. Must sanitize every arg (version regex + allowlist membership before any exec). Reads must be cheap; writes must be synchronous w.r.t. filesystem state but may be slow (apt).

**Files to create.**
- `panel-agent/internal/commands/php_ext_list.go`
- `panel-agent/internal/commands/php_ext_list_test.go`
- `panel-agent/internal/commands/php_ext_apply.go`
- `panel-agent/internal/commands/php_ext_apply_test.go`

**Command contracts (match exactly).**

```
php.ext.list
  params:  {"version":"8.5"}
  returns: {"version":"8.5","extensions":[
             {"name":"apcu","installed":false,"enabled":false,"built_in":false},
             {"name":"bcmath","installed":true, "enabled":true, "built_in":false},
             ...   // all 63, ordered per phpext.All()
           ]}

php.ext.apply
  params:  {"version":"8.5","ext":"curl","action":"install"}
              action ∈ {install, remove, enable, disable}
  returns: {"version":"8.5","ext":"curl","installed":true,"enabled":true}
  on failure: AgentError{Code:CodeFailedPrecondition, Message:"<apt/phpenmod stderr, truncated to 512 bytes>"}
```

**Validation rules (agent-side, non-negotiable — socket is the trust boundary, don't rely on API pre-checks).**
1. `phpext.ValidVersion(version)` — else `CodeInvalidArgument`.
2. Version must appear in `listInstalledPHPVersions()` (reuse helper from `php_version_list.go`) — else `CodeFailedPrecondition` (can't manage extensions for a version that isn't installed).
3. `phpext.Lookup(ext)` must return ok=true — else `CodeInvalidArgument`. **This check runs before any `exec.Command` touches the host**, even though the API already validated. The agent cannot trust an ext name it didn't look up itself.
4. For `action ∈ {install, remove}` on a `BuiltIn` ext → `CodeInvalidArgument` with message `"extension <ext> is built in; use enable/disable"`.
5. For `action ∈ {enable, disable}` on ext `mysql` → `CodeInvalidArgument` with message `"ambiguous target; enable/disable mysqli or pdo_mysql directly"`.
6. `action ∈ {install, remove, enable, disable}` else `CodeInvalidArgument`.

**Subprocess wrappers (new file `panel-agent/internal/commands/php_ext_shell.go`).**

Keep these behind function variables so tests can inject:

```go
var (
    runApt       = defaultRunApt        // apt-get -y install|remove <pkgs...>
    runPhpenmod  = defaultRunPhpenmod   // phpenmod  -v <v> -s fpm <mod>
    runPhpdismod = defaultRunPhpdismod  // phpdismod -v <v> -s fpm <mod>
    runReload    = defaultRunReload     // systemctl reload php<v>-fpm + jabali-fpm@*
    runDpkgQuery = defaultRunDpkgQuery  // dpkg-query -W -f='${Package}\t${Status}\n' <pattern>
)

// aptMu serializes apt invocations across concurrent apply calls so a second
// admin action waits instead of colliding on /var/lib/dpkg/lock-frontend. Held
// only for the apt subprocess; phpenmod/reload are not serialized.
var aptMu sync.Mutex
```

**Mock pattern for tests.** Dependency injection is via reassigning these package-level function variables inside each test, with a `defer` to restore. Example shape (write one concrete case like this in `php_ext_apply_test.go`):

```go
func TestApply_InstallCurl(t *testing.T) {
    oldApt, oldEn, oldRel := runApt, runPhpenmod, runReload
    defer func() { runApt, runPhpenmod, runReload = oldApt, oldEn, oldRel }()

    var aptCalls, enCalls, relCalls [][]string
    runApt       = func(ctx context.Context, args ...string) ([]byte, error) { aptCalls = append(aptCalls, args); return nil, nil }
    runPhpenmod  = func(ctx context.Context, v, mod string) ([]byte, error)   { enCalls  = append(enCalls,  []string{v, mod}); return nil, nil }
    runReload    = func(ctx context.Context, v string) error                  { relCalls = append(relCalls, []string{v}); return nil }
    // ... invoke handler, assert calls
}
```
Tests NEVER call the real apt/phpenmod/systemctl. `go test -race` must pass with zero subprocess side effects.

All `exec.Command` calls must:
- Take structured args (never `sh -c`).
- Set `cmd.Env` to a minimal, non-inherited env: `PATH=/usr/sbin:/usr/bin:/sbin:/bin`, `DEBIAN_FRONTEND=noninteractive`.
- Capture stdout + stderr.
- Honor `ctx` via `exec.CommandContext`.
- Timeout per call: `list` 10s. `apply` overall budget 300s, covering *all* phases (apt + phpenmod/phpdismod + reloadFPMs). Budget split: apt gets up to 270s; phpen/dis ≤5s; `reloadFPMs` runs with its own internal 60s deadline and is called synchronously but its errors are logged-only (never block the apply response). If apt finishes early, reloadFPMs gets more headroom — the split is advisory, the outer `ctx` is the only hard cap.

**Install/remove logic.**
- Resolve packages via `phpext.ResolvePackages(v, ext)`. For `dom`/`simplexml`/`xml`/etc., dedupe — don't pass `["php8.5-xml","php8.5-xml"]`.
- `install`: `apt-get -y install <pkgs>` then `phpenmod -v <v> -s fpm <enableName>` then `reloadFPMs(v)`.
- `remove`: `apt-get -y remove <pkgs>` then `reloadFPMs(v)`. `phpdismod` is not needed — removal yanks the `.ini` symlinks.
  - **Refuse to remove** if any other allowlist ext depends on the same package (e.g. `remove mysql` when `mysqli` is still present via the same apt pkg). Compute this by iterating allowlist and checking for shared `Packages`. Return `CodeFailedPrecondition` with the conflicting ext name.
- `enable`: `phpenmod -v <v> -s fpm <enableName>` + reload. Precondition: ini file must exist in `mods-available`; else `CodeFailedPrecondition` `"extension not installed"`.
- `disable`: `phpdismod -v <v> -s fpm <enableName>` + reload.

**`reloadFPMs(version)`.**
1. `systemctl reload php<v>-fpm.service` if unit exists (don't fail if absent — host may have masked it per ADR-0025 cutover).
2. `systemctl list-units 'jabali-fpm@*.service' --state=running --no-legend -o json` → for each, read `/etc/jabali-panel/user-phpver/<user>`; if matches `<v>`, `systemctl reload jabali-fpm@<user>.service`.
3. Return a soft error (logged, not surfaced) if any individual reload fails — don't block the apply response on a stray user FPM issue. The user's site is broken either way; surfacing stdout/stderr gives us more debug signal next sweep.

**State read (`php.ext.list`).**
1. Resolve all packages across the allowlist (dedupe) → single `dpkg-query -W -f='${Package}\t${Status}\n' php<v>-*` call (one subprocess total, not one per ext).
2. For each ext: `installed = !BuiltIn && allRequiredPackagesInstalled(dpkgResult, pkgs)`. Built-ins: installed = true iff `php<v>-common` is installed.
3. `enabled`: check `/etc/php/<v>/fpm/conf.d/*<enableName>.ini` exists (symlink to mods-available). Read once per call with a single `filepath.Glob`.

**Tests (table-driven, ≥80% cover).** Inject `runApt`, `runPhpenmod`, `runPhpdismod`, `runReload`, `runDpkgQuery` with fakes. Cases:
- `list` for a version with nothing installed → all 63 show `installed:false, enabled:false` (builtins show as `installed:true` if `php<v>-common` is reported installed).
- `list` rejects bogus version.
- `apply install curl 8.5` → calls apt with `["php8.5-curl"]`, calls phpenmod with `curl`, calls reload. Returns `installed:true, enabled:true`.
- `apply install mysql 8.5` → apt called with `["php8.5-mysql"]` (once, not twice).
- `apply install posix 8.5` → rejected with `CodeInvalidArgument` (built-in).
- `apply remove curl 8.5` → apt remove + reload. No phpdismod.
- `apply remove xml 8.5` when `dom` still installed via same `php8.5-xml` pkg → rejected with `CodeFailedPrecondition`.
- `apply enable/disable opcache 8.5` → phpenmod/phpdismod called; no apt.
- `apply bogus action` → `CodeInvalidArgument`.
- `apply` on a version not in `listInstalledPHPVersions()` → `CodeFailedPrecondition`.
- apt returns non-zero → `CodeFailedPrecondition` with truncated stderr.

**Registration.** Add `init()` in each file:
```go
func init() {
    Default.Register("php.ext.list", phpExtListHandler)
    Default.Register("php.ext.apply", phpExtApplyHandler)
}
```

**Exit criteria.**
- `go test -race -cover ./panel-agent/internal/commands/...` ≥ 80% on the two new files.
- `make vet lint` clean.
- `gitnexus_detect_changes` shows only the four new files + no changes outside `panel-agent/internal/commands/`.

---

### Step 3 — API handlers `/admin/php/versions/:version/extensions`

**Slug:** `api-php-ext-routes`
**Model:** default
**Depends on:** Step 1
**Parallel with:** Step 2 (use `MockAgentClient`)
**Rollback:** unregister routes in `panel-api/internal/server/routes.go`; `git revert`.

**Context brief.** Thin HTTP layer. Validates version + ext against `phpext` before forwarding; proxies to agent; translates `AgentError` via the existing `translateAgentError` helper.

**Files to create / edit.**
- Create: `panel-api/internal/api/php_extensions.go`
- Create: `panel-api/internal/api/php_extensions_test.go`
- Edit:   `panel-api/internal/api/php_versions.go` — extend `RegisterPHPVersionAdminRoutes` to call a new `RegisterPHPExtensionRoutes(rg, cli)` (or mount directly under the same `/php` group, whichever keeps the diff smaller). **Run `gitnexus_impact` on `RegisterPHPVersionAdminRoutes` before editing.**

**Routes (admin-only).**
```
GET  /api/v1/admin/php/versions/:version/extensions
POST /api/v1/admin/php/versions/:version/extensions/:ext/apply
```
Body for POST:
```json
{"action":"install"}        // ∈ install|remove|enable|disable
```

**Handler behaviour.**
- Validate `version` via `phpext.ValidVersion` → 400 if bad (don't call agent).
- Validate `ext` via `phpext.Lookup` → 404 if unknown.
- Validate `action` against the fixed set → 400 if bad.
- Timeout: 10s for GET, 5min for POST (`adminActionTimeout` already exists — reuse).
- On agent error, `translateAgentError(err)` → response.
- On success, forward raw JSON with `c.Data(200, "application/json; charset=utf-8", raw)`.

**Request / response types (lock these — both panel-api handler and Step 4 TS must match).**

```go
// Request body for POST .../apply
type ExtensionApplyRequest struct {
    Action string `json:"action" binding:"required,oneof=install remove enable disable"`
}

// Response for both GET list and POST apply — match the agent command output
type ExtensionState struct {
    Name      string `json:"name"`
    Installed bool   `json:"installed"`
    Enabled   bool   `json:"enabled"`
    BuiltIn   bool   `json:"built_in"`
    LastError string `json:"last_error,omitempty"`
}

type ExtensionListResponse struct {
    Version    string           `json:"version"`
    Extensions []ExtensionState `json:"extensions"`
}

type ExtensionApplyResponse struct {
    Version   string `json:"version"`
    Ext       string `json:"ext"`
    Installed bool   `json:"installed"`
    Enabled   bool   `json:"enabled"`
    LastError string `json:"last_error,omitempty"`
}
```

TS side in Step 4 mirrors these as `interface` declarations with identical field names.

**Tests (table-driven).**
- 200 happy path for GET and POST (use `MockAgentClient` returning a canned JSON body).
- 400 bad version format.
- 400 unknown ext (not in allowlist).
- 400 bad action.
- 403 non-admin (RBAC integration test via full router setup).
- Agent `CodeInvalidArgument` → 400, `CodeFailedPrecondition` → 409, `CodeInternal` → 500. Confirm `translateAgentError` mapping.

**Exit criteria.**
- `go test -cover ./panel-api/internal/api/...` — delta covered ≥ 80%.
- `gitnexus_impact` run on `RegisterPHPVersionAdminRoutes` logged in your commit body or step report.

---

### Step 4 — UI: two-tab layout + Extensions tab

**Slug:** `ui-php-extensions-tab`
**Model:** default
**Depends on:** Step 3 **merged AND deployed to the test host** (not just on a branch). Verify before starting: `curl -H "Authorization: Bearer <admin-token>" http://127.0.0.1:8443/api/v1/admin/php/versions/8.5/extensions | jq '.extensions | length'` returns `63`. If it 404s, Step 3 isn't live — stop and escalate.
**Parallel with:** nothing
**Rollback:** `git checkout -- panel-ui/src/App.tsx panel-ui/src/shells/admin/php-pools/ panel-ui/src/shells/admin/php/` — restore the original import path in App.tsx, then drop any newly created `admin/php/` directory. Prefer dropping the whole feature branch.

**Context brief.** Rename the misnamed component, wrap its JSX in `<Tabs>`, add a second tab that renders a version dropdown + extensions table with search.

**Existing import references to repair.** `panel-ui/src/App.tsx` currently has (lines 81-82):
```
import { PHPPoolsList } from "./shells/admin/php-pools/PHPPoolsList";
import { PHPPoolEdit } from "./shells/admin/php-pools/PHPPoolEdit";
```
Plan: leave `PHPPoolEdit` exactly where it is (it's a different concept — per-user PHP pool editing, separate from admin versions). Replace the `PHPPoolsList` import with a new `PHPVersionsPage` import from the new path. Check with `grep -rn 'PHPPoolsList' panel-ui/src` before the rename; any hit other than App.tsx must be resolved before proceeding.

**Icon imports to add.** AntD: `Tabs` (if not already imported). `@ant-design/icons`: `ApiOutlined` for the Extensions tab label and the per-row extension name icon. (`CodeOutlined` is already imported in the existing file.) Run `grep -n '@ant-design/icons' panel-ui/src/shells/admin/php-pools/PHPPoolsList.tsx` to see the existing set and add only what's missing.

**Files to edit / create.**
1. **Rename via `mcp__gitnexus__rename`:** `panel-ui/src/shells/admin/php-pools/PHPPoolsList.tsx` → `panel-ui/src/shells/admin/php/PHPVersionsPage.tsx`. The old directory (`php-pools/`) still holds `PHPPoolEdit.tsx` — leave that where it is; don't rename the folder.
2. Update imports/routes in `panel-ui/src/App.tsx` where `PHPPoolsList` is referenced. (Run `gitnexus_impact` on `PHPPoolsList` first.)
3. Create `panel-ui/src/shells/admin/php/PHPExtensionsTab.tsx` (component for tab 2).
4. Create `panel-ui/src/shells/admin/php/PHPExtensionsTab.test.tsx` if component-level Vitest tests are used elsewhere in the repo (check `panel-ui/package.json` scripts — if no `test:unit`, skip and rely on Playwright in Step 5).

**`PHPVersionsPage.tsx` shape.**
```tsx
export const PHPVersionsPage = () => {
  const [tab, setTab] = useState<'versions' | 'extensions'>('versions');
  return (
    <Tabs
      activeKey={tab}
      onChange={k => setTab(k as any)}
      centered
      items={[
        { key: 'versions',   label: <><CodeOutlined /> PHP Versions</>,   children: <VersionsTable /> },
        { key: 'extensions', label: <><ApiOutlined /> PHP Extensions</>, children: <PHPExtensionsTab /> },
      ]}
    />
  );
};
```
Move the existing table + handlers into a `VersionsTable` subcomponent in the same file (or a sibling file — keep the split clean).

**`PHPExtensionsTab.tsx`.**
- Fetches `/admin/php/versions/status` on mount (for the dropdown options).
- Dropdown renders only versions where `installed === true`; first such version is the default selection.
- When a version is selected, fetches `/admin/php/versions/:version/extensions`.
- Renders AntD `Table` with columns:
  - `Extension` — monospace font, with a puzzle icon prefix (`<ApiOutlined />` from `@ant-design/icons`).
  - `Status` — green `<CheckCircleOutlined />` if `enabled`, grey `<CloseCircleOutlined />` otherwise.
  - `Installed` — AntD `Tag` "Yes"/"No" coloured green/default.
  - `Action` — button:
    - `built_in && !enabled` → `Enable`
    - `built_in && enabled`  → `Disable`
    - `!installed`           → `Install`
    - `installed && enabled` → `Disable`
    - `installed && !enabled`→ `Enable` (primary) + secondary `Remove` link
  - On click: POST to apply endpoint, show `notification` (success/error), refetch row. Disable button while in-flight.
- Search box: `Input.Search` above the table, filters rows client-side by substring match on `name`.
- Loading state: `<Spin>` over the whole tab while fetching.

**Styling.** Match the existing `PHPPoolsList.tsx` card / typography / spacing exactly — the user wants pixel parity with the screenshot in the brief.

**Accessibility.** Keep the search input labelled (`placeholder="Search"` + aria-label). Action buttons carry explicit labels, not just icons.

**Exit criteria.**
- `npm run build` in `panel-ui/` exits 0.
- `npm run typecheck` (or `tsc --noEmit`) clean.
- Manually: `make run` + hit `/admin/php` in a browser → tabs work; both work; Versions tab unchanged.
- `gitnexus_detect_changes` shows only `panel-ui/src/shells/admin/` + `App.tsx`.

---

### Step 5 — Playwright E2E

**Slug:** `e2e-php-extensions`
**Model:** default
**Depends on:** Step 4 merged + a disposable test host (not prod)
**Parallel with:** nothing
**Rollback:** delete the spec.

**Context brief.** One spec, hits the deployed dev host (Playwright base URL as configured in other specs). Flows through install → disable → uninstall for a *safe, side-effect-free* extension.

**Test file.** `panel-ui/tests/e2e/php-extensions.spec.ts` (or wherever existing Playwright specs live — mirror `filebrowser.spec.ts` / `sftp.spec.ts` layout).

**Journey (pick `bcmath` as the test ext — small, no daemon, minimal side effects).**
1. **`beforeAll`: force clean slate.** Call the API `POST /api/v1/admin/php/versions/8.5/extensions/bcmath/apply` with `action: "remove"` — ignore 409 (already absent). This guarantees every run starts with bcmath uninstalled, so assertions don't branch on fixture state.
2. **`afterAll`: restore to same clean slate** — another `remove` call so reruns stay deterministic.
3. Login as admin.
4. Navigate to `/admin/php`.
5. Assert default tab is "PHP Versions" (heading visible).
6. Click "PHP Extensions" tab.
7. Assert version dropdown has at least one option (the default-installed `8.5`). Select `8.5`.
8. Wait for the extensions table. Assert `bcmath` row: Installed="No", Status=disabled.
9. Click Install → wait for Status green + Installed "Yes" (up to 15s — apt).
10. Click Disable → Status grey, Installed remains "Yes".
11. Click Enable → Status green again.
12. Click Remove → Installed "No", Status grey.
13. Screenshots on each state change (Playwright auto-snapshot).

The branch-on-state logic was removed intentionally — deterministic setup/teardown is cheaper to maintain than branching.

**Timeouts.** Per-action waits of 10s for state flips (apt install can be slow even for a small ext on a warm apt cache); 120s total spec timeout.

**Flaky-test policy.** If the test flakes twice in a row on CI, the dispatcher quarantines it (`test.fixme`) and opens a ticket — do not weaken assertions to paper over apt latency.

**Exit criteria.** Spec passes once locally against a live panel, then a second time back-to-back to confirm idempotency.

---

### Step 6 — ADR-0031 + BLUEPRINT + runbook

**Slug:** `docs-php-ext-adr`
**Model:** default
**Depends on:** Step 3 contract locked (can start in parallel once Step 3 is merged)
**Parallel with:** Steps 4, 5
**Rollback:** delete the docs files.

**Files to create / edit.**
- Create: `docs/adr/0031-php-extensions-management.md`
- Edit:   `docs/BLUEPRINT.md` — add section 4.10.2 "PHP Extensions management" under M9; update section 11 changelog row.
- Create: `docs/runbooks/php-extensions.md` — troubleshooting guide (apt lock held, phpenmod says "no such module", ini symlink drift, forced resync via manual `phpenmod -v <v> -s fpm <mod>`).

**ADR-0031 skeleton.**
```
Status: Accepted
Date:   <commit date>
Context: How do admins manage PHP extensions on the server.
Decision:
  1. Server-wide per PHP version (not per-user / per-pool).
  2. State read live from dpkg + /etc/php/<v>/fpm/conf.d (no DB persistence).
  3. Allowlist of 63 extensions, Go source-of-truth at internal/phpext.
  4. phpenmod / phpdismod for enable/disable; apt for install/remove.
  5. Synchronous reload of php<v>-fpm + all matching jabali-fpm@<user>.
  6. Admin-only API; agent re-validates at the trust boundary.
Consequences:
  - No migration, no drift risk vs OS state.
  - apt latency propagates to HTTP (up to 5min).
  - Extension list is fixed at compile time; expanding the list requires a panel release.
  - Built-ins aren't removable — enforced in allowlist classification.
```

**Exit criteria.** Links to real files; ADR is dated; BLUEPRINT changelog row reflects the commit hash.

---

## 6. Plan mutation protocol

If during execution you discover a step is larger than scoped or has a hidden dependency:

- **Split.** Add `Step N.5` between two existing steps. Renumber nothing; just slot in.
- **Insert.** Same as split, with an explicit rationale line at the top of the new step.
- **Skip.** Strike through the step body with a `~~...~~` wrapper and add a `## Skipped` note with the reason and the commit/PR that made it unnecessary.
- **Abandon.** Mark the whole plan `Status: Abandoned` at the top and stop. Don't partially merge.

All mutations go through a plan-file commit on a feature branch (slug: `planmod-<reason>`).

---

## 7. Verification (whole-plan exit gate)

Before dispatcher merges the last branch to `main`:

1. `make fmt vet lint test` clean.
2. `make coverage-check` ≥ 80%.
3. Manual browser smoke: log in as admin → `/admin/php` → both tabs function; install a cheap ext (`bcmath`) on the default version, confirm `php -v -m | grep bcmath` on the box shows it loaded.
4. Confirm a non-admin user cannot see either API route (403 from both).
5. `gitnexus_detect_changes` on the merged branch returns only files inside `internal/phpext/`, `panel-api/internal/api/php_*`, `panel-agent/internal/commands/php_ext_*`, `panel-ui/src/shells/admin/php*/`, `panel-ui/tests/e2e/php-extensions.spec.ts`, `docs/adr/0031-*.md`, `docs/BLUEPRINT.md`, `docs/runbooks/php-extensions.md`.
6. ADR-0031 linked from BLUEPRINT.
7. No CLAUDE.md / MEMORY.md churn.

---

## 8. Out-of-band risks

- **apt lock contention.** If unattended-upgrades is running, apt will block. The 5-min agent timeout catches this; UX is a 5xx. Document in the runbook.
- **phpenmod silently fails on a version mismatch** (e.g., tool thinks you mean 8.5 but only 8.4 is installed). The pre-call existence check in Step 2 validation #2 guards this.
- **Shared apt packages (`php8.5-xml`) create dependency traps** in the remove path. Step 2's refusal rule (allowlist cross-check) handles this.
- **Distro drift.** Sury renames packages occasionally. The allowlist may go stale. Mitigation: Step 2's `list` subtest on a real box (run via `install.sh` CI) should catch it; plan a periodic manual audit.
- **FPM reload storms** when many jabali-fpm@<user>.service instances are running. Reload is cheap (SIGUSR2) — acceptable.

---

**Last updated:** 2026-04-19 (initial draft, pre-review)
