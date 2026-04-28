# M37 — PostgreSQL feature parity

**Goal.** Bring PostgreSQL into jabali2 as a first-class database engine
alongside MariaDB: per-user PG databases + grants, phpPgAdmin SSO,
backup/restore, monitoring tiles, migration importer integration.

This is the LARGEST single milestone proposed. It explicitly amends
ADR-0018 (M7 MariaDB-only Phase 1, Postgres deferred).

Old issue tracker requests:
- #96 PostgreSQL feature parity (the explicit ask, closed
  enhancement)
- #38 Group PostgreSQL with Databases side nav
- #91 Backup includes PostgreSQL databases (closed because backup
  itself shipped MariaDB-only)

Branch: `m37/postgresql-parity`. Default mode: branch + ff-merge into
`main` after every step.

ADR target: **0081** (next free after M34=0078, M35=0079, M36=0080).
Will amend ADR-0018 with a "M37 reverses the deferral" note.

Migration high-water-mark on main: 000091. M37 takes 000092..000095
(or shifts up if other milestones land first; renumber on merge).

## Why this is large

Every place jabali2 says "MariaDB" today needs to become "MariaDB OR
Postgres":

- M7 databases page (per-user CRUD) — picks engine on create
- M30 backup — system_backup must dump pg_dumpall + per-user pg_dump,
  account_full must dump per-user PG dbs
- M30.1 schedules — schedules tag includes engine
- M14 notifications — disk_full alert must check pg data dir too
- M27 CrowdSec — pg auth log scenarios (pg-bf, pg-probe) ship as
  optional collection
- M31 server-status — pg service tile + connection-count metric
- phpPgAdmin equivalent — phpPgAdmin OR pgAdmin4 OR a built-in JSON
  admin (pick one in Step 2 wave gate; phpPgAdmin is closest to
  the existing M7 phpMyAdmin SSO model)
- M22 SSO — extend to phpPgAdmin auth-bridge
- M19 Applications — Discourse / Mastodon / Ghost installers that
  REQUIRE Postgres become viable
- M35 migration importers — cPanel/WHM PG dump restore unblocks

## Constraints + invariants

- **PostgreSQL 16 LTS.** Debian trixie ships PG 16 in main archive.
  No PGDG repo for v1 (consistent with M7 MariaDB-from-Debian
  position).
- **One PG cluster per host.** Use the default Debian-shipped
  cluster `main` on port 5432. Do not run a second cluster.
- **Loopback only — same as M25.1 MariaDB.** PG bound to
  `127.0.0.1:5432` only; use `unix_socket_directories=/run/postgresql`
  (Debian default) for in-process connections from panel-api +
  agent. Listen on `127.0.0.1:5432` for phpPgAdmin proxy. NO
  `0.0.0.0` bind. Captured as ADR-0081.
- **Per-database role scope** mirrors M7 ADR-0019: per-database role
  ownership, with `pg_db_owner` + named login-roles per-database
  rw/ro level. NOT a single shared role per user.
- **Same systemd-run transient unit pattern** for backup/restore as
  M30 + M30.1.
- **Dump format = directory + custom (-Fd).** Faster restore +
  parallelizable. Plain SQL dumps are the fallback used only for
  cPanel/WHM-tarball compatibility in M35.
- **NEVER drop a PG cluster automatically.** install.sh probes for
  existing cluster; if `pg_lsclusters` shows any cluster on 5432,
  install.sh refuses to overwrite and prints the operator-side
  recovery command. Same defensive posture as the M7 MariaDB
  install.

## Wave gate (Step 2 = engine abstraction in M7 + apply-plan schema)

Step 1 = foundation: install PG via apt + DB schema + ADR. **Step 2**
is the wave gate: refactor M7's existing MariaDB-specific code into
an engine-agnostic layer, then add the PG implementation. Specifically:

- `internal/db/engine.go` interface (CreateDB, DropDB, CreateRole,
  GrantRole, RevokeRole, Dump, Restore)
- `internal/db/mariadb/` — extract from existing M7 code
- `internal/db/postgres/` — new

Steps 3-9 build on this abstraction; if Step 2 lands wrong, every
subsequent step has to be redone.

## Steps

### Step 1: foundation — install + DB schema + ADR-0081

**Files:**
- `install.sh`: install_postgres() — apt-install postgresql-16
  postgresql-contrib + pg_dumpall + pg_dump; provision
  `/etc/jabali-panel/postgres.password` (root:jabali 0600); enforce
  loopback bind via `pg_hba.conf` + `postgresql.conf` overrides
  under `/etc/postgresql/16/main/conf.d/jabali.conf`.
- `panel-api/internal/db/migrations/0000NN_create_postgres_dbs.up.sql`
  + `.down.sql` — `postgres_databases` table (mirrors `databases`
  shape but `engine='postgres'` discriminator)
- `panel-api/internal/db/migrations/0000NN_alter_databases_engine.up.sql`
  — add `engine ENUM('mariadb','postgres') NOT NULL DEFAULT 'mariadb'`
  to existing `databases` table; NOT default `postgres` to avoid
  accidental engine flip on existing rows
- `panel-api/internal/db/migrations/0000NN_server_settings_postgres.up.sql`
  — knobs: `postgres_enabled` (default false until operator
  switches), `postgres_max_connections_per_user` (default 25)
- `panel-api/internal/models/database.go` — add Engine field (or
  introduce postgres_databases sibling model)
- `docs/adr/0081-postgresql-parity.md` — amends ADR-0018

### Step 2: WAVE GATE — engine abstraction + apply-plan schema

**Files:**
- `internal/db/engine.go` — interface
- `internal/db/mariadb/` — extracted from existing M7 code; no
  behaviour change vs current state
- `internal/db/postgres/` — new
- `panel-api/internal/api/databases.go` — refactor every handler to
  dispatch by engine

Wave gate review: dispatcher confirms (a) interface signature,
(b) MariaDB extraction is byte-equivalent in behaviour to today,
(c) PG dispatch shape, before Wave A starts.

### Step 3 (Wave A): PG database CRUD agent commands

**Files:** `panel-agent/internal/commands/db_postgres_*.go`

Mirror existing MariaDB commands:
- `db.postgres.create_db`
- `db.postgres.drop_db`
- `db.postgres.create_role`
- `db.postgres.grant`
- `db.postgres.revoke`
- `db.postgres.list_dbs`

Connect via unix socket `/run/postgresql/.s.PGSQL.5432` as `postgres`
user (in jabali group via group membership added by install.sh).

### Step 4 (Wave A): phpPgAdmin SSO bridge

**Files:**
- `install.sh`: install_phppgadmin() — apt-install phppgadmin OR
  unpack a tarball if Debian's package is too out-of-date
- `panel-api/internal/api/phppgadmin_sso.go` — mirrors the M22 SSO
  pattern: short-lived signed token, redirect to phpPgAdmin's
  login_pre.php with the token, drop-in PHP file under
  /usr/share/phppgadmin/ validates via UDS

### Step 5 (Wave B): backup integration (M30 + M30.1)

**Files:**
- `panel-agent/internal/commands/backup_postgres.go` — pg_dumpall +
  per-user pg_dump (-Fd into /run/jabali-backup/<job>/postgres/)
- `panel-agent/internal/commands/backup_create.go` (existing) —
  invoke postgres dumper alongside mariadb dumper when
  postgres_enabled
- `panel-agent/internal/commands/backup_system.go` — system backup
  bundles every PG cluster's data + auth + role definitions

### Step 6 (Wave B): restore integration (M30 + M35)

- M30 restore round-trip: pg_restore from -Fd
- M35 cPanel/WHM tarball restore: detects PG dump in tarball, restores
  via pg_restore (re-points the existing `skipped: postgres_unsupported`
  manifest entries from M35 → properly imported)

### Step 7 (Wave C): UI integration

**Files:**
- `panel-ui/src/shells/admin/databases/DatabasesPage.tsx` — engine
  picker on create form
- `panel-ui/src/shells/admin/databases/DatabaseList.tsx` — engine
  badge column
- `panel-ui/src/shells/user/databases/UserDatabaseList.tsx` — same
- `panel-ui/src/nav.ts` — keep "Databases" nav entry (don't split
  by engine; #38 ask was to KEEP them grouped)

### Step 8 (Wave C): server-status + monitoring

- `panel-api/internal/serverstatus/postgres.go` — service tile + active
  connection count + replica lag (if replication is ever configured)
- M14 notifications: new EventKinds `postgres.service_down`,
  `postgres.disk_high`, `postgres.connections_exhausted`

### Step 9 (Wave D): runbook + E2E + memory entry

`plans/m37-postgresql-parity-runbook.md` covers:
- enabling postgres (operator flips server_settings.postgres_enabled
  + restarts panel-api)
- migrating existing MariaDB databases (NOT supported in v1; manual
  pg_loader)
- recovery scenarios (cluster crash, role file lost)
- known limitations (no per-table grants in v1; no replication;
  no logical replication for migration)

## Out of scope (acknowledged)

- PG replication (logical or physical)
- Per-table grants
- pgAdmin4 in addition to phpPgAdmin
- PG extensions catalog (operator manually enables what they need;
  no built-in extension manager)
- Multiple PG cluster support
- TimescaleDB / Citus / pgvector marketplace
- Cross-engine migration (MariaDB → Postgres or reverse)
