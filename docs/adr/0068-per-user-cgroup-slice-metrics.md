# ADR-0068: Per-user slice metrics via direct cgroup v2 read

**Date**: 2026-04-26
**Status**: accepted
**Deciders**: shuki (operator) + assistant
**Related**: ADR-0032 (M18 resource limits, cgroup v2 hierarchy), ADR-0065 (server-status aggregator)

## Context

M18 (ADR-0032) provisions every Linux user the panel manages into
`/sys/fs/cgroup/jabali.slice/jabali-user.slice/jabali-user-<u>.slice`,
with cpu.weight / memory.max / pids.max from the user's package or
override. Operators have visibility into the *limits* via the package
editor but no visibility into actual *consumption* — "is alice eating
all the CPU?" required SSH'ing in and `systemd-cgtop`'ing by hand.

Server Status (ADR-0065) is the natural surface for this: it's already
polled every 5s, already authenticated as admin, already shells out
to the agent for every other host metric. The question is where the
data physically lives and how the agent collects it.

## Decision

Agent ships a new `system.user_slices` command that reads the cgroup
v2 sysfs files directly:
- `cpu.stat → usage_usec` (delta-cached against the previous sample
  for CPU%, mirroring the `system.cpu_usage` + `system.network`
  warming-up pattern)
- `memory.current` (current bytes)
- `memory.max` (limit; literal `"max"` returned for unlimited slices,
  coerced to 0 in the wire shape so the UI renders "—")
- `pids.current` (task count)

Aggregator gets a new `user_slices` slot in the envelope. UI renders
`UserSlicesCard` (User / CPU% / Memory[/limit] / Tasks). Empty slices
array on hosts with no Linux users; missing parent slice directory
returns empty, never errors.

## Alternatives Considered

### Alternative 1: Shell out to `systemd-cgtop --json`
- **Pros**: built-in, returns CPU% directly (no delta cache needed), human-readable
- **Cons**: requires a sampling window (CGTOP_DELAY) which forces a sleep inside the agent → conflicts with the 5s aggregator deadline; --json output format has changed across systemd versions
- **Why not**: blocking sleep + version coupling

### Alternative 2: Read via dbus to systemd Manager (GetUnit + GetCgroupProperties)
- **Pros**: typed API; survives systemd reorg
- **Cons**: another transport on top of UDS; per-slice round-trip multiplies latency; pulls in the godbus dependency
- **Why not**: round-trip cost; extra dep

### Alternative 3: Pull it on demand from Prometheus node_exporter
- **Pros**: zero new agent code
- **Cons**: requires deploying + pinning + securing node_exporter on every Jabali host; metrics scope creep
- **Why not**: separate-stack tax for one panel page

### Alternative 4: Push via `systemctl status jabali-user-<u>.slice` parsing
- **Pros**: pure CLI, no sysfs dependency
- **Cons**: fragile output format; one shellout per user (N+1); no usage-delta calculation
- **Why not**: parser brittleness + cost

## Consequences

### Positive
- One read pass per slice from a stable kernel interface (`/sys/fs/cgroup/...`)
- CPU% delta math reuses the agent's existing warming-up convention
- Card renders on any host with the M18 hierarchy; degrades cleanly elsewhere
- No new dependencies, no new daemons

### Negative
- CPU% is averaged over the wallclock interval between agent samples (5s under aggregator polling); short bursts get smoothed
- Per-slice IO stats deferred (io.stat schema is non-trivial across kernels)
- Per-process CPU% inside a slice still requires the deferred `/proc/<pid>/stat` work from ADR-0065 §Open

### Risks
- **Cgroup hierarchy reorg**: a future systemd / install.sh refactor that
  moves slices outside `/sys/fs/cgroup/jabali.slice/jabali-user.slice/`
  silently empties the card. Mitigation: keep `userSliceCgroupRoot` a
  package-level overridable var so a change has exactly one edit site.
- **Counter wraparound**: `cpu.stat usage_usec` is uint64 — won't wrap
  in any practical horizon, but the delta computation guards against
  `usage < prev.usage` (treats as 0%) so an agent restart mid-window
  doesn't produce a negative spike.
