# Plan: M9 — PHP / FPM pool manager (multi-version)

**Objective.** Make PHP a first-class Jabali capability. Each panel user gets
a PHP-FPM pool running as their own Linux account; each domain can bind to a
pool (or stay static); admins can choose among multiple installed PHP
versions. This unblocks M7 Tranche E (phpMyAdmin SSO, currently parked),
M10 (WordPress), M11 (FileBrowser), and every future per-user PHP app.

**Mode.** Direct (no `gh` CLI, Gitea remote). One commit per step on `main`;
no branches, no PRs.

**Sequencing.** Steps 1–3 strictly sequential. Steps 4/5/6 are file-disjoint
and may parallelize once 3 lands, but do not parallelize from the shared
main worktree — prior parallel runs caused real bugs. Sequential is
recommended; if parallel is desired, use `isolation: "worktree"` per agent.

**Invariants.** After every step:
- `cd panel-api && go test ./... -race` passes (add `-count=1` if tests cache).
- `cd panel-agent && go test ./... -race` passes.
- `cd panel-ui && npx tsc -b && npx vite build` passes.
- `bash -n install.sh` clean.
- No file in the diff exceeds 800 lines; no function exceeds 50 lines.
- No PHP version is hard-coded in Go code — `"8.3"` in `reconciler.go:304`
  moves to a per-domain DB column as part of step 6.

---

## Existing scaffolding this plan lights up

These already exist and assume PHP is installed — they're the reason M9 is
"light up the stub," not "design from scratch":

- `panel-agent/internal/commands/domain_create.go:74` — nginx vhost template
  already has `fastcgi_pass unix:/run/php/php{{.PHPVersion}}-fpm-{{.Username}}.sock;`
- `panel-agent/internal/commands/user.create` — already creates
  `/home/<username>` owned by `<username>:www-data`, mode 0750. The pool
  runs as `<username>:www-data`; nginx (www-data) can reach the socket.
- `panel-api/internal/reconciler/reconciler.go:304` — hard-codes
  `"php_version": "8.3"` with a `TODO: make configurable`. Step 6 removes
  the TODO.

## Design decisions (committed in step 1, ADR-0023)

The plan assumes these. Step 1 writes them into an ADR with alternatives
considered.

1. **Multi-version via Sury.** `install.sh` adds `packages.sury.org/php`,
   installs PHP 8.2 and 8.3 by default, accepts `--php-versions=7.4,8.0,...`
   to install more. Jabali bundles a version matrix constant.
2. **Default version is 8.3.** Matches the existing reconciler hard-code;
   lowers migration risk for existing domains.
3. **Pool granularity: one pool per panel user.** Not per-domain. Per-domain
   pools explode FPM worker count on shared-host boxes. A user who needs
   two PHP versions opens an admin request (handled outside MVP).
4. **Pool name + socket path.** Pool name: `jabali-<username>`. Socket:
   `/run/php/php<version>-fpm-<username>.sock`. Version is encoded in the
   path so switching versions is an atomic "old pool stops, new pool
   starts, nginx regens" — no in-place rewrite.
5. **Pool runs as the panel user.** `<username>:www-data`. `user` +
   `group` + `listen.owner` + `listen.group` set in the pool config.
   Nginx (www-data) has connect perms on the socket via group.
6. **Schema.** Three migrations: `php_pools`, `php_pool_ini_overrides`,
   `domains.php_pool_id` (nullable FK). NULL = static site (no PHP block
   in the vhost).
7. **Default PM.** `ondemand`, `pm.max_children = 20`,
   `pm.process_idle_timeout = 60s`. Admin-overridable at any time. User
   has no control over PM settings.
8. **Ini overrides — allowlist only.** Accepted directives:
   `memory_limit`, `upload_max_filesize`, `post_max_size`,
   `max_execution_time`, `max_input_vars`, `max_input_time`,
   `date.timezone`, `display_errors` (bool), `log_errors` (bool),
   `file_uploads` (bool). Any other directive is rejected at the API
   boundary. Stored one-row-per-override; rendered as
   `php_admin_value[<name>] = <value>` (or `php_admin_flag` for bools).
9. **Reconciler owns convergence.** The handler writes the DB; the
   reconciler calls agent commands to apply. Agent commands
   (`php.pool.apply`, `php.pool.remove`) are idempotent. This matches
   ADR-0004 and the existing DNS/SSL convergence pattern.
10. **Domain binding.** `POST /api/v1/domains/:id/php-pool {pool_id}`
    sets `domains.php_pool_id` and schedules an nginx regen. Nginx
    template reads `php_pool_id`; if NULL, the PHP location block is
    omitted entirely. **Pool deletion is refused (HTTP 409)** while
    any domain still references the pool — belt-and-suspenders
    alongside `ON DELETE SET NULL` on the FK, so a "leave domains
    static" semantic requires an explicit unbind first.
11. **phpMyAdmin will piggyback.** Decision #1 of the parked SSO plan
    (`plans/phpmyadmin-sso.md` step 6 and following) gets rewritten to
    use M9's pool manager instead of its own `jabali-pma` pool. That
    rewrite is out of scope for this plan — noted here as the
    explicit integration point when SSO is resumed.
12. **Jailbreak defense.** The pool config template hard-codes
    `open_basedir = /home/<username>:/tmp:/var/tmp` at install-time.
    `open_basedir` is explicitly **not** in the ini-override allowlist
    (decision 8), so users cannot loosen it through the API. This
    closes the `security.limit_extensions` symlink bypass: even if a
    user symlinks `shell.jpg → /etc/passwd`, PHP refuses to open
    anything outside `/home/<user>`, `/tmp`, `/var/tmp`.
13. **Username immutability.** Jabali does not currently support
    renaming a panel user, and M9's schema + pool lifecycle
    (socket path encodes username) compounds the cost of adding that
    feature later. Any future user-rename work must (a) ensure the
    user has no bound domains, (b) tear down the pool first via
    `php.pool.remove`, (c) rename the Linux account, (d) re-provision
    the pool under the new name, (e) rebind domains. ADR-0023 records
    this as a breaking constraint; the schema does not grow a
    "previous username" column in MVP.

---

## Step 1 — ADR-0023 (PHP/FPM pool manager design)

**Parallel:** no (blocks 2, 3).
**Model tier:** strongest.
**Agent:** `adr-architect`.
**Est. complexity:** LOW.

### Context brief

Jabali installs nginx but has no PHP today. Hosting users cannot run
WordPress, Laravel, or phpMyAdmin. The blueprint (M9) calls for a
per-user pool manager. A prior design iteration for M7 Tranche E
(phpMyAdmin SSO) proposed a single throwaway `jabali-pma` pool; we've
parked that in favor of building pools as real infrastructure first.
ADR-0023 locks the 11 decisions listed above so step 2+ implementers
don't rehash scope questions.

### Tasks

1. Read the existing ADRs in `docs/adr/` end-to-end. Pay particular
   attention to ADR-0002 (DB is truth), ADR-0004 (reconciler-driven
   convergence), ADR-0009 (nginx file-per-vhost).
2. Create `docs/adr/0023-m9-php-fpm-pool-manager.md`. Status: Accepted.
   Each of the **13** design decisions above becomes its own subsection
   with: the decision, alternatives considered (min 2), and
   consequences. The two "post-review" decisions (12 on open_basedir
   default, 13 on username immutability) must explicitly document
   the threat model or future-constraint they encode.
3. Update `docs/adr/README.md` to index ADR-0023.
4. Add one short banner at the top of ADR-0022 (phpMyAdmin SSO): "Parked
   2026-04-17. Depends on M9 (ADR-0023) for pool infrastructure."
5. Update `docs/BLUEPRINT.md` changelog:
   - Mark M9 as **In flight**.
   - Mark M7 Tranche E (phpMyAdmin SSO) as **Parked — resumes after M9**.

### Verification

- `test -f docs/adr/0023-m9-php-fpm-pool-manager.md`
- `grep -q "0023" docs/adr/README.md`
- `grep -q "Parked" docs/adr/0022-m7-phpmyadmin-sso-shadow-account-and-uds.md`
- `grep -q "Parked" docs/BLUEPRINT.md`

### Exit criteria

- ADR-0023 exists with all 11 decisions + alternatives.
- Index + SSO banner + blueprint changelog updated.
- Commit: `docs(adr): ADR-0023 M9 PHP/FPM pool manager design`.

---

## Step 2 — install.sh: Sury repo + multi-version PHP install

**Parallel:** no (step 7 reconciler invokes fpm services created here).
**Model tier:** default.
**Agent:** `backend-dev`.
**Est. complexity:** MEDIUM.

### Context brief

`install.sh` is Jabali's single install path. Convention: each external
package gets its own `install_<thing>()` function called from `main()`.
See `install_powerdns` and `install_node` for the patterns
(apt sources fragment + package install + systemd check +
idempotency guard). Sury (`packages.sury.org/php`) is the de-facto
standard multi-version PHP repo for Debian/Ubuntu; signed by
`DPA CA Certificate`, key at
`https://packages.sury.org/php/apt.gpg`.

### Tasks

1. Add `install_php()` to `install.sh`:
   - Accept via env or flag `JABALI_PHP_VERSIONS` (default `"8.2 8.3"`).
   - Install the Sury apt source as
     `/etc/apt/sources.list.d/sury-php.list`, key at
     `/usr/share/keyrings/sury-php.gpg`. Fetch the gpg key over HTTPS
     and validate its fingerprint against a vendored constant at the
     top of the function. **Document the fingerprint source in a
     comment block above the constant** (at minimum: the URL and the
     date the fingerprint was last verified against
     `packages.sury.org/php`). A silent constant with no provenance
     is how supply-chain pins rot.
   - Manual compat check: the verification section of this step
     requires confirming `apt-cache policy php8.2-fpm php8.3-fpm`
     shows the Sury repo as the candidate source on the target
     Debian/Ubuntu version before marking the step done.
   - `apt-get update`.
   - For each version in the list, install:
     `php<v>-fpm php<v>-cli php<v>-mysql php<v>-mbstring php<v>-zip
     php<v>-gd php<v>-curl php<v>-xml php<v>-intl php<v>-bcmath
     php<v>-opcache`. (This is a sensible baseline for WordPress,
     Laravel, and phpMyAdmin.)
   - Disable each `php<v>-fpm` systemd service's default pool
     (`/etc/php/<v>/fpm/pool.d/www.conf`) — rename to `www.conf.disabled`.
     Jabali pools are the only pools that should exist; the default
     `www` pool listens on a TCP port and runs as `www-data`, neither
     of which we want.
   - Ensure each `php<v>-fpm.service` is enabled + running (there will
     be one systemd unit per installed version).
   - Idempotency: re-running `install_php` must be safe. Detect existing
     sources list and apt repo availability; reinstall packages only if
     versions are missing.
2. Call `install_php` from `main()` after `install_nginx` and before
   `write_config_file`.
3. Write a tiny helper script
   `install/php/jabali-php-pool.conf.tmpl` (new directory) — this is
   the pool-config template that the agent will use in step 4. Fields:

   ```
   [{{.PoolName}}]
   user = {{.User}}
   group = {{.Group}}
   listen = {{.SocketPath}}
   listen.owner = {{.User}}
   listen.group = www-data
   listen.mode = 0660
   pm = {{.PmMode}}
   pm.max_children = {{.PmMaxChildren}}
   pm.process_idle_timeout = {{.ProcessIdleTimeout}}
   chdir = /home/{{.User}}
   security.limit_extensions = .php
   ; Jailbreak defense (decision 12 in ADR-0023): PHP can only
   ; open files inside the user's home, tmp, and /var/tmp. Users
   ; cannot override via the ini API — open_basedir is not in the
   ; allowlist. Without this, security.limit_extensions is bypassable
   ; via symlinks from the docroot to arbitrary .php targets.
   php_admin_value[open_basedir] = /home/{{.User}}:/tmp:/var/tmp
   ; Opcache defaults. Conservative 32MB SHM per pool keeps total
   ; memory bounded on shared-host boxes (20 users × 32MB = 640MB).
   ; Runbook (step 9) documents tuning.
   php_admin_value[opcache.memory_consumption] = 32
   {{- range .AdminValues }}
   php_admin_value[{{.Name}}] = {{.Value}}
   {{- end }}
   {{- range .AdminFlags }}
   php_admin_flag[{{.Name}}] = {{.Value}}
   {{- end }}
   ```

   `install.sh` copies this file to
   `/etc/jabali-panel/php-pool.conf.tmpl` on install so the agent can
   read it at runtime.

### Verification

- Fresh VM: `bash install.sh` completes without error; `systemctl is-active
  php8.2-fpm php8.3-fpm` both return `active`; no `www` pool exists in
  `/etc/php/*/fpm/pool.d/`.
- `bash -n install.sh` clean.
- Re-running `bash install.sh` is idempotent (no duplicate sources list,
  no package reinstall, no service restart storm).

### Exit criteria

- Sury sources + gpg key installed, versioned and verifiable.
- Default 8.2 + 8.3 installed, extras gated behind the version list
  variable.
- Default `www` pool disabled across all installed versions.
- Pool template at `/etc/jabali-panel/php-pool.conf.tmpl`.
- Commit: `feat(install): Sury repo + multi-version PHP/FPM`.

---

## Step 3 — Schema + models + repositories

**Parallel:** no (blocks 4, 5, 6).
**Model tier:** default.
**Agent:** `tdd-guide` → `backend-dev`.
**Est. complexity:** MEDIUM.

### Context brief

Latest migration is `000027_create_phpmyadmin_sso_tokens` (M7 Tranche E
foundation, now parked but still in the ledger). New migrations are
numbered sequentially: 000028, 000029, 000030. Models under
`panel-api/internal/models/`, repositories under `panel-api/internal/repository/`,
patterns are in `database_user_grant_repository.go`.

### Tasks

1. Create three migrations, each with up + down:

   - `000028_create_php_pools.up.sql`:
     ```sql
     CREATE TABLE php_pools (
       id CHAR(26) NOT NULL PRIMARY KEY,
       user_id CHAR(26) NOT NULL,
       php_version VARCHAR(8) NOT NULL,          -- "8.2", "8.3", etc.
       pm_mode VARCHAR(16) NOT NULL DEFAULT 'ondemand',
       pm_max_children INT UNSIGNED NOT NULL DEFAULT 20,
       process_idle_timeout_seconds INT UNSIGNED NOT NULL DEFAULT 60,
       status VARCHAR(16) NOT NULL DEFAULT 'pending',  -- pending | active | error
       last_error TEXT NULL,
       created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
       updated_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6),
       UNIQUE KEY uniq_user_pool (user_id),
       FOREIGN KEY fk_pool_user (user_id) REFERENCES users(id) ON DELETE CASCADE
     ) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
     ```
     `uniq_user_pool` enforces MVP's "one pool per user" decision.

   - `000029_create_php_pool_ini_overrides.up.sql`:
     ```sql
     CREATE TABLE php_pool_ini_overrides (
       id CHAR(26) NOT NULL PRIMARY KEY,
       pool_id CHAR(26) NOT NULL,
       directive VARCHAR(64) NOT NULL,
       value VARCHAR(255) NOT NULL,
       kind ENUM('value','flag') NOT NULL DEFAULT 'value',
       created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
       updated_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6),
       UNIQUE KEY uniq_pool_directive (pool_id, directive),
       FOREIGN KEY fk_override_pool (pool_id) REFERENCES php_pools(id) ON DELETE CASCADE
     ) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
     ```

   - `000030_add_php_pool_id_to_domains.up.sql`:
     ```sql
     ALTER TABLE domains
       ADD COLUMN php_pool_id CHAR(26) NULL,
       ADD CONSTRAINT fk_domain_php_pool FOREIGN KEY (php_pool_id) REFERENCES php_pools(id) ON DELETE SET NULL;
     ```
     `ON DELETE SET NULL` means deleting a pool leaves domains static
     (safer than cascading delete of domains).

2. Create models:
   - `panel-api/internal/models/php_pool.go`
   - `panel-api/internal/models/php_pool_ini_override.go`
   - Extend `panel-api/internal/models/domain.go` with `PHPPoolID *string`
     field.

3. Create repositories:
   - `panel-api/internal/repository/php_pool_repository.go`
     - `Create(ctx, pool)`, `FindByID(ctx, id)`, `FindByUserID(ctx, userID)`,
       `ListAll(ctx, opts)`, `Update(ctx, pool)`, `Delete(ctx, id)`,
       `SetStatus(ctx, id, status, lastErr)`.
   - `panel-api/internal/repository/php_pool_ini_override_repository.go`
     - `Create`, `ListByPool`, `Delete`.
   - Tests for each, go-sqlmock + table-driven, following
     `database_user_grant_repository_test.go` style.

4. Wire the three repositories into `panel-api/internal/app/app.go`
   `Deps` struct and into `panel-api/cmd/server/serve.go` construction.

### Verification

```bash
cd /home/shuki/projects/jabali2/panel-api
go build ./...
go test -race ./internal/repository/... -run "PHPPool|PhpPool"
go vet ./...
```

### Exit criteria

- Three migrations apply up + down cleanly on a fresh DB.
- Repositories pass all unit tests.
- Commit: `feat(php): migrations 028/029/030 + php_pools repos`.

---

## Step 4 — Agent commands: php.pool.apply, php.pool.remove, php.version.list

**Parallel:** yes, with steps 5 and 6 after step 3.
**Model tier:** default.
**Agent:** `tdd-guide` → `backend-dev`.
**Est. complexity:** MEDIUM.

### Context brief

Agent commands live in `panel-agent/internal/commands/` and register in
`registry.go`. The canonical pattern is validation-only unit tests
(see `db_user_create_test.go`, `db_mysqladmin_ensure_test.go`). Input is
a JSON blob; output is structured JSON. Identifier escaping is via
`EscapeMariaDBIdentifier`/`EscapeMariaDBLiteral` — but PHP pools use
filesystem paths, not SQL, so a different escape regime applies.

The pool config template was installed in step 2 at
`/etc/jabali-panel/php-pool.conf.tmpl`. The agent renders it with
Go's `text/template`.

### Tasks

1. `panel-agent/internal/commands/php_pool_apply.go`:
   - Params: `{ "username", "php_version", "pm_mode", "pm_max_children",
     "process_idle_timeout", "admin_values": [{"name","value"},...],
     "admin_flags": [{"name","value"},...] }`.
   - Validate: `username` matches `^[a-z][a-z0-9_]{0,31}$`;
     `php_version` matches `^\d+\.\d+$` and directory
     `/etc/php/<version>/fpm/pool.d/` exists; `pm_mode` in
     `{static, ondemand, dynamic}`; `pm_max_children > 0`;
     each `admin_values.name` matches a strict allowlist (same allowlist
     as the API validates, duplicated defense-in-depth);
     `admin_flags.value` in `{on, off}`.
   - Before writing: remove **all** stale pool files for this username
     across every installed PHP version (glob
     `/etc/php/*/fpm/pool.d/jabali-<username>.conf`) — guarantees the
     only pool for this user after apply is the one we're writing.
   - Render `php-pool.conf.tmpl` to
     `/etc/php/<version>/fpm/pool.d/jabali-<username>.conf`, mode 0644,
     owner root.
   - Reload **only** the affected `php<version>-fpm` service via
     `systemctl reload php<version>-fpm`. If any pool files for the
     previous version existed, also reload that version's fpm service.
   - Return `{ "socket_path": "/run/php/php<v>-fpm-<username>.sock",
     "pool_name": "jabali-<username>" }`.

2. `panel-agent/internal/commands/php_pool_remove.go`:
   - Params: `{ "username" }`.
   - Glob delete all `/etc/php/*/fpm/pool.d/jabali-<username>.conf`.
   - Reload every affected fpm service.
   - Return `{ "removed": N }`.

3. `panel-agent/internal/commands/php_version_list.go`:
   - No params.
   - Return `{ "versions": ["8.2", "8.3", ...] }` — derived from
     listing `/etc/php/*/fpm/pool.d/` directories and reading the PHP
     version from each.
   - This is the agent command the UI calls to populate the "PHP
     version" dropdown on domain detail.

4. Register all three in `registry.go`.

5. Tests (validation-only, no systemctl exec):
   - `php_pool_apply`: valid params render correct template; invalid
     username/version/mode rejected; unknown admin_value directive
     rejected; admin_flag value outside `{on,off}` rejected.
   - `php_pool_remove`: param validation only (the filesystem glob is
     functional, not unit-tested; integration test covers it).
   - `php_version_list`: no-op validation; unit test mocks the dir
     listing.

### Verification

```bash
cd /home/shuki/projects/jabali2/panel-agent
go build ./...
go test -race ./internal/commands/... -run "PHPPool|PhpPool|PHPVersion"
go vet ./...
```

### Exit criteria

- Three commands registered, validation-only tests pass.
- Pool template rendering produces a syntactically valid FPM config
  (verify manually by rendering a sample and running `php-fpm -t`).
- Commit: `feat(agent): php.pool.apply/remove + php.version.list`.

---

## Step 5 — Panel API: PHP pool CRUD + ini overrides + domain binding

**Parallel:** yes, with steps 4 and 6 after step 3.
**Model tier:** default.
**Agent:** `tdd-guide` → `backend-dev`.
**Est. complexity:** HIGH.

### Context brief

API handlers live in `panel-api/internal/api/`. Routes are registered in
`panel-api/internal/app/app.go` inside `NewWithDeps`. The canonical
pattern is one handler file per resource (e.g., `databases.go`,
`domains.go`), with a `Register<Entity>Routes(r *gin.RouterGroup, cfg
...Config)` function. Auth is via JWT middleware; owner checks are
inline (see `databases.go` for the pattern).

### Tasks

1. `panel-api/internal/api/php_pools.go`:
   - `GET /api/v1/php-pools` — list. Admin sees all; user sees only
     their own pool.
   - `GET /api/v1/php-pools/:id` — detail.
   - `PUT /api/v1/php-pools/:id` — update. Fields: `php_version`,
     `pm_mode`, `pm_max_children`, `process_idle_timeout_seconds`.
     User can edit only their own pool and only `php_version`; admin
     can edit all fields. Successful update sets `status = pending` so
     the reconciler re-applies.
   - `DELETE /api/v1/php-pools/:id` — admin-only. Refuses with HTTP
     409 if any `domains.php_pool_id = :id` still exists (belt-and-
     suspenders alongside the `ON DELETE SET NULL` FK). The 409 body
     lists the blocking domain IDs so the admin knows what to unbind
     first.
   - `POST /api/v1/php-pools/:id/ini-overrides` — add an override.
     Body: `{ "directive", "value" }`. Validate `directive` is in the
     allowlist (decision 8 in the ADR). Reject if pool is not
     user-owned (for non-admins).
   - `DELETE /api/v1/php-pools/:id/ini-overrides/:override_id` — remove
     an override.

2. `panel-api/internal/api/domain_php_pool.go` (new file, keeps
   `domains.go` from growing past 800 lines):
   - `POST /api/v1/domains/:id/php-pool` — bind. Body: `{ "pool_id" }`.
     Validate that `pool_id` belongs to the same user that owns the
     domain. Sets `domains.php_pool_id`. Triggers nginx regen.
   - `DELETE /api/v1/domains/:id/php-pool` — unbind. Sets
     `domains.php_pool_id = NULL`. Triggers nginx regen.

3. Handler tests: `httptest` + mocked repos + mocked reconciler trigger.
   Cover: owner check, allowlist rejection, cross-user pool binding
   rejection (user trying to bind their domain to another user's pool).

4. Wire routes in `app.go` inside the `v1` group.

### Verification

```bash
cd /home/shuki/projects/jabali2/panel-api
go build ./...
go test -race ./internal/api/... -run "PHPPool|PhpPool|DomainPHPPool"
go vet ./...
```

### Exit criteria

- All routes live; handler tests pass; no unauthorized cross-user
  access possible.
- Commit: `feat(api): PHP pool CRUD, ini overrides, domain binding`.

---

## Step 6 — Nginx vhost: render PHP block from domain.php_pool_id

**Parallel:** yes, with steps 4 and 5 after step 3.
**Model tier:** default.
**Agent:** `backend-dev`.
**Est. complexity:** MEDIUM.

### Context brief

The nginx vhost template is in
`panel-agent/internal/commands/domain_create.go`. It *already* emits a
PHP location block with `fastcgi_pass unix:/run/php/php{{.PHPVersion}}-fpm-{{.Username}}.sock`,
but `.PHPVersion` is always the hard-coded `"8.3"` from
`reconciler.go:304`'s TODO. Step 6 replaces the hard-code with a real
lookup: if `domain.php_pool_id` is set, fetch the pool's `php_version`
and render the PHP block with that; if NULL, omit the PHP block
entirely (static-only site).

### Tasks

1. Gate the PHP location block in the vhost template on a new
   `.HasPHP` boolean. When false, omit the `location ~ \.php$ { ... }`
   stanza entirely. When true, render it with the correct version.

2. In `panel-api/internal/reconciler/reconciler.go`:
   - Replace the `"php_version": "8.3",` hard-code with a pool lookup:
     - If `domain.PHPPoolID` is NULL → `HasPHP=false`, no version.
     - Else → load pool by ID, pass `php_version = pool.PHPVersion`,
       `HasPHP=true`.
   - Remove the `TODO: make configurable` comment.

3. Update `domain_create.go` params:
   - Add `HasPHP bool` alongside `PHPVersion string`.
   - Guard the template render accordingly.

4. Unit test the template render with and without `HasPHP` to confirm
   the PHP block is omitted / present correctly.

5. Integration note (not a task — comment for the reviewer): on
   staging, after this step ships, `/api/v1/reconcile/all` should
   regenerate every domain's vhost. Static-only domains lose their
   stale PHP block; bound domains gain the correct version.

### Verification

```bash
cd /home/shuki/projects/jabali2/panel-agent
go build ./...
go test -race ./internal/commands/... -run "DomainCreate|Template"
go vet ./...
cd /home/shuki/projects/jabali2/panel-api
go test -race ./internal/reconciler/...
```

### Exit criteria

- Domain with `php_pool_id = NULL` renders nginx vhost with no PHP
  location block.
- Domain with `php_pool_id` set renders with `fastcgi_pass` to the
  correct per-user socket for the pool's version.
- No PHP version hard-coded in Go code (grep
  `"8.3"\|"8.2"` under `panel-api/`, `panel-agent/` returns nothing
  outside tests and docs).
- Commit: `feat(nginx): render PHP block from domain.php_pool_id`.

---

## Step 7 — Reconciler: ensure default pool per user + apply pending

**Parallel:** no (needs 4, 5, 6).
**Model tier:** default.
**Agent:** `backend-dev`.
**Est. complexity:** MEDIUM.

### Context brief

`panel-api/internal/reconciler/reconciler.go` runs every 30s and
converges DB → system state for domains and DNS. Add a new pass
`reconcilePHPPools` that:

1. Ensures every panel user has a `php_pools` row (default 8.3, `ondemand`,
   `pm.max_children = 20`). Status set to `pending` on insert.
2. For each `php_pools` row with `status = 'pending'`, builds the apply
   spec (pool fields + ini overrides), calls agent
   `php.pool.apply`, sets `status = 'active'` on success or
   `status = 'error'` with `last_error` on failure.
3. Is idempotent: re-running it on an already-active pool is a no-op
   (agent's apply is also idempotent).

### Tasks

1. Add `reconcilePHPPools(ctx)` as a separate pass (don't block domain
   reconciliation on it). Batch: 50 users per tick.
2. **Ordering — critical.** Apply-then-verify-then-nginx, never the
   reverse, otherwise nginx can reload a vhost referencing a socket
   that doesn't exist yet (silent 502s for 30s). Per row:
   1. Call `php.pool.apply` on the agent.
   2. Verify the socket file exists at the expected path
      (`/run/php/php<v>-fpm-<username>.sock`) — add an agent
      `php.pool.wait_ready` command (or inline a small `stat` check
      in the apply handler itself, with a short bounded retry) so the
      reconciler never proceeds to nginx before the socket is
      connectable.
   3. For every domain bound to this pool, trigger the existing
      `domain.nginx.regen` path so vhost + fastcgi_pass land together.
3. On user delete: handler calls `php.pool.remove` before deleting the
   users row (cascade FK removes `php_pools` row; agent glob-delete
   removes the on-disk pool).
3. Unit tests with sqlmock + mock agent:
   - Missing pool → insert row, agent call, status active.
   - Pending pool → agent call, status active.
   - Error pool → agent call retried; remains error on repeated fail.
   - Active pool → no agent call.
4. Trigger endpoint: add `/api/v1/reconcile/php-pools` (admin-only),
   mirroring the existing `/api/v1/reconcile/all` endpoint for domains.

### Verification

```bash
cd /home/shuki/projects/jabali2/panel-api
go test -race ./internal/reconciler/... -run "PHPPool"
```

### Exit criteria

- Every existing and future user gets a pool within one tick of
  appearing.
- Pending pools converge to active in <30s.
- Error pools are retried on every tick with visible `last_error`.
- Commit: `feat(reconciler): PHP pool convergence pass`.

---

## Step 8 — UI: admin pools page + per-domain PHP version selector

**Parallel:** yes, with step 9 after step 7.
**Model tier:** default.
**Agent:** `mobile-dev` (React/AntD) → `typescript-reviewer`.
**Est. complexity:** MEDIUM.

### Context brief

The UI is React + Refine + Ant Design. Admin resources live under
`panel-ui/src/shells/admin/`, user resources under
`panel-ui/src/shells/user/`. Resources are declared in
`panel-ui/src/App.tsx`. API calls go through `apiClient.ts`. Style
guide: match existing Refine resource patterns (see
`panel-ui/src/shells/admin/domains/` and `user/databases/`).

### Tasks

1. Admin-only pages under `panel-ui/src/shells/admin/php-pools/`:
   - `PHPPoolsList.tsx` — table: user, php_version, pm_mode,
     pm_max_children, status, last_error. Sort + paginate.
   - `PHPPoolEdit.tsx` — form: select php_version (dropdown from
     `/api/v1/php/versions` — new GET endpoint step 5 should add as a
     follow-up if missing, or read from a static list sourced from
     the agent `php.version.list` command).
   - Ini overrides: nested list + "Add override" modal that validates
     the directive client-side against the same allowlist.

2. User-side: on the domain detail page
   (`panel-ui/src/shells/user/domains/DomainEdit.tsx` or similar),
   add a "PHP" section:
   - Dropdown of installed PHP versions + "None (static only)."
   - On change, POST/DELETE to
     `/api/v1/domains/:id/php-pool`.
   - Pool-level settings (pm_mode, max_children) are not exposed on
     the user side — only version choice.

3. Register resources in `App.tsx` with `meta: { shell: "admin" }`
   for the admin pages.

4. Typed `apiClient` methods for all new endpoints.

### Verification

```bash
cd /home/shuki/projects/jabali2/panel-ui
npx tsc -b
npx vite build
```

### Exit criteria

- Admin can create/edit pools and overrides.
- User can switch their domain's PHP version or make it static.
- No build warnings. No TypeScript errors.
- Commit: `feat(ui): PHP pool admin pages + per-domain version selector`.

---

## Step 9 — Observability, docs, E2E, blueprint update

**Parallel:** no (final consolidation).
**Model tier:** default.
**Agent:** `doc-updater` → `code-reviewer`.
**Est. complexity:** LOW.

### Context brief

Close out M9. The phpMyAdmin SSO plan can now be resumed as a
dependent; the `jabali-pma` pool it described becomes "just another
php_pool row" once M9 ships.

### Tasks

1. Audit log: pool apply/remove, version change per domain, ini
   override add/remove. Use the existing structured logger. Log fields:
   `{user_id, pool_id, domain_id, action, old, new}`.
2. `docs/BLUEPRINT.md`: move M9 from Planned to Shipped (anchor commit
   = the final commit of this plan). Add section 4.11 "PHP/FPM pools"
   listing all API paths, migrations, agent commands, and install
   artifacts.
3. `docs/RUNBOOK.md`:
   - Adding a new PHP version post-install:
     `JABALI_PHP_VERSIONS="8.2 8.3 8.4" bash install.sh` — walks through
     Sury + apt + service-enable.
   - Removing a PHP version: check no pools reference it, `apt remove
     php<v>-fpm php<v>-*`, remove sury lines if no versions remain.
   - Diagnosing a stuck pool: `systemctl status php<v>-fpm; journalctl
     -u php<v>-fpm -n 100; cat /etc/php/<v>/fpm/pool.d/jabali-<user>.conf;
     php-fpm<v> -t -y /etc/php/<v>/fpm/pool.d/jabali-<user>.conf`.
   - Rotating an ini override value: PUT the override (step 5 API).
4. E2E: a Playwright test (or scripted curl sequence) that:
   - Creates a domain with a PHP pool bound.
   - `curl -sS http://<domain>/phpinfo.php` returns a PHP info page
     showing the expected version.
   - Switches the pool to a different version; re-requests; sees the
     new version in phpinfo.
   - Switches to NULL (static); re-requests; gets the static file
     (or 404 for a `.php` path).
5. Resume the SSO plan (`plans/phpmyadmin-sso.md`):
   - Rewrite step 6 of the SSO plan to use M9's pool infrastructure
     instead of the `jabali-pma` dedicated pool. sso.php still runs,
     but it runs inside the user's own PHP pool — no new fpm pool
     needed. This is one edit to step 6 and step 9 of that file.
   - Unpark ADR-0022 (remove the "Parked 2026-04-17" banner).

### Verification

- `grep -q "PHP/FPM pools" docs/BLUEPRINT.md`
- `grep -q "Adding a new PHP version" docs/RUNBOOK.md`
- E2E test runs green.

### Exit criteria

- M9 marked shipped in the blueprint.
- Runbook covers add/remove/diagnose.
- SSO plan is updated to consume M9 (but not yet executed).
- Commit: `docs(php): M9 shipped — runbook, blueprint, SSO unpark`.

---

## Dependency graph

```
1 ──► 2 ──► 3 ──┬─► 4 ─┐
                 ├─► 5 ─┼─► 7 ──► 8 ──► 9
                 └─► 6 ─┘
```

Steps 4, 5, 6 are file-disjoint after 3. Steps 8 and 9 are independent
but 9 prefers 8 to have landed first (runbook references the admin UI).

## Rollback notes

- Step 2 (install.sh) — revert: `apt remove php8.2-fpm php8.3-fpm`,
  remove Sury sources list and gpg key, restart nginx.
- Step 3 — revert migrations 028/029/030 via `migrate down 3`. All
  additive schema; no data loss.
- Step 4 — revert agent file changes; restart agent.
- Step 5 — handler files are new; revert removes routes.
- Step 6 — nginx generator edits are small; revert restores the 8.3
  hard-code (feature regression but harmless).
- Step 7 — reconciler pass is a new method; revert removes the method.
- Step 8 — UI pages are new files; revert removes them.
- **No step irreversibly modifies user data.** Pools can be re-created;
  domains keep their docroot; nginx regenerates on every reconciler
  tick.

## Review log

Reviewed by `security-architect` (Opus) on 2026-04-17.
Verdict: FIX-BEFORE-SHIP. Fixes folded in-line:

- **C1: user-rename socket staleness** — decision 13 added,
  documents rename as an unsupported op with explicit future-work
  checklist.
- **C2: `security.limit_extensions` symlink bypass** — decision 12
  added; pool template now hard-codes `open_basedir` with a
  comment explaining why it's not user-overridable.
- **C3: apply-vs-nginx race** — step 7 rewritten with explicit
  ordering (apply → verify socket ready → nginx regen) and an
  agent-side `wait_ready`/stat guard.
- **H1: Sury fingerprint provenance** — step 2 now requires a
  comment block with the URL and last-verified date above the
  fingerprint constant.
- **H2: one-pool-per-user tightness** — decision 3 stays but ADR-0023
  must explicitly flag the constraint as MVP-only and document the
  future multi-pool migration (schema, routes, ownership model).
- **M1: pool deletion leaves stale nginx refs** — step 5 DELETE
  handler refuses with 409 when domains are still bound.
- M2 / M3 (Sury compat matrix + opcache SHM sizing) land in step 9's
  runbook, not as blockers.

## Anti-patterns explicitly forbidden

- ❌ Hard-coding a PHP version anywhere in Go code. All version choices
  come from `php_pools.php_version` (or the `php.version.list` agent
  command for UI dropdowns).
- ❌ Per-domain pool granularity in MVP. The unique constraint on
  `php_pools.user_id` enforces this. Changing to per-domain is a
  future migration.
- ❌ Running the FPM pool as `www-data` or `nobody`. Always
  `<username>:www-data`.
- ❌ Granting a non-owning user write access on another user's pool
  (including admin — admin edits go through the panel API, not the
  filesystem).
- ❌ Allowing arbitrary `php.ini` overrides. Allowlist only. An
  attacker-controlled `open_basedir` override would jailbreak one
  user's pool into another's filesystem.
- ❌ Accepting `admin_value[disable_functions]` overrides through
  the API — the allowlist excludes it, but double-check at the agent
  command layer too.
- ❌ Reusing a single TCP port for all FPM pools. Per-user Unix
  socket, always.
- ❌ Parallel agents writing to the shared main worktree for steps
  4/5/6. Run sequentially or with `isolation: "worktree"`.

## Success criteria for the whole plan

- A fresh `bash install.sh` produces a panel with PHP 8.2 and 8.3
  installed, both fpm services running, no default `www` pool.
- Creating a new panel user `alice` → within 30s, `alice` has a
  `php_pools` row in `active` status and
  `/etc/php/8.3/fpm/pool.d/jabali-alice.conf` exists.
- Creating a domain `alice.example.com` with `php_pool_id = alice's
  pool` → nginx vhost fastcgi_pass matches
  `/run/php/php8.3-fpm-alice.sock`.
- `phpinfo.php` in the docroot returns PHP 8.3 running as user
  `alice`.
- Switching the pool to 8.2 updates nginx, moves the socket,
  `phpinfo.php` returns 8.2 on next request.
- User `alice` cannot bind `bob`'s pool to her domain (403).
- Adding an `open_basedir` ini override via the API returns 400
  (not in allowlist).
- M7 Tranche E SSO plan's step 6 rewrites cleanly on top of M9 (noted
  but not executed here).
