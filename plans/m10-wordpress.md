# Plan: M10 — WordPress 1-click install, delete, clone

**Status:** Reviewed (opus adversarial pass, 3 CRITICAL + 4 HIGH + 5 MED folded in). Ready for Wave A dispatch.
**Owner:** shuki
**Scope:** M10 per `docs/BLUEPRINT.md`
**Depends on:** M2 ✅ (domains), M7 ✅ (databases + users + grants), M9 ✅ (per-user PHP-FPM pools), ADR-0025 ✅ (per-user slices)
**Next migration:** `000033_create_wordpress_installs`
**Next ADR:** `0026-m10-wordpress-installs`
**Working directory:** `/home/shuki/projects/jabali2` — branch `main`

---

## 0. Operating assumptions (read before you start any step)

### Conventions inherited from this repo

- **Commit rhythm:** one commit per step, pushed directly to `main`. No PR workflow (gitea remote, no `gh` CLI). Conventional commits (`feat`, `fix`, `refactor`, `docs`, `test`).
- **Go style:** `gofmt` + `go vet`; table-driven tests; `go test -race -count=1 ./...` must stay green. Handlers follow the existing `api.*HandlerConfig` injection pattern.
- **Migrations:** golang-migrate, both `.up.sql` and `.down.sql`. Schema defaults live in SQL, not Go. Down migration must not silently drop data; drop the new table only.
- **Agent wire:** NDJSON over UDS, `Default.Register("<command>", handler)` pattern in `panel-agent/internal/commands/`. Response struct JSON tags must match what the panel unmarshals **verbatim** — see `~/.claude/projects/-home-shuki-projects-jabali2/memory/feedback_cross_boundary_contracts.md`. For every new command, add a golden fixture in `testdata/` that both the panel unmarshal test and the agent marshal test read, or boot a real agent over a temp UDS in the test.
- **Helpers:** `_log` / `_ok` / `_warn` / `_die` only in `install.sh`. There is no `_error` — past installer steps broke because of that.
- **umask gotcha:** `cat > file` inherits systemd's umask (0077/0027). Follow any `cat >` that targets a file read by another user with explicit `chown` + `chmod`.
- **HTTPS only:** the panel is https-only on `:8443`; nginx default vhost listens on `:443` with the panel TLS cert. Do not introduce any plaintext or port-80-only paths.
- **Per-user execution:** anything that writes into `/home/<user>/domains/<name>/public_html/` must run as the domain-owning OS user and inside that user's slice (`jabali-user-<user>.slice`). Use `systemd-run --uid=<user> --slice=jabali-user-<user>.slice`; do not wrap with `sudo -u` — the systemd path keeps cgroup accounting correct and matches ADR-0025.
- **Reconciler is the convergence engine:** API writes DB state; reconciler reads DB and drives the filesystem/agent. WP install rows start `pending` and the agent or reconciler transitions them. API handlers must never block for the full install — return `202` with the row and let state converge.

### What we are NOT doing in M10

- **Postgres variant** — deferred. M7 shipped MariaDB only.
- **Auto-update / version bumps** — separate ticket.
- **Plugin / theme management UI** — users manage via wp-admin for now.
- **Multisite** — out of scope.
- **Backup / snapshot beyond what `wordpress.clone` uses internally** — separate ticket.
- **phpMyAdmin deep-link to the install's DB** — nice-to-have, postpone.

### Memory pointers relevant to this work

- `feedback_cross_boundary_contracts.md` — panel↔agent JSON tag drift
- `feedback_deps_in_installer.md` — every new system package (wp-cli, `mysqldump`, `rsync`) must land in `install.sh` and in `update.go`'s sync step if the asset is pulled from the repo
- `project_per_user_slices.md` — slice naming and runtime dir pattern

---

## 1. Dependency graph

```
Step 1 (schema + model + repo + ADR)   ─┐
Step 2 (agent: wordpress.install)       ─┤ parallel-safe with Step 1
                                         │
Step 3 (agent: wordpress.delete+clone)   ─┤ needs Step 2 (shares codegen / test harness)
Step 4 (API handlers /wordpress*)        ─┤ needs Step 1
Step 5 (reconciler hook + status)        ─┘ needs Step 1

Step 6 (user UI: list + install + delete) — needs Step 4
Step 7 (user UI: clone modal)            — needs Step 6
Step 8 (admin UI: cross-user list)       — needs Step 6
Step 9 (E2E + docs + blueprint status)   — needs all
```

**True parallel groups:**

- **Wave A:** Step 1 ‖ Step 2 (different files, no shared types)
- **Wave B:** Step 3 ‖ Step 4 ‖ Step 5 (all depend on Wave A; touch different packages)
- **Wave C:** Step 6 (depends on Wave B)
- **Wave D:** Step 7 ‖ Step 8 (both depend on Step 6, different route trees)
- **Wave E:** Step 9

Dispatch **Wave A first**, review both, then Wave B, and so on. Do **not** dispatch the whole graph up-front — each wave's review catches contract drift before the next wave bakes it in.

---

## 2. Model tier per step

| Step | Tier | Why |
|---|---|---|
| 1. schema + ADR | **opus** | design choices baked into SQL and ADR are hardest to change later |
| 2. agent install | default | straightforward subprocess orchestration |
| 3. agent delete+clone | default | clone semantics are subtle — do the dry-run test before marking done |
| 4. API handlers | default | mirrors existing databases.go patterns |
| 5. reconciler hook | **opus** | state-machine correctness under crashes/restarts |
| 6. user UI | default | AntD + Refine, well-trodden |
| 7. clone UI | default | one modal |
| 8. admin UI | default | mirrors user UI |
| 9. E2E + docs | default | mechanical |

---

## 3. Invariants verified after every step

Run all of these before committing any step:

```bash
cd /home/shuki/projects/jabali2
go build ./...                                 # compiles
go test -race -count=1 ./...                   # unit + integration green
cd panel-ui && npx tsc -b                      # UI typecheck green
cd panel-ui && npx vite build                  # UI build green (chunk warn OK)
```

Additionally per step:

- **Any SQL change:** `golang-migrate` up + down dry-run against a scratch DB (see `panel-api/internal/db/migrations` existing tests).
- **Any new agent command:** golden-fixture cross-test per the contract memory.
- **Any UI call that hits a paginated endpoint:** unwrap `{data, total}` — previous regression.

---

## 4. Steps

Each step below is self-contained. A fresh agent can execute it from the context brief alone.

---

### Step 1 — Schema, model, repository, ADR-0026

**Tier:** opus
**Wave:** A
**Depends on:** —
**Parallel with:** Step 2
**Outputs:** new migration, model struct, repository interface + GORM impl, ADR-0026

**Context brief.** M10 adds "WordPress installs" as a first-class entity in the panel. One install belongs to one domain and uses one DB (both already owned by the user). We need a durable record so delete/clone/reconciler can operate without re-scraping the filesystem. The table is small; optimize for clarity, not denormalization.

**Task list.**

1. Write `docs/adr/0026-m10-wordpress-installs.md`. Accepted decisions:
   - One install per domain (`domain_id` is `UNIQUE`). Multiple installs on the same domain are not a Jabali use case; if they become one, a new ADR raises the cap.
   - `db_id` is `NOT NULL` and `ON DELETE RESTRICT` — you cannot drop a DB out from under a live install.
   - `status` is an enum as a CHECK-constrained VARCHAR (matches other tables' pattern). Values: `pending`, `installing`, `ready`, `failed`, `deleting`, `cloning`. No `updating` — version bumps are out of scope.
   - `version` is the installed WordPress version string (e.g. `"6.5.3"`). `NULL` until the agent reports back.
   - Admin username + email are stored; **password is never stored** (issued once, discarded; rotation goes through wp-admin).
   - Locale is a text field defaulting to `en_US`.
   - Installs do not have their own soft-delete — they're cheap to rebuild.
2. Create migration `panel-api/internal/db/migrations/000033_create_wordpress_installs.{up,down}.sql`. Follow the repo's style: backtick-quote table + column names (`` `wordpress_installs` ``) to match existing migrations. Columns:
   - `id` (ULID VARCHAR(26) PK)
   - `user_id` (FK → users)
   - `domain_id` (FK → domains, UNIQUE)
   - `db_id` (FK → databases, ON DELETE RESTRICT)
   - `version` (VARCHAR(32) NULL) — wp core version; `NULL` until agent reports back
   - `admin_username` (VARCHAR(60) NOT NULL) — WordPress admin login; note this is a different concept from the OS user (32-char POSIX cap), so the ADR should call out the asymmetry
   - `admin_email` (VARCHAR(320) NOT NULL)
   - `locale` (VARCHAR(16) NOT NULL DEFAULT 'en_US')
   - `status` (VARCHAR(16) NOT NULL DEFAULT 'pending')
   - `last_error` (VARCHAR(1024) NOT NULL DEFAULT '') — **bounded**; 1024 chars is enough for a one-line truncated error. The API handler (Step 4) truncates agent stderr to 1024 before writing. Default empty string instead of NULL simplifies the GORM update path.
   - `created_at`, `updated_at` (DATETIME(6) NOT NULL)
   - `CHECK (status IN ('pending','installing','ready','failed','deleting','cloning'))`
   - Indexes: `idx_wpinstalls_user_id`, `idx_wpinstalls_status`
   - Down migration drops the table only. No data rewrite.
3. Add model `panel-api/internal/models/wordpress_install.go`. Mirror the table. Add `TableName() string { return "wordpress_installs" }`.
4. Add repository interface + GORM impl at `panel-api/internal/repository/wordpress_install_repository.go`. Methods: `Create`, `FindByID`, `FindByDomainID`, `ListByUserID(ctx, userID, opts) ([]WordPressInstall, int64, error)`, `List(ctx, opts)`, `UpdateStatus(ctx, id, status, lastError *string, version *string)`, `Delete`.
   - Model the status+error+version update as a single dedicated method so callers can't partially update state and race with each other.
5. Repository unit tests with a scratch MariaDB (use existing test harness pattern from `database_repository_test.go`).

**Verification.**
```bash
go build ./... && go test -race -count=1 ./panel-api/internal/repository/ ./panel-api/internal/models/
# Also: apply migration up + down against scratch DB
```

**Exit criteria.**
- Migration applies and reverts cleanly.
- Repo tests cover all 7 methods + the unique-domain_id constraint path.
- ADR is in `Accepted` status and linked from `docs/adr/README.md`.

**Rollback.** Revert the commit. Down migration drops the table — no other impact.

**Commit message.**
```
feat(m10): wordpress_installs schema + model + repository (ADR-0026)
```

---

### Step 2 — Agent command `wordpress.install`

**Tier:** default
**Wave:** A
**Depends on:** —
**Parallel with:** Step 1
**Outputs:** `panel-agent/internal/commands/wordpress_install.go` + test, entry in install.sh for wp-cli, update.go sync if we add a shim

**Context brief.** The agent already owns shell-side provisioning for DBs and users. `wordpress.install` is next in line: given a target docroot, DB credentials, and admin details, install WordPress files and run `wp core install`. Run it as the domain-owning OS user inside that user's slice so resource accounting matches ADR-0025 and the files end up owned correctly from byte zero.

**Task list.**

1. Add `wp-cli` install to `install.sh`. Pin a specific `wp-cli.phar` SHA-256 (check into `install/wp-cli.sha256`). Mirror the phpMyAdmin pattern: download to `/opt/wp-cli/wp-cli-<version>.phar`, symlink `/opt/wp-cli/current`, symlink `/usr/local/bin/wp` → that, mode 0755 root:root.
2. Also install system deps: `php-cli`, `php-mysqli`, `php-curl`, `php-xml`, `php-mbstring`, `rsync`, `mariadb-client`. The first five are for wp-cli itself; `rsync` and `mariadb-client` are for Step 3. Add all of them to `install_base_packages` (and not silently assume they exist — see memory `feedback_deps_in_installer.md`).
3. Request shape (`panel-agent/internal/commands/wordpress_install.go`):
   ```go
   type wordpressInstallReq struct {
       OSUser      string `json:"os_user"`       // domain owner (e.g. "shuki")
       Docroot     string `json:"docroot"`        // /home/shuki/domains/example.com/public_html
       DBName      string `json:"db_name"`        // already-provisioned
       DBUser      string `json:"db_user"`        // already-provisioned
       DBPassword  string `json:"db_password"`    // plaintext, single-use
       DBHost      string `json:"db_host"`        // "localhost" (unix socket) or "127.0.0.1"
       SiteURL     string `json:"site_url"`       // https://example.com
       SiteTitle   string `json:"site_title"`
       AdminUser   string `json:"admin_user"`
       AdminPass   string `json:"admin_pass"`
       AdminEmail  string `json:"admin_email"`
       Locale      string `json:"locale"`
   }
   type wordpressInstallResp struct {
       Version string `json:"version"`            // what wp-cli actually installed
   }
   ```
4. Implementation steps (each wrapped by `systemd-run --uid=<os_user> --gid=<os_user> --slice=jabali-user-<os_user>.slice --pipe --wait --collect`).

   **Credential-handling invariant:** no plaintext DB password or admin password is allowed in argv, env, or a file readable by another process. A breach of this invariant is a review-blocking defect.

   Concrete pattern (picked because it's verifiable and portable):
   - `wp core download --path=<docroot> --locale=<locale> --version=latest`
   - Do **not** pass creds to `wp config create`. Instead: `wp config create --path=<docroot> --dbname=<db_name> --dbuser=<db_user> --dbhost=<db_host> --dbpass=__JABALI_PLACEHOLDER__ --dbcharset=utf8mb4 --dbcollate=utf8mb4_unicode_ci --skip-check`. Then read `<docroot>/wp-config.php` and replace the literal `__JABALI_PLACEHOLDER__` with the real password in-process. Write it back with mode 0640, owner `<os_user>:<os_user>`. The real password is never in argv; the placeholder is harmless.
   - `wp core install --path=<docroot> --url=<site_url> --title=<site_title> --admin_user=<admin_user> --admin_email=<admin_email> --skip-email --prompt=admin_password` with the admin password written to the subprocess's stdin (`cmd.Stdin = strings.NewReader(adminPass + "\n")`). `wp-cli`'s `--prompt` reads exactly one line from stdin when stdin is non-TTY; this is documented and stable. Add a test that asserts `ps -ef` during install does **not** show the admin password in the cmdline (tail the proc/self/cmdline of the subprocess mid-execution).
   - `wp core version --path=<docroot>` → capture → return in response.
5. Before doing anything, validate `Docroot` is within `/home/<OSUser>/domains/` — reject path traversal.
6. On any step failure, attempt a best-effort cleanup: `rm -rf <docroot>/wp-*.php <docroot>/wp-admin <docroot>/wp-content <docroot>/wp-includes <docroot>/readme.html <docroot>/license.txt <docroot>/index.php` (never rm the docroot itself — it's a user-owned directory). Still return the original error.
7. Tests: unit-test the path validator and the command assembly by injecting a fake `exec.CommandContext` runner; integration test stays a manual checklist on a real host (document at the top of the file).
8. Register in agent: `Default.Register("wordpress.install", wordpressInstallHandler)`.

**Cross-boundary contract.** Add `panel-agent/internal/commands/testdata/wordpress_install_resp.json` with the exact wire shape. The API handler in Step 4 unmarshals the same fixture in a test to prove the JSON tags agree.

**Verification.**
```bash
go build ./... && go test -race -count=1 ./panel-agent/...
# Manual integration: agentctl invoke wordpress.install ... on a scratch domain
```

**Exit criteria.**
- Command is registered, unit tests pass including path-traversal rejection.
- `wp-cli.phar` is installed by `install.sh` with pinned SHA.
- `install/wp-cli.sha256` exists and matches the checked-in version number.

**Rollback.** Revert the commit. Uninstall `wp-cli.phar` manually if desired (not required — dormant binary harms nothing).

**Commit message.**
```
feat(agent,m10): wordpress.install command + wp-cli provisioning
```

---

### Step 3 — Agent commands `wordpress.delete` + `wordpress.clone`

**Tier:** default
**Wave:** B
**Depends on:** Step 2
**Parallel with:** Step 4, Step 5
**Outputs:** two new agent commands

**Context brief.** Delete wipes an install's files (DB teardown is handled by the API via the existing M7 flow, not by this command). Clone produces a second install from a first: copies files, dumps the source DB, restores into a second DB, rewrites `wp-config.php` for the new DB creds, and runs `wp search-replace` for the siteurl/home fields.

**Task list.**

1. `wordpress.delete`:
   - Request: `{os_user, docroot}`.
   - Validate docroot is within `/home/<os_user>/domains/`.
   - Under `systemd-run` as the OS user: remove the WordPress file footprint (the list from Step 2 task 6). Do **not** `rm -rf` the docroot itself; leave the directory empty.
   - Response (explicit shape, matches the golden-fixture contract): `{status: "deleted"}`.
2. `wordpress.clone`:
   - Request:
     ```go
     type wordpressCloneReq struct {
         OSUser           string `json:"os_user"`
         SrcDocroot       string `json:"src_docroot"`
         DstDocroot       string `json:"dst_docroot"`
         SrcDBName        string `json:"src_db_name"`
         DstDBName        string `json:"dst_db_name"`
         DstDBUser        string `json:"dst_db_user"`
         DstDBPassword    string `json:"dst_db_password"`
         DstDBHost        string `json:"dst_db_host"`
         SrcSiteURL       string `json:"src_site_url"`
         DstSiteURL       string `json:"dst_site_url"`
     }
     ```
   - Validate both docroots are within `/home/<os_user>/domains/`.
   - `rsync -a --delete <src_docroot>/ <dst_docroot>/` (trailing slash — copy contents, not dir).
   - `mysqldump --single-transaction <src_db_name> | mysql <dst_db_name>` (pipe; do **not** write dump to disk). Dump runs as the panel's DB admin user read from the agent's env (same path M7 uses); restore runs as the same. This avoids ever embedding user DB passwords in argv.
   - Rewrite `<dst_docroot>/wp-config.php` using `wp config set DB_NAME/DB_USER/DB_PASSWORD/DB_HOST --path=<dst_docroot>`.
   - `wp search-replace --path=<dst_docroot> --all-tables <src_site_url> <dst_site_url>`.
   - Response: `{version: "6.5.3"}` (from `wp core version --path=<dst_docroot>`).
3. Clone is **not crash-safe and not retryable in place.** If rsync succeeds but `wp search-replace` fails, retrying the same command on the same dst will re-rsync (harmless) and fail again on search-replace. The recovery model is: the API handler marks the row `failed`, the user deletes the dst install (which tears down its DB + docroot contents), and retries from scratch. Do **not** attempt in-place resume. The handler must pre-check that dst_docroot is empty before calling the agent — the command itself does not.
4. Tests: same path-validation unit tests as Step 2; pipe/command assembly covered by injecting a fake runner.

**Verification.**
```bash
go build ./... && go test -race -count=1 ./panel-agent/...
```

**Exit criteria.** Both commands registered and unit-tested. Both cross-boundary fixtures in `testdata/`.

**Rollback.** Revert the commit.

**Commit message.**
```
feat(agent,m10): wordpress.delete + wordpress.clone commands
```

---

### Step 4 — API handlers `/api/v1/wordpress*`

**Tier:** default
**Wave:** B
**Depends on:** Step 1
**Parallel with:** Step 3, Step 5
**Outputs:** `panel-api/internal/api/wordpress.go`, registered in `app.go`

**Context brief.** The API mediates the install/delete/clone flows. It provisions DB + DB-user via the existing M7 repo methods (reuse, don't duplicate), writes the `wordpress_installs` row in `pending`, kicks the agent asynchronously, and returns the row immediately. The reconciler (Step 5) watches for stuck rows; the agent's reply flips the row to `ready` via a dedicated status endpoint the agent calls back through the internal API surface.

**Task list.**

1. Routes:
   - `POST /api/v1/wordpress` — install.
   - `GET /api/v1/wordpress` — list (admin: all; user: scoped).
   - `GET /api/v1/wordpress/:id` — detail.
   - `DELETE /api/v1/wordpress/:id` — tear down.
   - `POST /api/v1/wordpress/:id/clone` — clone.
   - `POST /api/v1/wordpress/:id/health` — on-demand health probe (runs `wp core is-installed` + `wp core version` via agent and checks HTTP 200 on the domain). Returns `{wp_installed, wp_version, http_status}`. Not periodic — invoked only from the UI's "Run health check" button.

   **Owner check on every mutation or detail route.** Implement `FindByIDAndUserID(ctx, installID, userID)` in the repo (Step 1) and use it in `GET /:id`, `DELETE /:id`, `POST /:id/clone`, and `POST /:id/health`. Admins bypass via `claims.IsAdmin`, but non-admin callers never see an install they do not own — return 404 (not 403) to avoid leaking ID existence.
2. `POST /wordpress` request shape:
   ```go
   type createWordPressRequest struct {
       DomainID      string `json:"domain_id"   binding:"required"`
       SiteTitle     string `json:"site_title"  binding:"required"`
       AdminUsername string `json:"admin_username" binding:"required"`
       AdminEmail    string `json:"admin_email" binding:"required,email"`
       AdminPassword string `json:"admin_password"` // optional; generate if blank
       Locale        string `json:"locale"`          // default en_US
   }
   ```
3. Install flow (synchronous parts; agent call is async):
   - Validate domain exists and caller owns it.
   - Generate a DB name suffix (`wp_<6-char-id>`); provision DB + user + grant by calling the existing handler internals directly (`h.cfg.Databases.Create`, `h.cfg.DatabaseUsers.Create`, `h.cfg.DatabaseUserGrants.Add`). **No shared `db_quick_setup.go` helper is created in M10.** The Quick Setup modal remains UI-side for now; extracting a shared helper is deferred to a future refactor ticket because it would reshape two existing public API handlers mid-stream and isn't required by M10 itself.
   - Insert `wordpress_installs` row with `status = 'pending'`, admin creds, locale.
   - Spawn a goroutine that calls `agent.Call("wordpress.install", ...)`, flips the row to `installing` at start, then `ready` with the returned version on success, or `failed` with `last_error` (truncated to 1024 chars) on error. The goroutine owns its own context derived from `context.Background()` with a 5-minute timeout — do **not** use `c.Request.Context()`, which dies when the HTTP response returns.
   - **Crash-recovery window.** The goroutine is fire-and-forget: if the panel process crashes between row insert and agent response, the row stays in `installing` indefinitely. The reconciler (Step 5) sweeps `installing` rows older than its configured timeout and flips them to `failed`. Maximum time a row can stay stuck = 5 min (goroutine timeout, if panel survives) + reconciler interval. Document this window in the runbook.
   - Respond `202 Accepted` with the row and the generated admin password (once, like M7's DB-user-create).
4. `DELETE /wordpress/:id`:
   - Flip row to `deleting`.
   - Call agent `wordpress.delete`. On success, drop DB + DB-user (reuse M7 paths), then delete the row.
   - On agent failure, leave row in `failed` with `last_error`; do not delete the DB (operator may want to inspect).
5. `POST /wordpress/:id/clone`:
   - Body: `{dest_domain_id}`. Dest domain must belong to the same user (cross-user clone is out of scope; reject 403).
   - Dest domain must not already host an install (FK + UNIQUE gives us this, but return a clear 409 instead of the DB error).
   - Provision new DB + user.
   - Insert new `wordpress_installs` row with `status = 'cloning'`.
   - Goroutine → `agent.Call("wordpress.clone", ...)`. On success, row → `ready`. On failure, row → `failed` + last_error and the API handler issues a best-effort `wordpress.delete` + DB drop.
6. Repository helper: `CreateInstallAndKickAgent(ctx, row, agentParams)` — returns the inserted row id; kicks the agent in a goroutine internal to the helper. The handler doesn't spawn goroutines directly — it calls the helper.
7. Tests: table-driven handler tests covering happy path, domain ownership, already-installed-on-domain, quota exceeded (if the user has a per-package cap; if not, skip). Mock the agent and DB-provisioning subroutines.

**Quick Setup helper deferral.** See task 3 — the extraction is deferred. Step 4 re-implements the DB+user+grant flow inline inside the WordPress create handler using the existing repo methods. This means ~15 lines of duplicate orchestration code between WordPress install and the UI Quick Setup modal; acceptable, and a magnet for a future refactor ticket once a third caller appears.

**Verification.**
```bash
go build ./... && go test -race -count=1 ./panel-api/...
```

**Exit criteria.** All five routes unit-tested. Agent mock asserts the JSON tag names verbatim (not via a re-encoded Go struct) using the testdata fixture from Step 2.

**Rollback.** Revert. Row cleanup: if rollback happens mid-deploy with stuck `installing` rows, the reconciler (Step 5) will flip them to `failed` after timeout.

**Commit message.**
```
feat(api,m10): wordpress install/delete/clone + async reconcile kickoff
```

---

### Step 5 — Reconciler hook: stuck-install sweep + drift detection

**Tier:** opus
**Wave:** B
**Depends on:** Step 1
**Parallel with:** Step 3, Step 4
**Outputs:** reconciler loop extension, tests

**Context brief.** If the panel restarts mid-install, a row is left in `installing` with no goroutine watching it. The reconciler's job is to notice and re-drive. It must also handle the reverse: `ready` rows whose docroot got wiped by a user, or whose DB was dropped out from under them — those flip to `failed` and surface in the UI.

**Task list.**

1. Add a reconcile pass `reconcileWordPressInstalls(ctx)`:
   - **Per-state timeouts**, configurable via env (documented defaults):
     - `WORDPRESS_INSTALL_TIMEOUT` = 10m (download + config + install — bounded upload-ish)
     - `WORDPRESS_CLONE_TIMEOUT` = 30m (rsync + mysqldump can take real time on multi-GB installs)
     - `WORDPRESS_DELETE_TIMEOUT` = 5m (rm-bound — should be sub-second but tolerate disk pressure)
   - Pick up rows older than the state's timeout → flag `failed` with `last_error = "operation exceeded <Nm> timeout"`. Do **not** auto-retry. Retry is a user action.
   - For `ready` rows: probe `<docroot>/wp-includes/version.php` existence via a stat agent call. If missing, mark `failed` with `last_error = "docroot missing wp-includes"`. If present and row's `version` is NULL/empty, update it.
   - **Probe rate limit.** The stat call is per install per tick. For hosts with ≥500 installs this dominates reconciler cost. Cap at 100 probes per tick (round-robin by `updated_at` ascending so every install is re-probed roughly every `ceil(N/100) * tick_interval`). Config: `WORDPRESS_PROBE_BATCH` = 100. Document the tuning knob in the runbook.
   - Do **not** probe HTTP — reconciler is UDS-only. HTTP health is a separate per-request check exposed via `POST /wordpress/:id/health` (Step 4).
2. Tests: table-driven, mocking the time source and the agent, cover (a) stuck row transitions to failed after timeout, (b) no-op when row is ready and files exist, (c) transition to failed when files disappear.
3. Register the pass in the reconciler's main loop; keep the overall tick interval; guard behind a feature flag if the existing reconciler pattern has one (check `reconciler.go`).

**Verification.**
```bash
go test -race -count=1 ./panel-api/internal/reconciler/...
```

**Exit criteria.** Reconciler passes tests, no regression in existing reconciler tests. Stuck-row timeout is configurable via env (`WORDPRESS_INSTALL_TIMEOUT` default 10m).

**Rollback.** Revert. Stuck rows will stay stuck; operator can `UPDATE wordpress_installs SET status='failed'` manually.

**Commit message.**
```
feat(reconciler,m10): sweep stuck WordPress installs + detect drift
```

---

### Step 6 — User UI: list + install + delete

**Tier:** default
**Wave:** C
**Depends on:** Step 4
**Outputs:** new user shell page + Refine resource registration

**Context brief.** A new sidebar item "WordPress" sits below "Databases". The page has a list table (domain, version, status badge, admin_email, Install/Delete actions) and a "New WordPress install" button that opens a modal.

**Task list.**

1. Register Refine resource `wordpress` (API slug matches — `/api/v1/wordpress`) and user-shell route `/jabali-panel/wordpress`.
2. List table: columns `Domain`, `Version`, `Status` (badge: `pending`/`installing`/`cloning`/`deleting` → processing spinner, `ready` → green, `failed` → red + tooltip with `last_error`), `Admin email`, Actions.
3. Install modal:
   - Fields: `Domain` (dropdown, filter out domains that already host an install), `Site title`, `Admin username` (default: caller's panel username), `Admin email` (default: caller's panel email), `Admin password` (optional; if blank the API generates), `Locale` (dropdown with common locales, default `en_US`).
   - On submit: `POST /wordpress`; on success show password reveal once (same pattern as Quick Setup password) and refetch list.
   - Use `useInvalidate()` on both `wordpress` and `databases` — the create path also creates a DB that appears in the Databases page.
4. Delete button: confirm modal spelling "delete", warns that the database and files will be removed.
5. Handle paginated envelope unwrap when listing domains for the dropdown — prior regression.

**Verification.**
```bash
cd panel-ui && npx tsc -b && npx vite build
```

**Exit criteria.** Install happy path creates a row that shows `installing` → `ready` within 2 minutes on the test host. Delete tears down the DB and removes the row. Paginated domain dropdown works with multiple domains.

**Rollback.** Revert commit. Route is additive; no existing UI affected.

**Commit message.**
```
feat(ui,m10): user WordPress page — list, install, delete
```

---

### Step 7 — User UI: clone modal

**Tier:** default
**Wave:** D
**Depends on:** Step 6
**Parallel with:** Step 8
**Outputs:** clone action in the install row

**Context brief.** Clone is a per-row action. User picks a destination domain from the dropdown (filtered to domains they own that don't already host an install). One POST call, same async row-flip UX as install.

**Task list.**

1. Add `Clone` button next to Delete on the row. **Disabled when `status !== 'ready'`** with tooltip "Clone is only available for healthy installs". The API still enforces 409 on non-ready clones; the UI pre-empts the error.
2. Modal: single field — destination domain dropdown, disabled-options list for domains already hosting installs, helper text "Files will be copied and the database will be duplicated with a fresh name. siteurl and home will be rewritten automatically.".
3. On submit: `POST /wordpress/:id/clone`. Show success toast; invalidate list.
4. List shows both source and clone as separate rows.

**Verification.** `npx tsc -b && npx vite build`. Happy path manually verified on the test host.

**Exit criteria.** Cloning one install to a fresh domain produces a browsable WordPress at the new domain with an independent DB.

**Rollback.** Revert commit.

**Commit message.**
```
feat(ui,m10): clone action on WordPress installs
```

---

### Step 8 — Admin UI: cross-user list

**Tier:** default
**Wave:** D
**Depends on:** Step 6
**Parallel with:** Step 7
**Outputs:** admin shell page

**Context brief.** Admins need visibility into every WordPress install across the server to spot failed installs, out-of-date versions, and orphaned rows.

**Task list.**

1. Add admin-shell route `/jabali-admin/wordpress` with a read-only list: columns `User`, `Domain`, `Version`, `Status`, `Created`.
2. No create / edit / delete actions on the admin page — admins act via impersonation if they need to act.
3. Status filter + sort by `Created desc` by default.

**Verification.** `npx tsc -b && npx vite build`.

**Exit criteria.** Admin can see installs from at least two different users on the test host.

**Rollback.** Revert commit.

**Commit message.**
```
feat(admin-ui,m10): cross-user WordPress installs list
```

---

### Step 9 — E2E, docs, blueprint status

**Tier:** default
**Wave:** E
**Depends on:** all prior
**Outputs:** Playwright test, updated BLUEPRINT.md, runbook

**Task list.**

1. Playwright test `panel-ui/e2e/wordpress.spec.ts`:
   - User log in → Databases page exists.
   - Navigate to WordPress page → Install modal → fill + submit.
   - Poll list for `ready` status (up to 2min).
   - Visit `https://<domain>/wp-login.php` (inside the Playwright context, bypass cert) → login with admin creds → lands on dashboard.
   - Back to panel, **Clone action → pick second domain → submit → poll for `ready` on the clone** → navigate to Databases page and verify two WP-named DBs exist with different names → visit the clone's URL and confirm login with the same admin creds works (clone preserves credentials).
   - Delete both installs → confirm rows disappear.
2. Runbook `plans/m10-wordpress-runbook.md`: how to troubleshoot stuck installs, how to force-fail a row, how to manually trigger reconciler drift detection.
3. Flip `docs/BLUEPRINT.md` M10 section to `**Status:** Shipped (commits X…Y)`. Same pattern M9 used.
4. Update `docs/adr/0026-...` status to `Accepted` if it wasn't already; link it from ADR README.
5. Update memory: append `project_m10_wordpress.md` summarizing the plan outcome (1–2 line pointer).

**Verification.**
```bash
cd panel-ui && npx playwright test wordpress
```

**Exit criteria.** Playwright green. BLUEPRINT status updated. Runbook published.

**Rollback.** Revert. Does not block prior steps.

**Commit message.**
```
test(m10),docs(m10): Playwright E2E + runbook + blueprint status
```

---

## 5. Open questions (surface during review)

1. **wp-cli trust model.** wp-cli downloads arbitrary WordPress core ZIPs from wordpress.org/wp-cli.org. Do we pin WP core version for installs (M10 says "install latest" but is that right for a hosting panel)? **Proposed default:** install latest stable, no pin — matches user expectation. Admin can override at install time via `version` field (optional; not in the current request shape — deferred).
2. **Cloning across PHP pools.** What if src and dst domains are on different PHP versions? wp-cli runs server-side PHP, so it uses whatever the agent's PHP is, not the target site's. This might matter for plugins with version-specific schemas. **Proposed:** reject clone if src and dst domains point at different PHP pools; surface the conflict in the UI.
3. **Orphaned-DB cleanup policy.** If an install's row goes `failed` and the user retries with a different domain/DB, do we auto-drop the stranded DB? **Proposed:** no. Retries are user-driven; strand remains until user deletes via the Databases page. Document this.

Tag these **[OPEN]** in the ADR until Step 9 closes them one way or another.

---

## 6. Parallel dispatch plan

After review approves this document:

- **Dispatch Wave A now:** one agent on Step 1 (opus), one on Step 2 (default). Both in isolation worktrees (`isolation: "worktree"`) so conflicts never happen on `main`.

---

## 7. Review changelog

Adversarial review (opus, 2026-04-18) returned REWORK with 3 CRITICAL + 4 HIGH + 7 MED/LOW findings. Folded into the plan:

- **Step 1:** `last_error` bounded to VARCHAR(1024) NOT NULL DEFAULT ''; backtick-quoted SQL identifiers; ADR note on admin_username (60) vs OS user (32) asymmetry.
- **Step 2:** Credential-handling invariant added; `__JABALI_PLACEHOLDER__` rewrite pattern for DB password in wp-config.php (no plaintext in argv/env); `wp core install --prompt=admin_password` reading one line from stdin; explicit test requirement for no-password-in-cmdline.
- **Step 3:** Clone declared explicitly non-idempotent and non-resumable; retry model is user-driven delete+redo; delete response shape pinned to `{status: "deleted"}`.
- **Step 4:** `FindByIDAndUserID` owner check on every mutation/detail route (404 not 403); explicit crash-recovery window documentation; Quick Setup helper refactor deferred out of M10 (inline the DB trio instead); new `POST /wordpress/:id/health` route added to satisfy the BLUEPRINT health-check deliverable.
- **Step 5:** Per-state timeouts (`INSTALL` 10m, `CLONE` 30m, `DELETE` 5m); `WORDPRESS_PROBE_BATCH` rate limit for `ready`-row drift probing.
- **Step 6:** `Clone` button disabled when `status !== 'ready'` with tooltip.
- **Step 9:** Playwright covers the clone path end-to-end (install → clone → verify independent DB → login on clone).

Findings deferred as acceptable limitations, documented in the ADR or runbook rather than fixed in the plan:
- Agent DB admin creds in agent-process env (MEDIUM #11) — agent runs as root on the operator's host; acceptable.
- wp-cli latest-stable default (open question #1) — user can override at install time in a follow-up.
- Clone across PHP pools (open question #2) — UI check in a follow-up ticket.
- Orphaned-DB cleanup (open question #3) — user-driven cleanup, documented in runbook.
- **Review Wave A outputs** against the exit criteria and the contract-memory guidance (golden fixture for Step 2).
- **Then dispatch Wave B:** three agents in parallel (Step 3 default, Step 4 default, Step 5 opus).
- Continue wave-by-wave. Do **not** skip a wave's review — the wave's review catches cross-step contract drift before the next wave bakes it in.
