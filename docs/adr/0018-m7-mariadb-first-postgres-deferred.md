# ADR-0018: M7 Databases — MariaDB first, Postgres deferred

**Date:** 2026-04-17
**Status:** Accepted
**Deciders:** Shuki

## Context

M7 Databases (per BLUEPRINT.md §6) lists two engines: MariaDB and Postgres. Shipping both in Phase 1 doubles the agent-command surface, adds a second privileged service to install.sh's mandatory tier, and forces the panel to straddle two grant models with different semantics (MariaDB `GRANT ... ON db.*` vs. Postgres role-based ACLs). Jabali Panel's primary audience today is PHP + WordPress hosting; Postgres demand is real but secondary, and no immediate milestone (M10 WordPress, M11 FileBrowser) depends on it.

## Decision

**Phase 1 of M7 ships MariaDB support only.** The `databases.engine` column is retained as `ENUM('mariadb','postgres')` from day one, but only `'mariadb'` is accepted at the API. Postgres support is deferred to a later phase (no milestone number committed) and gated on install.sh bootstrapping Postgres as part of the mandatory tier.

## Alternatives Considered

### Ship both engines in Phase 1
- **Pros:** Blueprint alignment; no follow-up migration; signals cross-engine support early.
- **Cons:** Doubles agent command surface; install.sh must provision both services; grant semantics diverge and leak into the model layer; ~4h of extra work per phase for every remaining feature.
- **Why not:** Cost is linear in both scope and test burden for a capability with no downstream milestone depending on it.

### Ship MariaDB with no `engine` column (defer schema too)
- **Pros:** Smallest schema.
- **Cons:** Adding the column later requires a data migration and re-stamping every existing row; API clients must handle an engine value appearing mid-stream.
- **Why not:** The column costs nothing now and future-proofs the schema cheaply.

## Consequences

### Positive
- Phase 1 ships sooner; less agent surface to fuzz and stabilise.
- M10 (WordPress) can layer on M7's MariaDB primitives without waiting for Postgres design.
- One mental model for grants and quotas in Phase 1.

### Negative
- Postgres-first users (analytics-heavy, some WordPress variants) are locked out until the deferred phase.
- Two code paths eventually coexist — the `engine` column guarantees a switch statement is coming.

### Risks
- Demand for Postgres forces us to revisit before M10/M11. Mitigation: treat ADR-0018 as proposed-if-revisited; track requests in BLUEPRINT.md §6 without re-scoping M7.
