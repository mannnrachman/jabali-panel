# ADR-0019: M7 Databases — Per-database grants only, defer per-table/per-column

**Date:** 2026-04-17
**Status:** Accepted
**Deciders:** Shuki

## Context

M7's BLUEPRINT §6 spec calls out column-level verbs (SELECT/INSERT/UPDATE/DELETE/CREATE/ALTER/DROP). Full MariaDB grant granularity also covers per-table and per-column grants, each of which multiplies the grant matrix the UI must render and the revoke commands the agent must issue. Every control panel that ships fine-grained grants (cPanel, DirectAdmin) lives with a maintenance burden: forgotten column grants, drifted revokes, and user-support tickets. Jabali's immediate users are developers running CMSs and custom PHP — they virtually always want `ALL` on one database for the app user, plus optionally a read-only user for reporting.

## Decision

**Phase 1 of M7 exposes only per-database grants.** Each `database_user` has one `grant_level` per database it's attached to, chosen from a curated shortlist:

- `rw` — issued as `GRANT ALL PRIVILEGES ON \`<db>\`.* TO '<user>'@'localhost'`
- `ro` — issued as `GRANT SELECT ON \`<db>\`.* TO '<user>'@'localhost'`

The `database_user_grants` table is shaped to allow more rows per (user, db) pair in the future (per-table/column grants) without a migration, but the Phase-1 API and UI expose only the `rw`/`ro` toggle.

## Alternatives Considered

### Full per-verb grants (blueprint as-written)
- **Pros:** Matches the blueprint; maximum flexibility.
- **Cons:** Explodes the grant matrix (7 verbs × N databases × N users); UX becomes a checkbox grid; every grant change issues multiple GRANT/REVOKE statements.
- **Why not:** Complexity cost is high; actual users overwhelmingly want `rw` or `ro`.

### Per-table grants
- **Pros:** Supports multi-tenant apps that want to expose specific tables.
- **Cons:** Requires the panel to track table lists per DB and surface them in the UI; schema changes in user DBs would drift grants; revoke semantics are gnarly.
- **Why not:** Niche requirement; deferrable without blocking M10 (WordPress).

### Single implicit `ALL` with no grant model
- **Pros:** Simplest possible.
- **Cons:** Can't support RO users (common for reporting, backups, WordPress debug views); blocks legitimate WordPress-adjacent tooling.
- **Why not:** `ro` is cheap enough to include.

## Consequences

### Positive
- UI is a two-state radio per (user, db) pairing — fast to build, obvious to use.
- Agent commands `db_user.grant` / `db_user.revoke` take an enum, not a free-form verb list — smaller attack surface.
- Fuzz-testing the escaping and grant string generation is tractable.

### Negative
- Users who need column-level grants can't self-serve; must add the SQL manually via phpMyAdmin (still possible, but then drift from the panel's view of grants).
- `grant_level` change means drop-and-re-grant, not incremental — harmless but worth noting in the helper.

### Risks
- If a user adds raw grants via phpMyAdmin, the panel's `database_user_grants` table becomes stale. Mitigation: the grant table is descriptive (what the panel *tried to set*), not authoritative (what MariaDB actually has). Orphan-audit ticker (ADR forthcoming with Phase 5) can flag drift.
