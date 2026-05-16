# ADR-0100 — DB maintenance + processlist privilege model

**Date**: 2026-05-16
**Status**: accepted
**Deciders**: shuki (requested), Claude (design)
**Related**: ADR-0019 (per-database grants only), ADR-0050 (unix-socket model), ADR-0097 (root pw alongside socket), M46

## Context

M46 adds "Repair Databases" and "Show Database Processes". Two
questions: (1) what privilege runs them, and (2) is "Repair" an honest
label. `panel-api` connects as `jabali_panel`, which by design lacks
`PROCESS`/`SUPER`/superuser (ADR-0019: per-database grants only).
`mysqlcheck --repair` is a **no-op on InnoDB** — and ≈all jabali
databases are InnoDB. cPanel's "Repair MySQL Database" label is
misleading for exactly this reason; we should not inherit that scar.

## Decision

**Privilege model.** Maintenance, processlist, and KILL all run
**agent-side, root-over-socket (MariaDB) / postgres-over-peer
(Postgres)**. `jabali_panel` gains **no** new privilege — no migration
touches its grants. This keeps the `jabali_panel` blast radius minimal
(ADR-0019) and is symmetric with every other privileged DB op
(ADR-0097), at the cost of one extra agent round-trip per call
(acceptable; these are operator-initiated, not hot-path).

`KILL <id>` / `pg_terminate_backend(pid)` are audited with actor +
target id in `db_admin_audit`. Process-kill does **not** emit an M14
event (too noisy); root-pw rotate, config apply (ok/fail/unrecoverable),
and maintenance-finished do.

**Honest naming.** The feature is **"Maintenance (Optimize & Analyze)"**,
not "Repair":

- MariaDB: `mysqlcheck --optimize --analyze` (optional `--check`).
  `--auto-repair` only does anything for MyISAM/Aria; the result
  summary states this per-table rather than claiming "repaired" for
  InnoDB.
- Postgres: per-database `REINDEX DATABASE` + `VACUUM (ANALYZE)`.
  `VACUUM FULL` is **not** default (table-locking); explicit opt-in flag
  only.

**Durability + concurrency.** Long-running runs are tracked in
`db_admin_jobs` (survives `systemctl restart jabali-panel`, so a
mid-run page reload doesn't 404 its own job). A second maintenance
`POST` for an engine whose job is `running` returns **409**, enforced
against that table — never a parallel `mysqlcheck`.

## Alternatives considered

- **Grant `PROCESS` (+ optional `SUPER`) to `jabali_panel`.** Simpler
  (no agent hop) but widens the always-on service account's privilege
  for an operator-only feature. Rejected per ADR-0019 + symmetry with
  ADR-0097.
- **Keep the "Repair" label.** Familiar (cPanel parity) but actively
  misleading for InnoDB; rejected.
- **`VACUUM FULL` by default for Postgres.** Reclaims the most space
  but takes an `ACCESS EXCLUSIVE` lock; unacceptable as a default on a
  live hosting box.

## Consequences

- New agent commands: `db.maintenance`, `db.kill`, `db.processlist`
  (+ `db.postgres.*` twins).
- Operators see honest "optimize/analyze" wording; no false promise of
  InnoDB repair.
- `db_admin_jobs` rows accumulate; pruned with `db_admin_audit` by the
  >180d housekeeping pass (runbook).
