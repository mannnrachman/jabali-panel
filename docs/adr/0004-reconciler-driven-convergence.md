# 0004 — Reconciler-driven convergence

## Status
Accepted — 2026-04-16

## Context
After a database write, the filesystem (nginx, pdns, certbot) must be updated. This can happen inline (in the handler) or offline (periodic reconciliation). Offline reconciliation is more resilient: if the agent crashes mid-operation, the next loop will retry idempotently.

## Decision
A background goroutine (`internal/reconciler/`) runs every 60 seconds and on `Schedule(resourceID)` calls. It reads desired state from the DB and pushes convergent operations to the agent. The reconciler:
- Is idempotent (safe to run repeatedly)
- Is resumable (tracks serial numbers, cert renewal checks)
- Owns DNS serial bumps, SSL renewals, nginx regeneration, pdns metadata writes
- Reports drift (orphan files, missing resources)

Handlers commit the DB row first, then call `reconciler.Schedule(id)` (non-blocking).

## Consequences

### Positive
- Fault-tolerant: agent crash doesn't lose intent
- Single point of convergence logic (simpler debugging)
- Resumes automatically on restart
- Enables eventual consistency for slow operations

### Negative
- 60-second lag before filesystem reflects DB writes
- Orphan detection is log-only; no automatic cleanup (safety first)
- Requires stable serial bumps for DNS (extra complexity)

### Neutral
- Periodic work (DNS drift check, SSL renewal ticker) runs inside reconciler

## Alternatives considered

- **Per-handler direct agent calls**: Rejected — fragile under partial failure, no retry semantics
- **External worker daemon**: Rejected — extra unit, IPC overhead, harder to deploy

## References
- `panel-api/internal/reconciler/reconciler.go` — main loop
- `panel-api/internal/reconciler/domain.go` — domain convergence (DNS, nginx, certs)
- `./0002-database-source-of-truth.md` — database writes that trigger reconciliation
