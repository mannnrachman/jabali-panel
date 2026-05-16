# ADR-0098 — Curated, reconciler-converged DB config tuner

**Date**: 2026-05-16
**Status**: proposed
**Deciders**: shuki (requested + chose curated allowlist), Claude (design)
**Related**: ADR-0002 (DB is source of truth), ADR-0004 (reconciler convergence), ADR-0009 (nginx file-per-vhost), M46

## Context

cPanel/WHM "Edit Database Configuration" is a *curated* MySQL profile
tuner, not a raw `my.cnf` editor — for a hard reason: one typo in
`my.cnf` and `mariadbd` won't start; the panel then can't reach its own
database and there is no way to fix it from the UI. The same failure
class exists here. We need operator tuning without that lockout.

## Decision

**Allowlist only — no raw editor.** `internal/dbtuning/` holds a
per-engine registry: key name, engine, type, min/max, unit, default,
restart-required. Anything outside it is rejected with 422.

Curated keys (v1): MariaDB — `max_connections`,
`innodb_buffer_pool_size`, `innodb_log_file_size`, `max_allowed_packet`,
`table_open_cache`, `tmp_table_size`, `slow_query_log`,
`long_query_time` (version-gated `query_cache_*`). Postgres —
`max_connections`, `shared_buffers`, `work_mem`,
`maintenance_work_mem`, `effective_cache_size`, `wal_buffers`,
`random_page_cost`.

**DB is the source of truth.** Values persist in `db_tuning_settings`.
The **reconciler renders config from the table every tick** and reloads
only when the on-disk output diverges — identical to the
nginx-file-per-vhost pattern (ADR-0009). Consequences: hand-edits to the
managed files do not survive; a host rebuilt from a panel-DB backup
keeps its tuning; `applied_at` stays truthful.

- **MariaDB**: managed drop-in `/etc/mysql/mariadb.conf.d/zz-jabali-tuning.cnf`.
  Apply path: render candidate → validate
  (`mariadbd --defaults-file=<candidate> --validate-config`, fallback
  `mysqld --help --verbose` dry parse) → atomic-rename into place,
  keeping the prior file as `.bak` → `systemctl reload-or-restart
  mariadb` → post-restart health probe. **On probe failure: restore
  `.bak` + restart.**
- **PostgreSQL**: `ALTER SYSTEM SET <key>=<value>` (writes
  `postgresql.auto.conf`, reversible via `ALTER SYSTEM RESET`) →
  `SELECT pg_reload_conf()`; restart-required keys flagged in the UI.

**Rollback-of-rollback (mandatory).** If the restore-restart *also*
fails, the agent writes a one-shot marker
`/var/lib/jabali-agent/db-config-broken.json` and emits M14
`db.admin.config_apply_failed_unrecoverable` at **critical** severity,
so the operator hears it from the notification stack rather than from a
tenant ticket.

## Alternatives considered

- **Raw `my.cnf` textarea.** Maximum flexibility, maximum lockout risk;
  rejected for the reason cPanel curates.
- **Curated + advanced raw escape hatch.** Reintroduces the lockout
  class behind a confirm; rejected for v1 (revisit if a real need
  appears, behind its own ADR).
- **Apply-once (no reconciler).** Simpler, but violates ADR-0002/0004:
  hand-edits survive, backup-restored hosts silently lose tuning,
  `applied_at` lies. Rejected.
- **JSON blob on `server_settings` instead of a KV table.** Allowed
  in-step deviation if the converger is materially simpler that way;
  default is the KV table for per-param `applied_at` + diffing.

## Consequences

- A MariaDB restart drops every site's DB connections for a few
  seconds; the UI Apply confirm says so; prefer `reload` where a key
  allows it, `restart` only when required.
- Adding a tunable = one registry entry + tests; no schema change.
- The reconciler gains a DB-tuning converger (new, small, mirrors the
  nginx one).
