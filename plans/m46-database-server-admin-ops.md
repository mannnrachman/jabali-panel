# M46 — Database Server Admin Ops (Server Settings ▸ Databases tab)

**Branch:** `m46/database-server-admin-ops`
**Status:** Blueprint — advisor-reviewed, B1–B11 folded. Ready to execute Step 0.
**Next free migration:** `000135`
**Next free ADR:** `0096`
**Milestone #:** M46 (M45 = root web terminal, highest on `main`)

---

## 1. Goal

cPanel/WHM-parity database administration, surfaced in the **existing**
`Server Settings ▸ Databases` tab (`panel-ui/src/shells/admin/settings/DatabasesCard.tsx`,
already wired as tab key `"databases"` in `ServerSettingsPage.tsx`). No new
nav/tab plumbing.

Six operator capabilities, **both engines** (MariaDB + PostgreSQL) per user
decision 2026-05-16:

| # | Feature | Status before M46 | M46 work |
|---|---------|-------------------|----------|
| 1 | Change DB **root/superuser** password | none (MariaDB); pg pw exists at `/etc/jabali-panel/postgres.password` | NEW — option **A**: password *alongside* socket/peer auth |
| 2 | Change DB **user** password | **SHIPPED** — `POST /database-users/:id/rotate-password` → agent `db_user.rotate_password` / `db.postgres.create_role`; `DatabaseUsersList`+`DatabaseUserDrawer` UI | Verify row action present; surface a link from Databases tab. No backend work. |
| 3 | Edit Database Configuration | none | NEW — curated allowlist tuner, **reconciler-converged** |
| 4 | Log in to phpMyAdmin / Adminer | **per-DB-user SSO SHIPPED** (`/sso/phpmyadmin`, `/sso/adminer`, ownership-checked) | NEW — **admin all-DBs** privileged SSO entry |
| 5 | Database **Maintenance** (was "Repair") | none | NEW — InnoDB-honest optimize+analyze / pg `REINDEX`+`VACUUM` |
| 6 | Show Database Processes | none | NEW — `SHOW FULL PROCESSLIST`+`KILL` / `pg_stat_activity`+`pg_terminate_backend` |

---

## 2. Load-bearing constraint (do NOT regress)

**MariaDB root = `unix_socket` auth by design. PostgreSQL `postgres` = `peer`
auth on the local socket by design.** Both are deliberately password-less for
the panel's own access path:

- `install.sh:1660` hard-asserts `MariaDB unreachable via unix_socket auth as root` — a validator. Do not weaken it.
- `install.sh:~2012-2042` keeps Debian default `pg_hba` peer-on-socket; panel-api connects as `postgres` via peer; a password is *also* set and stashed at `/etc/jabali-panel/postgres.password` (`root:jabali`) as a documented backup path.
- `db_create`, `backup_databases`, `db_restore`, `db_size`, maintenance (#5), processlist (#6) all connect **root-over-socket / postgres-over-peer**. Nothing in M46 may make panel/agent depend on a password to reach the DB.

**User decision: Option A.** Add/rotate a root (MariaDB) / superuser (Postgres)
password that *coexists* with socket/peer auth. ADR-0096 records this and
**amends, does not supersede**, the socket-auth assumption (the invariant
stays; we add an *additional* credential).

### B1 — Mandatory MariaDB grammar (verified via Context7, MariaDB official docs)

MariaDB `ALTER USER ... IDENTIFIED VIA X OR Y` **removes any method not
re-stated.** A naive `ALTER USER 'root'@'localhost' IDENTIFIED VIA
mysql_native_password USING PASSWORD(...)` **silently drops `unix_socket`**
→ breaks install.sh:1660 + panel/backup/repair/db_create. The **only**
acceptable statement (socket listed first so the panel's socket path keeps
winning; supported since MariaDB 10.4, safe on 11.4/11.8 = Ubuntu 24.04 /
Debian 13):

```sql
ALTER USER 'root'@'localhost'
  IDENTIFIED VIA unix_socket
  OR mysql_native_password USING PASSWORD('<generated>');
```

Step 1 MUST: (a) use exactly this form, (b) `SHOW CREATE USER 'root'@'localhost'`
post-apply and assert the output still contains `unix_socket`, (c) agent
self-probe `mysql --protocol=socket -e 'SELECT 1'` as root BEFORE returning
success, (d) on any failure, restore prior auth and return error. This is the
runtime guard for the install.sh:1660 invariant.

For Postgres this is largely *exposing* rotation of the existing
`/etc/jabali-panel/postgres.password` (`ALTER ROLE postgres WITH PASSWORD …`;
peer auth on socket is untouched), not a new model.

### B2 — No overlap with `db_mysqladmin_ensure`

`panel-agent/internal/commands/db_mysqladmin_ensure.go` creates a **per-panel-user**
shadow `<user>_mysqladmin@localhost` with `GRANT ALL ON `\``<user>\_%`\``.*`
(scoped to that user's DBs) — the per-user PMA SSO account. It does NOT touch
root and is NOT server-wide. Step 1 (root pw) has zero overlap. Step 4
(admin all-DBs shadow) **reuses its proven patterns**: `panelUsernameRegex`
validation, `EscapeMariaDBLiteral`, `CREATE USER IF NOT EXISTS` + `ALTER USER`
idempotent pair, and "never echo mysql stderr (may contain password)".

---

## 3. ADRs (drafts land in Step 0, status Proposed; Accepted in Step 7)

| ADR | Title | Decision |
|-----|-------|----------|
| 0096 | Root/superuser password alongside socket/peer auth | Option A. MariaDB: the §2-B1 dual-`VIA` statement, socket-first, with the 4 mandatory guards. Postgres: `ALTER ROLE postgres WITH PASSWORD`, peer untouched. Secret at rest: MariaDB → new `/etc/jabali-panel/mysql-root.password` (`root:jabali` 0640) mirroring the pg file; written via **tmp + atomic rename** (B11); never in DB, never in API responses (reveal-once only). The install.sh:1660 validator and the socket/peer panel path are explicitly preserved. |
| 0097 | Curated, reconciler-converged DB config tuner | Allowlist of known-safe keys only — no raw editor (typo → mariadbd won't start → panel loses its own DB → unfixable from UI; this is exactly why cPanel curates). DB (`db_tuning_settings`) is the **source of truth**; the reconciler renders config from the table every tick and reloads only on divergence (B3, ADR-0002/0004 parity with nginx-file-per-vhost). MariaDB → managed drop-in `/etc/mysql/mariadb.conf.d/zz-jabali-tuning.cnf`; Postgres → `ALTER SYSTEM SET` → `postgresql.auto.conf` (reversible via `RESET`). Dry-run validate before apply; auto-rollback; rollback-of-rollback path (B7). |
| 0098 | Admin-scoped **privileged** DB web access (phpMyAdmin/Adminer all-DBs) | Honest framing (B4): **this is a privileged web shell over every database on the box.** The MariaDB admin shadow is effectively `ALL PRIVILEGES ON *.*` (minus account-mgmt meta) — that is inherent to "see and edit all DBs incl. future ones", not a weakness to grant-trim. The threat model is controlled by the **gating**, not the grant: `RequireAdmin` + same-origin (Origin/Referer) CSRF + single-use short-TTL token + a distinct audit line `scope=admin`. Postgres: Adminer as `postgres` superuser via existing peer/secret. Separate endpoints from the per-user SSO; per-user ownership check intentionally absent. |
| 0099 | DB maintenance + processlist privilege model | Maintenance / processlist / KILL run **agent-side root-over-socket (MariaDB) / postgres-over-peer (Postgres)**. `jabali_panel` is NOT granted `PROCESS`/`SUPER`/superuser (no schema migration touches its grants). `KILL` / `pg_terminate_backend` audited with actor + target id. Concurrency: one maintenance job per engine at a time, enforced by the jobs table (B10 → 409). |

---

## 4. Wave / step plan

Sequential where steps share migration `000135` / `agentwire` types; UI assembly
(Step "6/UI") gates on 1/3/4/5/6-backend. One branch-internal commit per step.
Execute **inline** (`feedback_never_agents`); briefs are cold-start-complete.

### Step 0 — Foundation (migration + wire + ADR stubs)
- Migration `000135_m46_db_admin.sql`, **schema only** (`feedback_migration_data_seed_ordering` — no seed from app-populated tables; tuning defaults seeded by the app on first read, not the migration):
  - `db_tuning_settings` — `id` CHAR(26) ULID PK, `engine` ENUM('mariadb','postgres'), `param` VARCHAR, `value` VARCHAR, `applied_at` DATETIME NULL, `applied_by` CHAR(26) NULL, UNIQUE(`engine`,`param`). `utf8mb4_unicode_ci` on the table and every FK-bearing col (`feedback_mariadb_collation_fk`).
  - `db_admin_jobs` (B5/B10) — `id` ULID PK, `engine`, `kind` ('maintenance'), `scope` (all|<db>), `status` ('running'|'ok'|'error'), `summary` TEXT, `actor_user_id` CHAR(26), `started_at`, `finished_at` NULL. Survives `systemctl restart jabali-panel`; powers the 409 concurrent-guard.
  - `db_admin_audit` — `id` ULID PK, `ts`, `actor_user_id`, `engine`, `action`, `target`, `outcome`. Retention (B9): pruned >180d by an existing reconciler housekeeping pass (or documented operator-pruned in the runbook if no housekeeping hook is cheap).
- `agentwire/`: req/resp structs for every new command (Steps 1/3/5/6). Enumerate property KINDS not just names — tagged-enum vs bare string, required-at-create (`feedback_schema_enumerate_kinds_not_names`). `omitempty` on server-assigned fields (`feedback_go_json_omitempty_create`).
- M14 event sources (B8) decided here: emit `db.admin.root_password_rotated`, `db.admin.config_applied` (ok/fail), `db.admin.config_apply_failed_unrecoverable` (critical, B7), `db.admin.maintenance_finished`. **No** event for process-kill (too noisy).
- Write ADR drafts 0096–0099 (status **Proposed**).
- Branch-local; no behaviour change.

### Step 1 — Root/superuser password (feature #1)
- Agent `db.root.set_password` (MariaDB): generate strong pw; run the **exact §2-B1 statement**; the 4 guards (SHOW CREATE USER contains `unix_socket`, socket self-probe, restore-on-fail); write `/etc/jabali-panel/mysql-root.password` 0640 `root:jabali` via tmp+atomic-rename (B11); never echo mysql stderr. Agent `db.postgres.superuser.set_password`: `ALTER ROLE postgres WITH PASSWORD`; rewrite `/etc/jabali-panel/postgres.password` (tmp+rename); peer untouched.
- panel-api: `POST /api/v1/admin/databases/root-password {engine}` → reveal-once response (mirror M7 `rotateDatabaseUserPassword` shape), `RequireAdmin`, audited, emits `db.admin.root_password_rotated`.
- UI: "Root / superuser password" section in DatabasesCard — per-engine Set/Rotate, reveal-once modal + copy, danger confirm.

### Step 2 — (verification only) DB user password
- Confirm `DatabaseUsersList.tsx` exposes the rotate-password row action (advisor: almost certainly present). If yes: add a one-line `<Button>`/link from the Databases tab → `/jabali-admin/database-users`. Only if a real gap: minimal fix. No new agent/endpoint/migration.

### Step 3 — Edit Database Configuration (feature #3), reconciler-converged
- Allowlist registry `internal/dbtuning/` (repo-root internal, shared panel-api⇄reconciler⇄agent): per key — name, engine, type, min/max, unit, default, restart-required. MariaDB: `max_connections`, `innodb_buffer_pool_size`, `innodb_log_file_size`, `max_allowed_packet`, `table_open_cache`, `tmp_table_size`, `slow_query_log`, `long_query_time` (version-gate `query_cache_*`). Postgres: `max_connections`, `shared_buffers`, `work_mem`, `maintenance_work_mem`, `effective_cache_size`, `wal_buffers`, `random_page_cost`.
- Agent `db.config.apply` (MariaDB): render candidate drop-in → validate (`mariadbd --defaults-file=<candidate> --validate-config`, fallback `mysqld --help --verbose` dry parse) → atomic-rename into `zz-jabali-tuning.cnf`, keep prior as `.bak`, `systemctl reload-or-restart mariadb`, post-restart health probe; **fail → restore `.bak` + restart**. **B7 rollback-of-rollback**: if the restore-restart also fails, write one-shot marker `/var/lib/jabali-agent/db-config-broken.json` AND emit M14 `db.admin.config_apply_failed_unrecoverable` (critical) so the operator hears it from the notification stack, not a tenant ticket. Agent `db.postgres.config.apply`: `ALTER SYSTEM SET` allowlisted keys → `SELECT pg_reload_conf()`; restart-required keys flagged; `RESET` on failure.
- **B3 reconciler hook**: a converger renders expected config from `db_tuning_settings` each tick; if on-disk drop-in / `postgresql.auto.conf` diverges, re-apply + reload. Hand-edits don't survive; host rebuilt from a panel-DB backup keeps tuning; `applied_at` stays truthful. Mirrors nginx-file-per-vhost (ADR-0009 pattern).
- panel-api: `GET/PUT /api/v1/admin/databases/config?engine=` — PUT validates vs allowlist (422 unknown key / out-of-range), persists to `db_tuning_settings`, dispatches agent, audited, emits `db.admin.config_applied`.
- UI: per-engine `Form` (`InputNumber`/`Select`, units shown, restart-required keys badged), Apply with confirm copy: *"Saving may briefly restart MariaDB — every site's DB connections drop for a few seconds."*

### Step 4 — Admin all-DBs phpMyAdmin / Adminer SSO (feature #4)
- Reuse SSO token repo + sso.php / Adminer plugin (Step-0 patterns from `db_mysqladmin_ensure`, B2). New privileged path:
  - MariaDB: agent `db.pma_admin.ensure` creates `jabali_pma_admin@localhost` with `ALL PRIVILEGES ON *.*` (honest per ADR-0098), pw via tmp+rename secret file.
  - Postgres: Adminer as `postgres` superuser via existing peer/secret.
- panel-api: `POST /api/v1/admin/databases/sso/phpmyadmin` + `/sso/adminer`, `RequireAdmin`, **no per-DB ownership check**, same-origin CSRF + single-use short-TTL token retained, audit `scope=admin`.
- UI: "Open phpMyAdmin (all databases)" / "Open Adminer (all databases)" → new tab to returned redirect URL.

### Step 5 — Database Maintenance (feature #5) — InnoDB-honest (B6)
- **Not** called "Repair". `mysqlcheck --repair` is a no-op on InnoDB (≈all jabali DBs); promising "repair" repeats cPanel's misleading label scar.
- Agent `db.maintenance` (MariaDB): `mysqlcheck --optimize --analyze` (+ optional `--check`; `--auto-repair` only meaningfully runs for MyISAM/Aria — surface that in the summary, don't claim repair for InnoDB), root-over-socket, per-db or all. `db.postgres.maintenance`: per-db `REINDEX DATABASE` + `VACUUM (ANALYZE)` (NOT `VACUUM FULL` by default — lock risk; explicit opt-in flag only).
- Long-running → 202 + `db_admin_jobs` row; status via `GET …/maintenance/:id` (survives panel restart, B5). **B10**: a `POST …/maintenance` while a job for that engine is `running` → 409. Audited; emits `db.admin.maintenance_finished`.
- UI: "Maintenance (Optimize & Analyze)" section — engine + all/pick-DB + run, live status (poll pattern from `DatabasesCard.handleInstall`), last-run summary, honest InnoDB copy.

### Step 6 — Show Database Processes (feature #6)
- Agent `db.processlist` (MariaDB `SHOW FULL PROCESSLIST`, root socket) + `db.kill {id}` (`KILL <id>`). `db.postgres.activity` (`SELECT … FROM pg_stat_activity`) + `db.postgres.terminate {pid}` (`pg_terminate_backend`). No new `jabali_panel` privilege (ADR-0099).
- panel-api: `GET /api/v1/admin/databases/processes?engine=` (list envelope `{data,total,page,page_size}` — `feedback_verify_wire_contract`) + `POST …/processes/kill {engine,id}`. Kill audited (actor+target).
- UI: auto-refresh table (poll ~3s while tab visible), per-row kill behind `Popconfirm` using `RowDeleteButton`, `scroll={{x:"max-content"}}` (M23).

### Step 7 — Tests, E2E, docs
- Go: arg-sanitisation tests for every new agent command; repo sqlmock; handler tests (allowlist reject, RequireAdmin 403, audit + jobs rows written, 409 concurrent, MariaDB dual-`VIA` preserves socket — assert against the `SHOW CREATE USER` guard with a fake exec). `go test -race` clean; ≥80% on new code.
- Vitest: DatabasesCard sections render + mutation success/error.
- Playwright: admin → Databases tab; config form loads; processes table renders (mock agent). Run before declaring green.
- Runbook `plans/m46-database-server-admin-ops-runbook.md`: root-pw recovery, config rollback + the `db-config-broken.json` unrecoverable path, maintenance expectations (InnoDB), audit retention.
- ADRs 0096–0099 → Accepted; update `docs/adr/README.md`, `docs/BLUEPRINT.md` (table + section), `docs/ENV.md` (new files/vars), `CONVENTIONS.md` pointer row if `internal/dbtuning` is shared.
- Memory: `project_m46_database_admin_ops.md` + one MEMORY.md line (≤200 chars).

---

## 5. Conventions / scars honoured

- List envelope `{data,total,page,page_size}` (`feedback_verify_wire_contract`).
- Route family pattern; all routes `RequireAdmin`; nothing mounts off `v1` for the agent (no internal routes here).
- Reveal-once password response mirrors M7 `database_users.rotatePassword`.
- No raw config editor (lockout class bug — ADR-0097).
- Migration = schema only (`feedback_migration_data_seed_ordering`); `utf8mb4_unicode_ci` on FK-bearing CREATE TABLE (`feedback_mariadb_collation_fk`); avoid MariaDB 11.4+ reserved words in identifiers/aliases (`feedback_mariadb_reserved_words`); test migration on pinned MariaDB 11.x.
- Agent never opens outbound; all shell args sanitised; never echo mysql stderr.
- GORM params only; ULID PKs; `%w` wrap; `slog`; `set -e`/SIGPIPE-safe in any bash (`feedback_sigpipe_silent_exit`).
- Branch-only; whole milestone stays on `m46/...` until ship-ready (`feedback_no_partial_blueprint_to_main`); rebase `origin/main` + re-run tests before final report; dispatcher pushes (CLAUDE.md).
- Execute inline, no Agent/Task dispatch (`feedback_never_agents`).
- `npm run build` (not `tsc --noEmit`) before declaring UI green (`feedback_panel_ui_use_npm_run_build`).

## 6. Residual risks (carry into execution, not blockers)

1. MariaDB version skew: §2-B1 form is 10.4+; pinned targets are 11.4/11.8 — fine, but Step 1 still runs the `SHOW CREATE USER` guard so a surprise older box fails loud, not silent.
2. Admin PMA shadow = root-equivalent web access (ADR-0098 states this plainly). Accept; mitigation is the gating, not the grant.
3. InnoDB "repair" honesty handled by the Step 5 rename + summary copy.
4. Config restart blast radius: every site's DB connection drops on a MariaDB restart — UI copy says so; prefer `reload` where the key allows, `restart` only when required.
5. `db_tuning_settings` KV vs JSON columns on `server_settings`: KV chosen for engine×param sparsity + per-param `applied_at`/reconciler diffing. If Step 3 finds the converger simpler with a JSON blob, that's an allowed in-step deviation (note it in the ADR).
