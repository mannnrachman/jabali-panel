# M31 — Server Status Page

**Goal.** Dedicated admin page at `/jabali-admin/server-status` showing live system health: CPU / memory / disk / network / per-service health / queues / pending updates / NTP / kernel / load. Replaces the half-built health snippet on the existing admin Dashboard with a deep, polling, first-class page that mirrors what an operator would expect from WHM/Server Status or DirectAdmin/System Information.

The existing admin Dashboard (`panel-ui/src/shells/admin/Dashboard.tsx`, ~492 lines) stays as the landing page — it shrinks to a top-level health summary card (hostname, uptime, top alerts) with a "View server status →" deep link. All the deep meters and service grids move to the new page.

Branch: `m31/server-status`. Default: branch + ff-merge into main per step.

ADR target: **0065** (M30 was reserved 0065 but is parked; M31 reclaims it). If M30 unparks first, M31 takes 0066.

---

## Constraints + invariants

- **Read-only first.** v1 surfaces info. Restart/start/stop buttons next to each service row are gated behind "Show service controls" toggle (off by default) so a casual page view can't bring services down with a stray click. v2 (M31.1) can flip the default.
- **Polling, not push.** TanStack Query refetchInterval=5s while the tab is foreground; pauses when tab hidden (`window.visibilityState`). Servers rarely change health on a sub-5s cadence, and 5s × 1 admin = trivial agent load.
- **Aggregator endpoint, not N round-trips.** Single `/admin/server-status` REST returns the whole snapshot in one shot. Backend fans out to agent in parallel via `errgroup`. A single browser refresh = ONE agent call (which itself reads cgroup files inline; cheap).
- **No new agent commands if existing ones cover it.** `system.info` already returns hostname + OS + kernel + mem + uptime + load + disk; `service.list` returns systemd units. Add `system.network` (interfaces + RX/TX deltas) and `system.processes` (count + top-N by mem) if not present. Reuse everything else.
- **Network rates need a delta.** `cat /proc/net/dev` gives counters, not rates. The agent caches the last sample per interface in-memory; client poll → agent computes (current - last) / interval. First call after agent boot returns rates=0 and a `warming_up` flag.
- **Don't cross the user shell.** All routes are admin-only (`RequireAdmin`). Users see their own resource usage on `/dashboard` already; this page is operator-side only.
- **No DB writes.** Pure read-through. No migration needed.
- **Long-running ops stay where they live.** "Apply security update" / "Update jabali" buttons are NOT on this page — they live on `/jabali-admin/updates` (M29). This page only SURFACES that updates are available with a deep link.
- **Hard cap on concurrent agent calls per refresh.** Aggregator caps at 8 in-flight; if any sub-call exceeds 5s it's marked `timeout` in the response and the rest still render.
- **Time-stamping**: every metric carries a UTC `as_of` ISO8601 timestamp so the UI can show "X seconds ago" if a sub-call timed out.

---

## Steps

### Step 1: foundation — agent commands + REST aggregator

**Files:**
- `panel-agent/internal/commands/system_network.go` — new `system.network`
- `panel-agent/internal/commands/system_processes.go` — new `system.processes`
- `panel-agent/internal/commands/system_info.go` — extend if missing fields
- `panel-api/internal/api/server_status.go` — `GET /admin/server-status`
- `panel-api/internal/api/server_status_test.go`
- `docs/adr/0065-server-status.md`

**`system.network` returns** (per interface): name, state (UP/DOWN), MAC, IPv4 + IPv6 list, MTU, RX bytes/sec, TX bytes/sec, RX packets/sec, TX packets/sec, RX errors total, TX errors total. Loopback excluded by default; query param `include_loopback=true` overrides for debug.

**`system.processes` returns:** total count, running count, sleeping count, zombie count, top-10 by RSS (pid, comm, user, rss_kb, cpu_percent_avg_1s).

**REST `GET /admin/server-status` response shape:**
```json
{
  "as_of": "2026-04-25T12:00:00Z",
  "host": { "hostname": "...", "os": "...", "kernel": "...", "uptime_seconds": 12345, "load_1": 0.5, "load_5": 0.4, "load_15": 0.3, "ntp_synced": true, "timezone": "UTC" },
  "cpu": { "model": "...", "cores": 4, "usage_percent": 23.4, "per_core": [...], "iowait_percent": 0.5 },
  "memory": { "total_bytes": ..., "used_bytes": ..., "available_bytes": ..., "buffers_bytes": ..., "cached_bytes": ..., "swap_total_bytes": ..., "swap_used_bytes": ... },
  "disks": [
    { "mount": "/", "device": "/dev/sda1", "fs": "ext4", "total_bytes": ..., "used_bytes": ..., "inodes_total": ..., "inodes_used": ..., "quota_active": true }
  ],
  "network": [ { "iface": "eth0", "state": "UP", "ipv4": ["..."], "rx_bps": 1024, "tx_bps": 2048, "rx_errors": 0, "tx_errors": 0, "warming_up": false } ],
  "services": [
    { "unit": "jabali-panel.service",   "active": "active",   "sub": "running", "memory_bytes": 32000000, "tasks": 12, "uptime_seconds": 3600 },
    { "unit": "jabali-agent.service",   "active": "active",   "sub": "running", ... },
    { "unit": "mariadb.service",        ... },
    { "unit": "jabali-stalwart.service",... },
    { "unit": "jabali-kratos.service",  ... },
    { "unit": "redis-server.service",   ... },
    { "unit": "pdns.service",           ... },
    { "unit": "pdns-recursor.service",  ... },
    { "unit": "nginx.service",          ... },
    { "unit": "crowdsec.service",       ... },
    { "unit": "ufw.service",            ... }
  ],
  "processes": { "total": 250, "running": 3, "sleeping": 247, "zombie": 0, "top_by_rss": [ ... ] },
  "queues": {
    "mariadb_connections":    { "current": 5, "max": 151 },
    "nginx_active_connections": 12,
    "stalwart_mail_queue":      { "size": 3, "warming_up": false }
  },
  "alerts": [
    { "level": "warning", "kind": "disk", "detail": "/ at 85% used" },
    { "level": "critical", "kind": "service", "detail": "pdns-recursor inactive" }
  ],
  "updates": { "panel_behind": false, "system_packages_count": 4 },
  "crowdsec": { "active_decisions": 12, "alerts_24h": 47 }
}
```

**Backend fan-out:**
```
errgroup:
  agent.Call("system.info")
  agent.Call("system.network")
  agent.Call("system.processes")
  agent.Call("service.list", units=[...])
  mariaDB SELECT current_connections, max_connections
  agent.Call("nginx.connections") [or stub for now → 0]
  stalwart-cli queue stats [or stub]
  apt-status query (cached, 60s TTL)
  cscli decisions count (5s TTL)
```

Each sub-call gets a 5s timeout. Aggregator runs them in parallel, collects results, then synthesizes `alerts` from threshold checks (disk > 80% → warning, > 95% → critical; any service inactive → critical; load > cores × 2 → warning).

**Verification:**
- Mock agent + sample fixtures → REST returns full envelope < 200 ms.
- Slow `system.processes` (3s) → response still arrives within budget (5s timeout cap), processes block flagged `timeout: true`.
- All 11 services in fixture set returning various states → `alerts` correctly enumerates only the inactive ones.

---

### Step 2: UI page skeleton — `/jabali-admin/server-status`

**Files:**
- `panel-ui/src/shells/admin/server-status/ServerStatusPage.tsx` (page wrapper, polling logic)
- `panel-ui/src/shells/admin/server-status/HostHeaderCard.tsx` (hostname / OS / kernel / uptime / load + NTP badge)
- `panel-ui/src/shells/admin/server-status/MetersGrid.tsx` (4-up: CPU / Memory / Swap / Load)
- `panel-ui/src/App.tsx` — add route, sidebar nav entry

**Polling:** TanStack Query, `queryKey: ["server-status"]`, `refetchInterval: 5000`, `refetchIntervalInBackground: false` (pause when tab hidden).

**Header card (HostHeaderCard):**
- Left: hostname (bold), OS + kernel (secondary), uptime humanized ("3d 4h"), timezone
- Right: NTP-sync badge (green check / red x), as_of timestamp ("Updated 3s ago"), refresh button (forces refetch)

**Meters (MetersGrid):**
- CPU usage Progress bar (color-coded: <70 green, 70-90 yellow, >90 red), per-core sparkline below
- Memory Progress bar (used / total)
- Swap Progress bar (used / total) — hidden if total=0
- Load 1/5/15 averages with thresholds (yellow at cores×1, red at cores×2)

**Verification:**
- Renders against a frozen fixture envelope (Storybook-like test).
- Responsive: <md → 1 col stack; ≥md → 2-col; ≥lg → 4-col.

---

### Step 3: disk + network sections

**Files:**
- `panel-ui/src/shells/admin/server-status/DisksTable.tsx`
- `panel-ui/src/shells/admin/server-status/NetworkTable.tsx`

**DisksTable:** AntD Table with cols: Mount, Device, FS type, Used / Total (with Progress bar), Inodes used / total, Quota active (badge). One row per mountpoint. Threshold colors on usage %.

**NetworkTable:** AntD Table with cols: Interface, State (UP/DOWN badge), IPv4 + IPv6 (chips, copy-on-click), RX rate, TX rate, RX errors, TX errors. RX/TX rates animate via CountUp on each refetch. `warming_up: true` interfaces show a "—" instead of 0 to avoid misleading first-second readings.

**Verification:**
- Disk over 80%/95% rows render warning/critical tint.
- Network interface DOWN row stripes red.
- IP chips clickable → clipboard.

---

### Step 4: services grid + alerts banner

**Files:**
- `panel-ui/src/shells/admin/server-status/ServicesGrid.tsx`
- `panel-ui/src/shells/admin/server-status/AlertsBanner.tsx`

**ServicesGrid:** table with cols: Service (unit name + friendly label), Active (badge: active/inactive/failed), Sub (running/dead/exited), Memory (humanized bytes), Tasks, Uptime (humanized). One row per service in the response. Color: active+running = green; inactive/failed = red; activating/deactivating = yellow.

Bottom of grid: "Show service controls" toggle (default off). When on, each row gets Restart / Start / Stop buttons (gated by `RequireAdmin`; backend runs `service.restart` etc through agent). Confirm modal on every click.

**AlertsBanner:** sits at top of page above HostHeaderCard. AntD Alert per `alerts[]` entry. Critical = error, warning = warning. Empty = banner hidden. "Pending updates" alert links to `/jabali-admin/updates`. "CrowdSec ban-rate spike" alert links to `/jabali-admin/security`.

**Verification:**
- Stop a fixture service → alert appears + service row red.
- Toggle controls + click Restart → confirm modal → API hit (mocked) → row re-fetches.

---

### Step 5: queues + processes + updates panels

**Files:**
- `panel-ui/src/shells/admin/server-status/QueuesCard.tsx`
- `panel-ui/src/shells/admin/server-status/ProcessesCard.tsx`
- `panel-ui/src/shells/admin/server-status/UpdatesCard.tsx`

**QueuesCard:** three-row Card: MariaDB connections (X / Y, Progress bar), Nginx active connections (raw number + sparkline of last 12 samples = 60 s window), Stalwart mail queue size.

**ProcessesCard:** "Processes (250 total · 3 running · 0 zombie)" header; collapsible Top-10 by RSS table (PID / comm / user / RSS / CPU%). Default collapsed.

**UpdatesCard:** "X system updates pending" + "Panel: up to date" or "Panel update available". Each line is a deep link to `/jabali-admin/updates`. CrowdSec: "12 active decisions, 47 alerts in last 24h" → link to `/jabali-admin/security`.

**Verification:**
- Sparkline retains last 12 samples in memoized client state (no extra backend calls).
- Top-10 loads only when card is expanded (`enabled: expanded` on the query for the `system.processes` slice — if we split it).

---

### Step 6: trim Dashboard.tsx + e2e + docs

**Files:**
- `panel-ui/src/shells/admin/Dashboard.tsx` — shrink to a 1-screen landing card with: welcome line, top-level health badge (Healthy / Warnings: N / Critical: M), 3-stat strip (Users count, Domains count, Active mail accounts), prominent "View server status →" button
- `panel-ui/tests/e2e/server-status.spec.ts`
- `docs/adr/0065-server-status.md` — finalise, status=accepted
- `docs/runbooks/server-status.md` — what each metric means, threshold definitions, how to extend

**E2E spec:**
1. Sign in as admin → navigate to `/jabali-admin/server-status` → verify HostHeaderCard renders hostname.
2. Verify 4 meter cards render with non-zero values.
3. Stop `pdns-recursor` via SSH (test setup) → poll for 10 s → critical alert appears + service row red.
4. Restart it → poll for 10 s → alert clears.
5. Toggle "Show service controls" → Restart button visible → click → confirm modal → cancel → no API call.
6. Click "View server status →" deep link from `/jabali-admin` Dashboard → lands on this page.

**Exit criteria:**
- All 6 steps merged to main.
- Playwright suite green.
- ADR-0065 accepted.
- Runbook published.
- Dashboard.tsx ≤ 200 lines (trimmed from ~492).
- Bundle size delta ≤ 40 KB minified.

---

## Out of scope (defer to M31.1+)

- **Historical charts** (last 24h CPU/mem/disk graphs). Needs a metrics store (Prometheus / VictoriaMetrics / SQLite-on-disk). Belongs to M13 (stats & monitoring), not here.
- **Per-user resource breakdown.** That lives in `/jabali-admin/users` (UserSliceStatus already shows per-user mem + tasks + disk).
- **Alerting rules editor.** Threshold tuning (when does a disk go critical) deferred — hardcoded sane defaults in Step 1.
- **Service control buttons enabled by default.** v1 = read-mostly. v2 may flip the toggle default once we trust the audit trail.
- **Mobile-app push notifications on alerts.** M14 already has the dispatcher + notification UI; alerts on this page can fire those events but the design lives in M14 enhancements, not here.
- **External monitoring integration** (Datadog, Grafana, Prometheus exporter). M31.2 if at all.

---

## Risk register

| Risk | Mitigation |
|---|---|
| 5s polling × N admin tabs = agent load | `refetchIntervalInBackground: false`; one tab idle = 0 calls. Cap at 12 calls/min/admin via existing rate limit middleware. |
| Slow `system.processes` (sorting top-N over 500 procs) blocks the whole envelope | Per-sub-call 5s timeout; envelope still returns with the slow slice flagged `timeout: true`. |
| Race between in-flight refresh and tab close | Abort signal threaded through; cancelled queries don't update state. |
| Service controls foot-gun | Default toggle OFF; confirm modal; admin-only RBAC. |
| Network rates wrong on first sample after agent restart | `warming_up: true` flag → UI renders "—" instead of misleading 0. |
| Bundle bloat (CountUp + sparkline lib) | Use existing AntD Statistic for animated counts; sparkline = inline SVG <30 lines, no new dep. |
| User confused between Dashboard and Server Status | Dashboard explicitly says "Top-level summary — for live system status, see Server Status →". |

---

## Implementation order summary

```
Step 1 (agent + REST aggregator)
  ├─> Step 2 (page skeleton + meters)
  │     ├─> Step 3 (disks + network)
  │     ├─> Step 4 (services + alerts)
  │     └─> Step 5 (queues + processes + updates)
  │            ↓
  └────────> Step 6 (trim Dashboard + e2e + ADR + runbook)
```

Steps 3, 4, 5 can be parallel-dispatched after Step 2 lands (they're independent UI sections all reading the same envelope).

Total estimated commits: ~15-20.

Step 1 dispatchable.
