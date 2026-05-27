# Databases

MariaDB and PostgreSQL, per-user databases and DB-users, with SSO into phpMyAdmin / pgAdmin.

## Engines

- **MariaDB 11.x** — primary, installed by default. `skip-networking` on (M25.1) — clients connect via Unix socket only; no `tcp/3306`.
- **PostgreSQL** — installed alongside, off by default. Per-user DBs create on demand.

(Both are connected to the panel itself via Unix socket; the panel DB connection string is `unix:///run/mysqld/mysqld.sock`.)

## Per-user DBs

`/jabali-panel/databases`:

- **Create database** — pick the engine, name (`<user>_<suffix>` prefix enforced by `db_admin` policies), default DB user.
- **Create DB user** — username + password (shown once). The agent provisions the user with `GRANT ALL ON <user>_*.* TO …`.
- **phpMyAdmin SSO** — single-use, short-TTL **SSO Token** (CONTEXT.md). Click "Open phpMyAdmin" → land authenticated as the DB user.
- **pgAdmin SSO** — same flow for PostgreSQL.

## Admin DB Ops (M46)

`/jabali-admin/settings` → Database section:

- **Root password rotation** — agent-dispatched, audited. New password stored encrypted in `panel_db.db_admin_secrets`.
- **Curated config tune** — change a whitelisted set of MariaDB / PostgreSQL config keys (`innodb_buffer_pool_size`, `max_connections`, `query_cache_size`, etc., ADR-0098). The reconciler converges the change (`db_tuning_reconcile.go`) and the agent applies via `db.config.apply`.
- **Maintenance** — `OPTIMIZE TABLE`, `ANALYZE`, `CHECK`, `REPAIR` against selected DBs.
- **Processlist** — live processlist; one-click `KILL <id>`.
- **Admin phpMyAdmin SSO** — privileged shadow account (`__M46_ADMIN_ALL__` sentinel — see CONTEXT.md), full server access.

Every privileged DB action is a **Privileged DB Admin Action** in domain language: agent-dispatched, audited (success + failure), announced via M14 notifications.

## SSO Token Resolution

The single security-critical decision (`SSO Token Resolution`, CONTEXT.md): an `SSO Token` resolves to either

- the privileged shadow account (admin-scope, sentinel `__M46_ADMIN_ALL__`), **or**
- a per-DB-user scoped shadow account (ownership check passes).

The handler in `panel-api/internal/api/databases_admin_ops.go` is the adapter; the resolution module is what makes the call.

## Architecture

- **MariaDB skip-networking** (M25.1) — DBs are reachable only via socket. phpMyAdmin connects via socket; the panel connects via socket; user apps connect via socket.
- **Socket peer auth** for the panel itself — the `jabali` Linux user is the DB owner; no password needed for the panel's own connection.
- **Root password alongside socket peer auth** (ADR-0097) — root has a password (so `mysql -uroot -p` from a console still works) plus socket peer auth (so the panel never sends it).
- **Reconciler-converged tuning** — never edit `/etc/mysql/mariadb.conf.d/jabali.cnf` by hand; the reconciler will overwrite.

## CLI

```bash
jabali db list [--user <id>]
jabali db create --user <id> --name suffix
jabali db delete <id>
jabali db-user create --db <id> --username name
```
