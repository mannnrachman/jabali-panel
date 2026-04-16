# 0006 — In-process worker, not separate daemon

## Status
Accepted — 2026-04-16

## Context
Background jobs (reconciler, DNS drift check, SSL renewal ticker, GoAccess tailer) need to run continuously. This can be a separate service (`jabali-worker.service`) or goroutines inside `panel-api`. In-process is simpler for small teams.

## Decision
Background jobs run as goroutines inside `panel-api`. No separate `jabali-worker.service`. The `internal/worker/` package contains:
- Reconciler loop (every 60s)
- DNS drift check ticker
- SSL renewal check (hourly)
- GoAccess log tailer (if applicable)

All started by `app.Run()` alongside the HTTP server.

## Consequences

### Positive
- Simpler install: one binary, one systemd unit
- Shared DB connection pool (no per-worker overhead)
- Shared in-memory config cache
- Easier debugging (single process)

### Negative
- Worker and API compete for CPU (less isolation)
- Restart panel to update jobs; no independent job scaling
- Memory usage grows with worker load

### Neutral
- Requires goroutine-safe DB pool and channel patterns

## Alternatives considered

- **Separate `jabali-worker.service`**: Rejected — extra unit file, IPC overhead, harder install
- **systemd timers for each job**: Rejected — loses idempotence, defeats reconciler design

## References
- `panel-api/internal/worker/` — job package
- `panel-api/cmd/panel-api/main.go` — app.Run() startup
