# ADR-0065: Server Status aggregator

**Status:** ACCEPTED (2026-04-25). M31 Steps 1–6 shipped.
**Related:** plans/m31-server-status.md.

## Context

Operators want one page to see the live state of a managed Jabali host:
hostname, kernel, uptime, load, CPU/mem/disk meters, per-service health,
network rates, top-N processes, pending updates, NTP, and a top-of-page
alert banner. The existing admin Dashboard surfaces a fraction of this
and hits multiple endpoints sequentially. M31 introduces a dedicated
page; this ADR documents the backend half.

## Decisions

### 1. Single REST aggregator endpoint

`GET /admin/server-status` returns the entire envelope in one shot. The
panel-api handler fans out to the agent in parallel using
`golang.org/x/sync/errgroup` and synthesizes the alerts before
responding.

Rejected alternatives:
- One REST per slice (host, cpu, network…). 5–8 round-trips per refresh
  multiplied by N admin tabs hammers the agent for no reason.
- WebSocket / SSE push. Server status is not a real-time feed; 5s
  polling is adequate and avoids long-lived connection state on the
  panel-api side.

### 2. Per-sub-call 5s timeout, hard cap of 8 in-flight

`g.SetLimit(maxInFlight=8)` + `context.WithTimeout(subCallTimeout=5s)`
on every sub-call. A slow `system.processes` (sorting top-N over 500
procs on a busy host) doesn't block the rest of the envelope: the slow
slice becomes `null` in the response and an `errors.processes` entry
explains why.

The aggregator never returns 5xx for sub-call failure — the envelope is
"best effort" by design. UI handles `null` slices with a "—" cell.

### 3. Reuse existing agent commands; add three more

| Command | Purpose | Status |
|---|---|---|
| `system.info` | hostname, OS, kernel, CPU model + count, mem, swap, partitions, uptime, load avg, NTP | extended (added OS/kernel/cpu_model/swap/NTPSynced) |
| `service.list` | systemd units allowlist + active/load_state/enabled | reused as-is |
| `system.network` | per-iface state + bps + pps + errors | new |
| `system.processes` | total/running/sleeping/zombie + top-N by RSS | new |
| `system.cpu_usage` | aggregate + per-core busy% + iowait%, delta-cached | new |
| `system.service_details` | per-unit memory/tasks/uptime via one `systemctl show` | new |

`system.network` and `system.cpu_usage` keep an in-memory previous
sample per agent process so they can compute rates from the next call's
counter delta. First call after agent boot returns `warming_up=true`
with zero rates so the UI doesn't render a misleading 0 B/s.

### 4. Unit allowlist enforced agent-side

`system.service_details` validates every requested unit against
`AllowedServices()` from `service_list.go`. A malformed panel-api
request can't introspect arbitrary systemd units even if the panel-api
side were compromised. Same boundary protects the existing service
lifecycle commands.

### 5. Alert synthesis lives panel-api-side, not agent-side

Threshold rules (disk > 80% warning, > 95% critical; load > cores × 2
warning; service inactive → critical) live in `synthesizeAlerts`. The
agent ships raw numbers; the panel decides what they mean. This keeps
threshold-tuning a one-place edit + lets the same agent serve multiple
panel versions with different rule sets.

Step 1 ships a minimal rule set; Step 4 will extend with service-
specific link-out alerts (e.g. "system updates pending" → deep link to
`/jabali-admin/updates`).

### 6. Per-call response carries `as_of` timestamp

`system.info` doesn't ship one (it's the canonical "now-state" call;
caller stamps the envelope with its own `as_of`). `system.network` and
`system.cpu_usage` DO carry their own `as_of` so the UI can detect a
sub-call that timed out vs. one that returned cached-but-stale data.
The envelope-level `as_of` is the panel-api's wall-clock at handler
start.

### 7. No DB writes; no migration

Pure read-through. State is in-memory in the agent (delta caches,
small) and ephemeral on the panel-api side. Survives an agent restart
gracefully — first call after restart sets `warming_up=true` and
recovers on the next.

## Consequences

- **Pro:** one round-trip per dashboard refresh.
- **Pro:** sub-call failures are visible (errors map + warning alerts)
  instead of hidden under a generic 500.
- **Pro:** allowlist + agent-side validation keep the audit surface
  small even though the page surfaces a lot of host detail.
- **Con:** 5s timeout is conservative. A genuinely overloaded host
  (load > 50, scanning /proc takes 6s) will keep flagging
  `processes: timeout` until pressure drops. Acceptable — the UI
  surfaces it as a warning rather than a hard failure.
- **Con:** delta-cache state lives in the agent process. Restart =
  one warming_up cycle (~5s on the next call). UI handles it.

## Verification

- `go test ./panel-agent/internal/commands/...` covers the parsers
  (`/proc/stat`, `/proc/net/dev`, `/proc/<pid>/stat,statm,status`,
  `systemctl show` output).
- `go test ./panel-api/internal/api/...` covers RBAC + happy-path +
  timeout-handling on the aggregator.
- Live-VM smoke deferred to Step 6 once the UI shell exists.

## Open

- **Queue stats** (mariadb / nginx / stalwart). UI ships placeholder
  card; aggregator extension lands in M31.1.
- **Pending-updates + crowdsec alerts** (currently surfaced via the
  Updates card; not synthesized into the AlertsBanner yet).
- **Historical charts** — out of scope (see plan §Out of scope).
