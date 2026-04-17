# ADR-0023: M9 — PHP/FPM pool manager (per-user, multi-version)

**Date:** 2026-04-17
**Status:** Accepted
**Deciders:** Shuki

## Context

Jabali installs nginx but has no PHP today. Hosting users cannot run WordPress,
Laravel, phpMyAdmin, or any other PHP application. The existing codebase
contains vestigial scaffolding that assumes PHP is available:

- `panel-agent/internal/commands/domain_create.go:74` has an nginx vhost
  template with `fastcgi_pass unix:/run/php/php{{.PHPVersion}}-fpm-{{.Username}}.sock;`
- `panel-api/internal/reconciler/reconciler.go` hard-codes the default `php_version` (bumped from `"8.3"` to `"8.5"` on 2026-04-17)
  with a TODO to make it configurable
- `panel-agent` already creates `/home/<username>` owned by
  `<username>:www-data` (mode 0750), ready for a pool to run there

This decision set locks the design for a per-user PHP-FPM pool manager that
supports multiple installed PHP versions. It unblocks M7 Tranche E (phpMyAdmin
SSO, currently parked), M10 (WordPress), M11 (FileBrowser), and every future
per-user PHP application.

## Decision

We are building a **per-user PHP-FPM pool manager** with the following 13
design decisions. Each panel user gets exactly one pool (MVP constraint);
that pool can be bound to multiple domains, each domain can select the PHP
version independently, and admins can customize pool settings via an
allowlisted ini-override mechanism. The Sury package repository provides
multiple installed PHP versions; default version is 8.5 (supported range 7.4–8.5).

---

## Design Decisions

### 1. Multi-version via Sury

**Decision:** `install.sh` adds `packages.sury.org/php` (Sury's third-party
Debian/Ubuntu repository), installs multiple PHP versions via their official
signed packages, and accepts a configuration variable `--php-versions` to
customize which versions are installed (default: `8.5`). The panel
bundles a version matrix constant that enumerates all available versions.

#### Alternatives considered

- **Compile PHP from source.** More control, but adds build dependencies,
  vastly longer install time, and no security updates from upstream. No
  mechanism to test against multiple versions. Rejected.
- **Debian/Ubuntu's default repositories.** Only offer one PHP version per
  distro release; does not support version selection. Rejected.
- **PPA (Ondrej/PPA).** De-facto standard but less formally maintained than
  Sury. Sury is the current standard used by hosting providers. Accepted.

#### Consequences

- Multi-version support is first-class, not a one-off hack.
- Administrators can offer users a choice of PHP versions at domain bind time.
- Supply chain depends on Sury's GPG key; fetch is automated but fingerprint is
  vendored and documented to resist rot.
- `install.sh` must fetch and validate the Sury GPG key before adding the
  source, requiring network access and pin documentation.

---

### 2. Default version is 8.5 (supported range: 7.4–8.5)

**Decision:** When no explicit version is specified, the system defaults to
PHP 8.5 — the current stable release. The supported range is 7.4 through 8.5
(via Sury). `install.sh` installs 8.5 by default; admins install additional
versions side-by-side with `JABALI_PHP_VERSIONS="7.4 8.2 8.5" bash install.sh`.

**Amended 2026-04-17:** bumped from 8.3 to 8.5; supported range explicitly
documented as 7.4–8.5. Rationale: keep the default tracking the latest
stable; preserve ability to install legacy versions (7.4) for sites that
haven't migrated yet.

#### Alternatives considered

- **Default to latest (current 8.5).** Chosen. Tracks upstream stable releases;
  means new installs get modern PHP without admin intervention.
- **Default to an older LTS-adjacent version (8.2 or 8.3).** Conservative but
  leaves new installs on older releases; admins who want current PHP have to
  remember to set `JABALI_PHP_VERSIONS`. Rejected.
- **No default — force admin to specify.** Wrong default-experience trade-off
  for a control panel; fresh installs should be usable out of the box. Rejected.

#### Consequences

- Fresh installs get PHP 8.5 with no extra configuration.
- Upgrading an existing install via `jabali-panel update` does **not**
  automatically change a user's pool version — existing pools keep their
  recorded `php_version`. Admin can edit a pool to bump it.
- Bumping the default in a future release (e.g. to 8.6) requires editing
  two places: `install.sh` (`php_versions=` default) and
  `reconciler.go`'s `PHPVersion:` literal in the "Create default pool if
  missing" block. Keep these in sync.
- Versions outside 7.4–8.5 are not tested; Sury's availability of
  other versions is out of scope.

---

### 3. One pool per panel user (MVP constraint)

**Decision:** Each panel user gets **exactly one** PHP-FPM pool, shared across
all domains owned by that user. The pool's name encodes the username; socket
path encodes both version and username. Multiple domains can bind to the same
pool, but different PHP versions require an administrative request to create
a new pool (future feature).

#### Alternatives considered

- **Per-domain pools.** More isolation between applications, but explodes FPM
  worker count on shared-host systems (20 users × 5 domains each = 100 pools =
  2,000 workers at default settings). Impractical. Deferred to M10+.
- **One global PHP pool running as www-data.** Simplifies configuration but
  violates the principle of per-user resource isolation; one user's runaway
  script kills performance for all. Rejected.

#### Consequences

- Pool lifecycle is tightly coupled to user lifetime.
- Scaling to per-domain pools requires a data migration (split pool, assign
  domains, rebind) and is documented as a future capability.
- Users who need multiple PHP versions must contact an administrator.

---

### 4. Pool name + socket path encoding

**Decision:** Pool name is `jabali-<username>`. Socket path is
`/run/php/php<version>-fpm-<username>.sock`. The version is embedded in the
socket path, not a parameter, so changing versions triggers a pool restart
and socket change — nginx regeneration happens atomically during convergence.

#### Alternatives considered

- **Socket path ignores version; pool name includes version.**
  `/run/php/fpm-<username>.sock` reused across versions via in-place config
  rewrite. Introduces coupling between version switch and pool config edits;
  risk of transient socket-permission mismatches during restart. Rejected.
- **Centralized socket directory; version chosen at runtime via environment.**
  Requires dynamic socket name lookup at request time. Adds complexity to
  nginx config templating. Rejected.

#### Consequences

- Socket path is deterministic from (username, version); nginx config can
  render it at template-time without a lookup.
- Switching a domain to a different PHP version is a two-step process:
  (1) pool status changes to "switching", (2) old socket is closed, new
  socket is opened, nginx is regenerated. No in-place socket swap.
- System can cleanly detect stale sockets from old pools and remove them.

---

### 5. Pool runs as the panel user

**Decision:** The FPM pool configuration runs the worker processes as
`<username>:www-data` (user/group set in the pool `.conf` file). The socket
is owned `<username>:www-data` with mode 0660, allowing nginx (running as
www-data) to connect via group membership.

#### Alternatives considered

- **Pool runs as www-data.** Simplifies user/group management but breaks
  isolation: one user's poorly-configured app can read another's files (e.g.,
  `.htaccess` or included config files with secrets). Rejected.
- **Pool runs as root, drops to www-data.** Adds complexity and security risk;
  gains nothing over running as the user directly. Rejected.

#### Consequences

- Each user's PHP scripts execute with their own Linux UID, enforcing kernel
  file-permission boundaries.
- The panel agent must ensure the Linux user account exists before pools are
  created (already done in `user.create`).
- Nginx socket ACL is group-based; www-data must have the user's primary group
  or be in a supplementary group. Implemented via
  `listen.owner` and `listen.group` in the pool config.

---

### 6. Schema shape: three migrations

**Decision:** Three new tables: `php_pools` (pool definition), `php_pool_ini_overrides`
(per-pool ini setting overrides), and an addition to `domains` table (`php_pool_id` FK).

- `php_pools`: one row per user (MVP), references `users.id`, tracks version,
  PM settings, status, and last error.
- `php_pool_ini_overrides`: rows added on demand, stores directive name/value
  pairs; unique constraint on (pool_id, directive).
- `domains.php_pool_id`: nullable FK to `php_pools.id`, `ON DELETE SET NULL`
  so a deleted pool leaves domains static.

#### Alternatives considered

- **Flatten ini overrides into json_extract array in `php_pools` table.** Saves
  one table; makes it harder to enforce the allowlist (no CHECK constraint on
  JSON keys) and harder to query "which pools have upload_max_filesize > X".
  Rejected.
- **Store version in `domains` table directly.** Avoids the FK, but duplicates
  the version in every domain row if they share a pool. Rejected; pool is the
  source of truth.

#### Consequences

- Queries for "pools with status='error'" or "all overrides for a pool" are
  simple, direct SQL.
- `php_pools` must have a unique constraint on `user_id` to enforce MVP.
- `ON DELETE SET NULL` on the FK means deleting a pool is never catastrophic;
  domains revert to static, and can be re-bound later if needed.

---

### 7. Default process manager: ondemand, max_children=20, idle_timeout=60s

**Decision:** Every pool starts with `pm = ondemand`, `pm.max_children = 20`,
`pm.process_idle_timeout = 60` (in seconds). These defaults are
admin-overridable via the API at any time. **Users have no direct control**
over PM settings; requests to tune these go through admin approval.

#### Alternatives considered

- **pm = dynamic with fixed min/max.** More predictable resource usage, but
  requires careful tuning per host; sensible defaults vary by RAM. `ondemand`
  is safer for shared hosts. Rejected.
- **No PM settings API; only config-file edits.** Reduces API surface, but
  requires downtime or service restart. Admin knobs are useful for production
  troubleshooting. Rejected.

#### Consequences

- Pool startup is fast (no pre-forked workers); latency on cold domain hit is
  acceptable (~100ms extra).
- Memory is efficient: idle processes are killed, not held open.
- Admin can tune via the API if a user reports slowness or resource issues.
- Default values are conservative; the runbook (M9 step 9) documents tuning
  strategies.

---

### 8. Ini overrides — allowlist only

**Decision:** Users can override specific PHP ini directives via the API, but
only those explicitly in an allowlist. Accepted directives are:
`memory_limit`, `upload_max_filesize`, `post_max_size`, `max_execution_time`,
`max_input_vars`, `max_input_time`, `date.timezone`, `display_errors`,
`log_errors`, `file_uploads` (bools rendered as `php_admin_flag`, others as
`php_admin_value`). Any request to override a directive outside the allowlist
is rejected at the API boundary with a clear error.

#### Alternatives considered

- **All directives allowed.** Allows users to set dangerous values (e.g.,
  `disable_functions` to null, `open_basedir` empty, `expose_php=on`).
  Rejected; allowlist is the minimal-trust model.
- **Hard-code all settings in pool template, no overrides.** Reduces API
  surface but makes it impossible for admins to adjust for specific user needs.
  Rejected; some tuning is necessary.

#### Consequences

- The allowlist must be documented and code-reviewed; new directives require an
  ADR or RFC.
- Overrides are stored one-row-per-directive in `php_pool_ini_overrides`,
  making it easy to track and audit changes.
- Unsupported override requests are explicit and visible in logs, enabling
  future prioritization of common requests.

---

### 9. Reconciler owns convergence

**Decision:** The API handler writes pool state to the database. The
reconciler polls the `php_pools` table and calls agent commands
(`php.pool.apply`, `php.pool.remove`) to apply the configuration on the
host. Agent commands are idempotent. Status field on `php_pools` (`pending`,
`active`, `error`) tracks convergence. This matches ADR-0004 and the existing
DNS/SSL reconciliation pattern.

#### Alternatives considered

- **API handler directly invokes agent.** Tighter coupling, no audit trail in
  DB if the agent call fails. The reconciler pattern (db-first, agent-second)
  is proven in this codebase. Rejected.
- **Agent-driven: agent polls the API for desired state.** Requires long-polling
  or a push mechanism; adds complexity for no benefit. Rejected.

#### Consequences

- Pool configuration is visible in the DB before it's applied; admins can audit
  changes.
- If the agent command fails, the status field captures the error; next
  reconciler tick retries.
- Reconciler crash or temporary agent unavailability do not lose the desired
  state; it survives in the DB.

---

### 10. Domain binding semantics: NULL = static; 409 on pool delete while bound

**Decision:** A domain's `php_pool_id` can be NULL (meaning the domain serves
static content, no PHP block in the nginx vhost) or a FK to an active pool
(meaning nginx fastcgi_pass to that pool's socket). When a user requests to
delete a pool via the API, the deletion is refused (HTTP 409 Conflict) if any
domain still references it. Explicit unbind is required first. The `ON DELETE
SET NULL` FK constraint is a safety net, not the primary mechanism.

#### Alternatives considered

- **Cascade delete domains when pool is deleted.** Catastrophic; deleting a
  pool would silently delete the user's domains. Rejected.
- **Auto-unbind on pool delete, no 409.** Convenient but silent; user might
  not notice their domain reverted to static. Explicit unbind is clearer.
  Rejected.

#### Consequences

- Admin must explicitly unbind all domains before deleting a pool.
- If a domain's `php_pool_id` is set but the pool is deleted (e.g., due to DB
  corruption), the FK constraint sets it to NULL on the next reconcile, leaving
  the domain static and safe.
- Nginx vhost template must check for NULL and omit the PHP location block if
  `php_pool_id` is NULL.

---

### 11. phpMyAdmin SSO integration point

**Decision:** The parked M7 Tranche E plan (`plans/phpmyadmin-sso.md`) assumes
a dedicated `jabali-pma` FPM pool. When SSO is resumed (after M9 ships), step
6 of that plan will be rewritten to use M9's pool manager instead: a regular
panel user `phpMyAdmin` (or similar) will own the phpMyAdmin pool via M9's
standard pool lifecycle. This decision records the explicit integration point
but defers the rewrite to the SSO plan's own steps.

#### Alternatives considered

- **Build phpMyAdmin integration into M9 itself.** Couples the concerns; M9
  should be a general pool manager, and phpMyAdmin is one application that
  uses it. Rejected.
- **Keep the separate jabali-pma pool forever.** Works, but duplicates pool
  management logic and leaves M9 incomplete. Rejected.

#### Consequences

- M9 does not directly provision or manage a phpMyAdmin pool.
- M7 Tranche E remains parked until M9 ships; then it adds one `phpMyAdmin`
  user and one pool binding.
- The general pool manager is not burdened with phpMyAdmin-specific logic.

---

### 12. Jailbreak defense: hard-coded open_basedir

**Decision:** The pool configuration template hard-codes
`php_admin_value[open_basedir] = /home/<username>:/tmp:/var/tmp` and renders
it into every pool's `.conf` file at install time. This directive is
**explicitly excluded** from the ini-override allowlist (decision 8), so users
cannot loosen it via the API.

**Threat model:** PHP's `security.limit_extensions` prevents execution of files
without a `.php` extension. A determined attacker can symlink
`shell.jpg → /etc/passwd` inside their docroot, then request
`shell.jpg?exec` in a vulnerable application, tricking the extension check
into loading `/etc/passwd` as PHP. The `open_basedir` restriction closes this
bypass by forbidding PHP to open anything outside the allowed paths, regardless
of filename. Without it, even with `limit_extensions`, the symlink attack
succeeds.

#### Alternatives considered

- **Allow users to set open_basedir.** Defeats the jailbreak defense; a user
  setting `open_basedir = /` makes the symlink attack work again. Rejected.
- **Rely on limit_extensions alone.** Sufficient against naive attacks, but the
  symlink bypass (and other creative attacks) show that extension-based limits
  are insufficient. Rejected.
- **Restrict open_basedir to only the docroot (no /tmp).** Breaks applications
  that legitimately need `/tmp` for uploads or temp files. Rejected.

#### Consequences

- Applications that require write access to `/tmp` or `/var/tmp` work as expected.
- Applications cannot read arbitrary files; symlinks to system files fail with
  "Permission denied" at the open() layer.
- The defense is non-negotiable and cannot be tuned by users; admins who want
  to override it must rebuild the pool config manually (rare and auditabled).

---

### 13. Username immutability and future rename checklist

**Decision:** Jabali does not currently support renaming a panel user. The M9
pool design (socket path encodes username, pool name includes username,
database FK to user) compounds the cost of adding user rename later. This ADR
records username as immutable in the MVP scope.

If user rename is implemented in a future release, the checklist is:

1. Verify the user has no domains (or unbind all domains first).
2. Call `php.pool.remove` via the reconciler (pool status → `stopping`).
3. Rename the Linux account via `usermod -l <new_name> <old_name>`.
4. Move the home directory: `mv /home/<old> /home/<new>`.
5. Call `php.pool.apply` to re-provision under the new name.
6. Re-bind any domains that were unbound in step 1.

#### Alternatives considered

- **Add a `previous_username` column and alias logic.** Helps with migrations
  but does not eliminate the manual steps; sockets still encode the current
  username. Rejected for MVP.
- **Rename the user without touching the pool.** Pool name and socket path
  become stale; nginx config references a non-existent socket. Rejected.

#### Consequences

- User rename is not a self-service operation in MVP; it requires an admin.
- If rename is implemented later, the above checklist ensures the pool
  infrastructure stays in sync.
- The socket path's encoding of the username is acceptable because renames are
  rare and well-documented.

---

## Consequences (combined)

### Positive

- PHP is a first-class Jabali capability, unblocking WordPress, Laravel,
  phpMyAdmin, and future PHP applications.
- Per-user pools provide resource isolation; one user's runaway script does not
  kill others' performance.
- Multi-version support enables users to choose the PHP version that best suits
  their application, as versions age and EOL.
- Reconciler-driven convergence matches the existing DNS/SSL pattern; admins
  understand the flow.
- Socket paths encode version and username, making nginx config generation
  deterministic and stateless.
- Allowlisted ini overrides give users knobs for tuning (memory, timeouts) while
  protecting critical settings (open_basedir, disable_functions).
- Hard-coded `open_basedir` closes the symlink jailbreak; cannot be disabled
  without rebuilding the pool.
- Schema is simple: one new table per entity, clear FKs, no denormalization.

### Negative

- One pool per user is a constraint; users who need multiple PHP versions per
  domain must request an admin override (future feature).
- Username is immutable in MVP; rename support requires manual steps
  documented in a future ADR.
- Multiple PHP versions increase disk footprint and security-update burden
  (`apt upgrade` must patch all installed versions).
- Socket path encoding of username means socket cleanup on user deletion must
  be explicit (agent command, not just FK cascade).

### Risks

- **Sury supply chain.** Mitigated by vendored GPG key fingerprint with
  documented source. Fingerprints will rot if not maintained; runbook must
  document the refresh procedure.
- **open_basedir bypasses via new PHP features.** Mitigated by reviewing PHP
  release notes for new functions that can escape open_basedir; if found, must
  be added to `disable_functions` (future allowlist).
- **Domain stays bound to deleted pool due to FK drift.** Mitigated by `ON
  DELETE SET NULL` and explicit 409 on pool delete while bound.
- **Admin forgets to disable default www pool in install.sh.** Leads to
  conflicts if nginx tries to proxy to it. Mitigated by explicit rename to
  `www.conf.disabled` and test verification in install.sh.

---

## Related Decisions

- ADR-0002: Database is the source of truth
- ADR-0004: Reconciler-driven convergence
- ADR-0009: Nginx file-per-vhost
- ADR-0020: phpMyAdmin SSO via signon proxy (partially supersedes; resumes after M9)
- ADR-0022: phpMyAdmin SSO shadow account (depends on M9 for pool infrastructure)

## References

- `plans/m9-php-fpm-pool-manager.md` — Implementation plan with step-by-step tasks
- `plans/phpmyadmin-sso.md` — M7 Tranche E, parked pending M9
- Sury repository: https://packages.sury.org/php
- PHP-FPM process managers: https://www.php.net/manual/en/install.fpm.configuration.php (pm, pm.strategy)
- open_basedir jailbreak: CWE-427, symlink bypass via security.limit_extensions
