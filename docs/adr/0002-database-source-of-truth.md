# 0002 — Database is the source of truth

## Status
Accepted — 2026-04-16

## Context
A panel must track domains, zones, records, users, certificates, and cron jobs. These can live in the database or in on-disk configuration files. Historical systems (cPanel, HestiaCP, DA) often treat config files as primary, causing drift when the UI and CLI write to the same files out of order.

This decision enforces a unidirectional flow: database row → generated config file → service reload. Generated files are never edited by hand.

## Decision
All domain state (domains, zones, records, users, mailboxes, SSL certs, cron jobs, etc.) is stored as rows in MariaDB/Postgres. On-disk config files (nginx vhosts, pdns tables, certbot renewal hooks, systemd units) are GENERATED outputs of the database. Every feature requires:
1. Migration + GORM model
2. API CRUD handler
3. Reconciler path (periodic convergence)
4. Generator (template render to disk)

## Consequences

### Positive
- Single source of truth; no split-brain bugs
- Filesystem is rebuildable from database (disaster recovery)
- Reconciler can detect and repair drift
- Audit trail lives in DB rows (easier compliance)

### Negative
- Every feature requires 4 steps (migration, model, handler, generator); slower initial velocity
- File-based tools (direct nginx edit, pdns-sql-scripts) must route through API
- Migration mistakes can corrupt state; rollback must be explicit

### Neutral
- Requires transaction discipline to keep DB and FS in sync

## Alternatives considered

- **File-first (cPanel/DA style)**: Rejected — causes drift, hard to audit, recovery is manual
- **PowerDNS API as authoritative for DNS**: Rejected — creates two sources of truth; panel DB must own all domain metadata

## References
- `./0004-reconciler-driven-convergence.md` — documents the periodic reconciliation step
- `panel-api/internal/db/migrations/` — all schema changes
- `panel-api/internal/reconciler/` — convergence goroutine
