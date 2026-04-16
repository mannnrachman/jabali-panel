# 0008 — Sibling repos are out-of-scope for panel

## Status
Accepted — 2026-04-16

## Context
The Jabali ecosystem includes several independent services: jabali-manager (central control plane), jabali-security (WAF daemon), jabali-isolator (nspawn container manager), jabali-tunnel (WSS sidecar), and Bulwark webmail (Next.js JMAP client). Each has its own release cadence and audience.

This decision enforces scope: this repo (jabali2) develops the panel only. Sibling repos are installed as optional addons but not developed here.

## Decision
`jabali-manager`, `jabali-security`, `jabali-isolator`, `jabali-tunnel`, and Bulwark webmail are separate repositories. The panel may define wire protocols (bridge endpoints, socket proxies) for integration but does NOT implement the sibling daemons. Their APIs are pinned via `~/projects/jabali-shared/CONTEXT.md`.

## Consequences

### Positive
- Clear repo boundaries; independent release cycles
- Reduced merge conflict and test surface
- Teams can own separate services independently
- Easier rollback (one service doesn't block another)

### Negative
- Inter-service API changes require coordination
- Multi-node features (manager, tunnel) are deferred
- Install complexity grows (optional addons)

### Neutral
- `install.sh` may invoke sibling installers (as subshells)

## Alternatives considered

- **Monorepo**: Rejected — couples release cycles, hard to scale teams
- **Vendored subtrees**: Rejected — merge hell and duplicate CI

## References
- `~/projects/jabali-shared/CONTEXT.md` — wire protocol pinning
- `install.sh` — addon installation order
