# M37 PostgreSQL parity — operator runbook

**Scope:** Day-to-day runbook for operators running jabali2 with the
PostgreSQL engine alongside MariaDB. Covers enabling, operating,
recovering, and known limitations. Not a getting-started guide for
PostgreSQL itself — assumes basic PG operator knowledge.

**Related:** ADR-0091 (PostgreSQL parity phase 1), `plans/m37-
postgresql-parity.md` (blueprint).

---

## 1. Enabling PostgreSQL

PG ships **disabled by default** on a fresh install. Default-off is
the safety gate: an operator who doesn't ask for PG never has a PG
service taking up RAM, never has the disk-quota event source firing
on /var/lib/postgresql, never gets the postgresql sub-tab cluttering
the admin UI's Database Users page.

To enable:

```sh
mariadb jabali_panel -e "UPDATE server_settings SET postgres_enabled=1"
systemctl enable --now postgresql
systemctl restart jabali-panel jabali-agent
```

Or via the admin UI: **Server Settings → Databases → Enable PostgreSQL**.
The toggle writes the same row + emits a notification through M14 so
the operator gets an audit-trail entry.

After enable:
- The `postgresql` service-list entry on Server Status flips from
  masked-grey to green.
- The Database Users page shows an "Engine" column with mariadb /
  postgres badges.
- The Quick Database Setup modal exposes an engine picker.
- M14 event source `eventsources/postgres.go` starts polling pg_stat
  every 2 minutes for service_down / disk_high (>85%) /
  connections_exhausted (>90% of max_connections).

## 2. Per-user PG database lifecycle

cPanel-style: each jabali user gets prefixed-name databases
(`<username>_<dbname>`). PG roles are created lazily — when a user
first creates a PG database, the agent runs:

```sh
sudo -u postgres createuser --no-superuser --no-createrole --no-createdb <username>
```

Subsequent grants (per user/database row) materialise via:

```sh
sudo -u postgres psql -c "GRANT ALL ON DATABASE <db> TO <role>"
```

The grant lifecycle mirrors the MariaDB shape (database_user_grants
table), with `engine='postgres'` distinguishing rows. The same UI
flows the operator already uses for MariaDB grants apply.

## 3. Backup + restore

PG dumps land in restic alongside MariaDB dumps via the M30 backup
pipeline. The agent's `backup.databases` stage detects engine=postgres
rows and runs `pg_dump -Fc` (custom-format binary) per database.
Restore (`backup.restore`) detects PG dumps via the `engine=postgres`
tag in the manifest and routes through `pg_restore --clean --if-exists
--no-owner`.

Limitations:
- No logical replication; restore is full-replace.
- No per-table grants — grants restore at the database level only.
- pgAdmin4 not shipped; phpPgAdmin via the existing Adminer SSO
  bridge is the operator interface.

## 4. Recovery scenarios

### 4.1 PG service crashed / won't start
1. `systemctl status postgresql` for the failure reason.
2. If WAL corruption: standard PG recovery via `pg_resetwal`.
3. If config drift: `jabali update` re-applies install.sh defaults.
4. M14 fires `postgres.service_down` after one tick with the down
   status; cooldown 30 min so a known-down service doesn't spam.

### 4.2 /var/lib/postgresql disk usage > 85%
1. M14 fires `postgres.disk_high`.
2. Investigate via `du -sh /var/lib/postgresql/*/*`.
3. Common cause: WAL not archiving — check `archive_command` in
   postgresql.conf.
4. Common cause: long-running transaction holding bloat — `SELECT *
   FROM pg_stat_activity WHERE state != 'idle' ORDER BY xact_start`.

### 4.3 Connection count near max_connections
1. M14 fires `postgres.connections_exhausted`.
2. `SELECT count(*), state FROM pg_stat_activity GROUP BY state`.
3. If too many idle: shorten `idle_in_transaction_session_timeout`.
4. If too many active: bump `max_connections` (requires restart) or
   add pgbouncer (operator-driven, not shipped).

## 5. Migrating MariaDB → PG

**Not supported in v1.** Per-engine schemas are too divergent to
auto-translate. Operator path:
1. `mysqldump --compatible=postgres` produces close-enough SQL.
2. Manual edits for type mismatches (TINYINT → SMALLINT, etc.).
3. Create the PG database via the panel UI.
4. `psql` import the cleaned dump.
5. Update the application's connection config.

The migration importer (M35) does NOT translate MariaDB dumps to
PG either; cross-engine data movement remains operator-driven.

## 6. Disabling PostgreSQL

If PG is no longer needed:

```sh
# 1. Drop every PG database first via the admin UI / CLI.
#    Leaving PG dbs around with the engine off causes orphaned rows.
jabali db list  # filter by engine=postgres
jabali db delete --id <ULID>  # repeat for every PG db

# 2. Flip the server setting.
mariadb jabali_panel -e "UPDATE server_settings SET postgres_enabled=0"

# 3. Stop + mask the service.
systemctl disable --now postgresql

# 4. Restart panel + agent so the event source bails on next tick.
systemctl restart jabali-panel jabali-agent
```

The data dir at `/var/lib/postgresql` is preserved — operator deletes
manually when ready.

## 7. Known limitations (acknowledged)

- **No PG replication** (logical or physical). Single-node only.
- **No per-table grants** — grants are database-scoped.
- **pgAdmin4 not shipped** — phpPgAdmin via Adminer SSO is the
  operator UI. pgAdmin4 has a heavier footprint + duplicates Adminer's
  feature surface.
- **No PostGIS / extension auto-install** — operator runs `CREATE
  EXTENSION` manually in the relevant database.
- **Migration tooling** — cPanel/DA tarballs containing PG dumps
  record `postgres_unsupported` in the manifest and skip; M37
  importer integration imports them in a follow-up pass once the
  operator confirms the PG database setup.

## 8. Where to look

| Concern | File / Location |
|---|---|
| Engine dispatch in REST | `panel-api/internal/dbops/dbops.go` |
| Engine dispatch in CLI | `panel-api/cmd/server/db_cmd.go` |
| Agent commands | `panel-agent/internal/commands/db_*.go` |
| Backup / restore branching | `panel-agent/internal/commands/backup_databases.go` + `backup_restore.go` |
| Event source | `panel-api/internal/eventsources/postgres.go` |
| Service-status entry | `panel-agent/internal/commands/service_list.go` |
| Adminer SSO | `panel-api/internal/api/adminer_sso.go` |
| install.sh PG bring-up | `install.sh:install_postgres()` |
