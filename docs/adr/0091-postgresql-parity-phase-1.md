# 0091 — PostgreSQL feature parity, Phase 1 (M37 foundation)

**Status:** ACCEPTED
**Date:** 2026-05-08
**Amends:** ADR-0018 (M7 MariaDB-only Phase 1, Postgres deferred).
This ADR reverses that deferral — Phase 1 (foundation) ships now;
full parity (per-user CRUD, phpPgAdmin SSO, backup/restore, M14
events) follows in subsequent waves.

## Context

ADR-0018 deferred Postgres support during M7 to keep the MariaDB
single-engine surface tight. Issue tracker #96 (PostgreSQL feature
parity) and #38 (group PG with Databases nav) made the operator
ask explicit. M19 Applications also added a soft pull: Discourse,
Mastodon, Ghost are PG-required and currently can't install.

Two architectural shapes were considered:

- **A. Sibling table** — `postgres_databases` mirroring the
  `databases` shape. Pros: no risk of cross-engine queries
  short-circuiting on a missing column. Cons: every handler that
  lists "databases" has to UNION two tables; the engine boundary
  leaks into every read path.

- **B. Engine discriminator on `databases`** — `engine ENUM('mariadb',
  'postgres')` column already shipped in migration 000020 (a forward-
  thinking M7 schema decision; the column has been default `'mariadb'`
  since day one). Pros: every existing handler keeps working;
  per-engine dispatch is a single switch in the API layer. Cons:
  schema-level mixing of two engines in one table.

## Decision

**Take path B.** The discriminator already exists; building on it
preserves every existing query and keeps the M7 surface intact.

Phase 1 (this ADR) ships:

1. `install.sh install_postgres()` — apt-installs `postgresql-16` +
   `postgresql-contrib-16` + `postgresql-client-16` from Debian's
   archive. Loopback-only via
   `/etc/postgresql/16/main/conf.d/jabali.conf` drop-in.
   Service starts disabled — `server_settings.postgres_enabled`
   is the operator opt-in switch. Persisted superuser credential
   at `/etc/jabali-panel/postgres.password` (root:jabali 0640) so
   panel-api can read it without `sudo -u postgres`. The jabali
   user is enrolled in the postgres group at install time so the
   `/run/postgresql/.s.PGSQL.5432` socket is reachable via peer
   auth.

2. Migration `000111_server_settings_postgres.up.sql` —
   `postgres_enabled` (default `false`) +
   `postgres_max_connections_per_user` (default `25`).

3. **No new model — Engine field already exists.** `Database.Engine`
   in `panel-api/internal/models/database.go` was added in 000020
   alongside the table itself; this ADR makes that schema choice
   load-bearing instead of cosmetic.

## Out of scope (Phase 1)

Wave A (Steps 3-4) handles per-user database CRUD, phpPgAdmin SSO,
and the engine abstraction layer. Wave B (Steps 5-6) handles
backup + restore. Wave C (Steps 7-8) handles UI + monitoring. Wave
D (Step 9) ships the runbook + memory entry. Each Wave is a
separate ADR amendment if the design surface changes.

Permanently out of scope (per plan):

- Logical or physical replication
- Per-table grants (Phase 1 grants at database level only)
- Migration tooling between MariaDB ↔ Postgres
- pgAdmin4 (phpPgAdmin is the M22-pattern SSO target)

## Consequences

**Positive:**

- Shipped packages only (no PGDG repo) keeps the supply-chain
  surface unchanged. PostgreSQL 16 is the LTS in Debian 13 and
  matches MariaDB's "Debian-stock" stance.
- The `postgres_enabled` gate means existing hosts pay zero
  resident memory until they opt in. Operator can revert the
  install by setting the flag back to `false` and stopping the
  service — no cleanup of bespoke jabali tables required.
- Engine discriminator already in place means the API rewrite
  in Step 2 is a per-handler switch, not a schema migration.

**Negative / trade-offs:**

- Phase 1 is foundation only — operators who flip the gate before
  Wave A ships can install postgres-the-service but the panel UI
  still reflects MariaDB-only flows. Documented in the runbook
  step that ships with Wave D.
- Two engines in one table means a future "hard split" (separate
  engines, e.g. for unified-query reasons) is more work. Not
  expected — the engine boundary is consistently a per-row
  attribute, not a per-query mode.

## Implementation surface

| File | Change |
|---|---|
| `install.sh` | Add `install_postgres()` + wire into main() between Redis and PowerDNS |
| `panel-api/internal/db/migrations/000111_*.{up,down}.sql` | server_settings knobs |
| `docs/adr/0091-postgresql-parity-phase-1.md` | This ADR |

Wave A and beyond live in their own commits + memory entries.
