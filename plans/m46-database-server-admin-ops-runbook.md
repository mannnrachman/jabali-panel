# M46 — Database Server Admin Ops — Runbook

Operator procedures for the Server Settings ▸ Databases tab additions
(ADR-0097..0100). All six features are admin-only and audited in
`db_admin_audit`.

## Secret files (agent-written, `root:jabali` 0640)

| File | Written by | Read by |
|------|-----------|---------|
| `/etc/jabali-panel/mysql-root.password` | `db.root.set_password` | operator (break-glass `mysql -uroot -p`) |
| `/etc/jabali-panel/postgres.password` | install.sh + `db.postgres.superuser.set_password` | operator + Adminer admin SSO |
| `/etc/jabali-panel/pma-admin.password` | `db.pma_admin.ensure` | panel-api phpMyAdmin admin SSO validator |

These are NOT in the panel DB and never returned by any GET. Back them
up out-of-band if you rely on the break-glass passwords.

## 1. Root / superuser password

- **What it does NOT do:** it does not switch auth. MariaDB root keeps
  `unix_socket` (the statement is `IDENTIFIED VIA unix_socket OR
  mysql_native_password USING PASSWORD(...)`); Postgres `postgres`
  keeps `peer`. The panel/agent path is unchanged.
- The agent hard-asserts socket survival (`SHOW CREATE USER` contains
  `unix_socket` + a socket `SELECT 1`) and **reverts to socket-only**
  if anything is off, returning `failed_precondition`. install.sh:1660
  stays satisfied.
- Password shown **once**. Lost it? Just rotate again.

## 2. Per-database-user passwords

Unchanged by M46 — rotate on the Databases page (each DB user's
reveal-once “Password” action). The Databases tab notes this.

## 3. Configuration (curated tuner)

- Allowlist only (`internal/dbtuning`). DB (`db_tuning_settings`) is the
  source of truth; the reconciler re-applies any engine with
  `applied_at IS NULL` rows.
- MariaDB: managed `/etc/mysql/mariadb.conf.d/zz-jabali-tuning.cnf`
  (DO NOT hand-edit — the reconciler overwrites it). Apply path:
  validate → backup `.bak` → atomic swap → reload/restart → health
  probe → **auto-rollback** on failure.
- **Unrecoverable (B7):** if apply AND rollback both fail, the agent
  writes `/var/lib/jabali-agent/db-config-broken.json` and panel-api
  raises a **critical** M14 `db.admin.config_apply_failed_unrecoverable`.
  Recovery: inspect that file, fix/remove the drop-in by hand, restart
  mariadb, then clear the marker.
- A restart-required key bounces the service: every site's DB
  connections drop briefly (UI warns).

## 4. phpMyAdmin / Adminer (all databases)

- **This is a root-equivalent web shell.** Gated by RequireAdmin +
  same-origin + single-use short-TTL token + `scope=admin` audit, NOT
  by a trimmed grant (`jabali_pma_admin` = `ALL PRIVILEGES ON *.*`;
  Adminer = `postgres` superuser).
- Token carries `__M46_ADMIN_ALL__` as DatabaseID; the SSO validators
  branch on it BEFORE the per-user shadow path, so per-user SSO is
  byte-unchanged.
- **Live-VM smoke (required — no PMA/sockets in CI):** on the test VM,
  Server Settings ▸ Databases ▸ “Open phpMyAdmin (all MariaDB)” →
  lands logged in, all DBs visible. Repeat for Adminer/Postgres. Verify
  a `scope=admin` row in `db_admin_audit`. Confirm a normal per-user
  DB→phpMyAdmin SSO still works (regression check).

## 5. Maintenance (optimize & analyze)

- Honest naming: `mysqlcheck --repair` is a **no-op on InnoDB**. This
  runs `--optimize --analyze` (MariaDB) / `vacuumdb --analyze` +
  `reindexdb` (Postgres). It reclaims space + refreshes planner stats;
  it does NOT “repair” InnoDB.
- Async: `db_admin_jobs` row (survives `systemctl restart
  jabali-panel`); one job per engine (second request → 409).

## 6. Processes

- `information_schema.PROCESSLIST` / `pg_stat_activity`; KILL /
  `pg_terminate_backend`. Every kill audited (actor + pid); no M14
  (too noisy).

## Audit retention

`db_admin_audit` / `db_admin_jobs` grow unbounded. Prune >180 days
(operator cron, or fold into reconciler housekeeping later):

```sql
DELETE FROM db_admin_audit WHERE ts  < NOW() - INTERVAL 180 DAY;
DELETE FROM db_admin_jobs  WHERE started_at < NOW() - INTERVAL 180 DAY;
```

## CI vs live

Unit + race tests cover `internal/dbtuning` (pure) and every new agent
command's argument validation. The success paths exec
`mysql`/`mysqlcheck`/`psql` and the SSO handoff needs phpMyAdmin +
sockets — these are **live-VM smoke**, consistent with how SSO has
always been verified in this project (192.168.100.150).
