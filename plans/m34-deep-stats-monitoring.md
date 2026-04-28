# M34 — Deep stats & monitoring expansion

**Goal.** Move per-domain bandwidth, per-user cgroup samples, server load/mem/disk
samples, real-time access-log streaming, load-alert dispatch, and cgroup-reassign-on-
package-change from "we know it's missing" to "operators see it on a single page."

This is the work originally scoped as M13 in `docs/BLUEPRINT.md`. The number M13 was
reused (and shipped) for SSH shell sandbox via ADR-0067 / commit 127243d, so the
stats expansion takes a fresh number — **M34** — to avoid review-time confusion.
BLUEPRINT M13 row gets a redirect note when this lands.

Branch: `m34/deep-stats-monitoring`. Default mode: branch + ff-merge into `main`
after every step.

ADR target: **0078** (next free above ADR-0077 jabali-repair).

Migration high-water-mark on main: `000091_alter_backup_destinations_extra_options`.
M34 takes 000092..000094.

## Why now

What we have today (M31 ServerStatus + M14 notifications):
- Server-status pull: instantaneous CPU/mem/disk/services/load — but **no history**.
  Refresh the page and you see *now*; you can't ask "what did load look like at
  3 AM yesterday?"
- Per-user disk usage: live from `du -sb /home/<user>` per request. No memory or
  CPU samples, no traffic, no historical trend.
- Per-domain stats: zero. We do not track which domain consumed how much
  bandwidth.
- Alert wiring: M14 dispatch exists; only `cert_renew`/`disk_full`/`service_down`/
  `crowdsec_spike` event sources fire today. Server load/mem/disk thresholds do
  not generate notifications even though the data is there.

What this milestone adds:
- 60-second background samplers that persist server / per-user / per-domain
  metrics to MariaDB, with retention policies that age data out automatically.
- REST endpoints that expose those samples for the UI and any operator that
  wants to graph them in Grafana later.
- A real-time access-log streaming pane (GoAccess-WS) for ops who want
  *now* without polling.
- Load/mem/disk threshold alerts wired into M14.
- `package.update` hook that reapplies the user's cgroup limits — today a
  user's slice keeps its old `MemoryHigh=` until the operator manually
  intervenes.

## Constraints + invariants

- **MariaDB-only persistence.** No timeseries DB, no Prometheus pull. We have
  MariaDB; we have indexes; per-host volume is bounded. Retention timer trims
  rows nightly. If that ever stops scaling, we revisit — not before.
- **60-second sample cadence** for server + per-user + per-domain. Lower cadence
  buys nothing for the operator UI; higher cadence floods MariaDB and
  contributes nothing the panel uses.
- **Retention tiers (per-table):**
  - `bandwidth_samples`: 60s rows for 24 h, 5-min rollups for 30 days, hourly
    rollups for 365 days. Rollup pass runs in the same retention timer.
  - `per_user_resource_samples`: same tier as bandwidth.
  - `server_metrics_samples`: same tier.
  Rollup tables live alongside; the retention pass folds 60s rows into 5-min
  rows once they age past 24 h, then deletes the 60s rows. Same shape for
  5-min → hourly.
- **Samplers run as `jabali-agent` agent commands**, NOT as separate cron
  binaries. The agent is the one process with the right permissions to read
  /sys/fs/cgroup/jabali-user.slice/... and parse nginx access logs across
  vhosts; spawning sidecar daemons just to reproduce the same I/O is wasteful.
- **Per-vhost log_format** so the bandwidth aggregator can attribute bytes
  cleanly. M9.5 / M19 ship per-vhost configs already; the M34 step that wires
  the new log format is purely additive.
- **No Tetragon/eBPF for samplers.** Standard procfs + cgroup v2 reads. eBPF
  is reserved for malware (M33) where userspace polling cannot match its
  precision.
- **Load/mem/disk alerts are debounced.** A flapping load over the threshold
  must NOT page the operator every 60 s. M14 dispatch already supports
  per-rule debounce; M34 just defines the rule windows
  (load_15 > N for ≥ 5 min ⇒ one alert, then silence for ≥ 30 min).
- **GoAccess WebSocket runs only when the operator opens the page.** It is NOT
  a permanent daemon. Idle = no goaccess process. First admin to open the
  Metrics → Live tab spawns goaccess via systemd-run transient unit (per M29
  pattern); last admin closing the tab triggers a 5-minute idle teardown.
- **Per-domain bandwidth requires the access log to exist.** If a domain has
  log rotation or a custom log format that strips $bytes_sent, the aggregator
  records a row with `bytes_in=0 bytes_out=0` and a `manifest.warnings += [
  "log_unparseable"]`. UI badges those rows.
- **cgroup-reassign on package change** is idempotent. Apply current package
  limits to the user's slice via the existing reconciler hook;
  if the user's slice already has the same `MemoryHigh=`/`CPUWeight=`, this
  is a no-op.

## Wave gate (Step 2 = bandwidth aggregator + sample schema lock-in)

Step 1 lays foundation (DB tables + retention timer + ADR + sampler scaffolding).

**Step 2 is the wave gate** — it pins (a) the bandwidth-sample row shape,
(b) the per-vhost nginx log_format, (c) the rollup table shape, (d) the
agent command names and JSON envelopes. Steps 3-9 must not start before
Step 2 lands.

Wave A (3, 4): server-metrics sampler + per-user cgroup sampler. Run
in parallel — independent agent commands, independent tables.

Wave B (5, 6): REST API + GoAccess-WS deployment. Sequential.

Wave C (7, 8): admin UI + user UI. Sequential.

Wave D (9, 10): load alerts + cgroup-reassign. Sequential — alerts
land first because the rule wiring is small; cgroup-reassign is the
larger reconciler hook.

## Steps

### Step 1: foundation — DB schema, retention timer, ADR-0078

**Files:**
- `panel-api/internal/db/migrations/000092_create_metrics_samples.up.sql`
  (+ `.down.sql`)
- `panel-api/internal/db/migrations/000093_create_metrics_rollups.up.sql`
- `panel-api/internal/db/migrations/000094_server_settings_metrics_retention.up.sql`
- `panel-api/internal/models/metrics_sample.go`
- `panel-api/internal/repository/metrics_sample_repository.go` (+ tests)
- `install.sh`: provision `/etc/jabali-panel/metrics-retention.conf` + enable
  `jabali-metrics-retention.timer`
- `install/systemd/jabali-metrics-retention.service` + `.timer`
- `panel-api/cmd/server/metrics_retention_cmd.go` (cobra subcommand the
  service unit invokes)
- `docs/adr/0078-deep-stats-monitoring.md`

Tables (000092):
```sql
CREATE TABLE bandwidth_samples (
  id          BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  domain_id   CHAR(26) NOT NULL,
  ts          DATETIME(0) NOT NULL,
  bytes_in    BIGINT UNSIGNED NOT NULL DEFAULT 0,
  bytes_out   BIGINT UNSIGNED NOT NULL DEFAULT 0,
  PRIMARY KEY (id),
  KEY idx_bandwidth_domain_ts (domain_id, ts),
  KEY idx_bandwidth_ts (ts)
) ENGINE=InnoDB;

CREATE TABLE per_user_resource_samples (
  id          BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  user_id     CHAR(26) NOT NULL,
  ts          DATETIME(0) NOT NULL,
  cpu_pct     DECIMAL(5,2) NOT NULL DEFAULT 0,
  mem_kb      BIGINT UNSIGNED NOT NULL DEFAULT 0,
  io_read_kb  BIGINT UNSIGNED NOT NULL DEFAULT 0,
  io_write_kb BIGINT UNSIGNED NOT NULL DEFAULT 0,
  PRIMARY KEY (id),
  KEY idx_user_resource_user_ts (user_id, ts),
  KEY idx_user_resource_ts (ts)
) ENGINE=InnoDB;

CREATE TABLE server_metrics_samples (
  id            BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  ts            DATETIME(0) NOT NULL,
  load_1        DECIMAL(6,2) NOT NULL,
  load_5        DECIMAL(6,2) NOT NULL,
  load_15       DECIMAL(6,2) NOT NULL,
  mem_used_kb   BIGINT UNSIGNED NOT NULL,
  mem_total_kb  BIGINT UNSIGNED NOT NULL,
  disk_used_kb  BIGINT UNSIGNED NOT NULL,
  disk_total_kb BIGINT UNSIGNED NOT NULL,
  PRIMARY KEY (id),
  KEY idx_server_metrics_ts (ts)
) ENGINE=InnoDB;
```

Rollup tables (000093) mirror columns but key on `(window_start, …)` and add
`window_size ENUM('5min','1h')`. Retention pass:
- 60s rows older than 24 h → fold to 5-min rollups, delete originals.
- 5-min rollups older than 30 d → fold to 1-h rollups, delete originals.
- 1-h rollups older than 365 d → delete.

Tunable via `server_settings`:
- `metrics_retention_60s_hours` (default 24)
- `metrics_retention_5min_days` (default 30)
- `metrics_retention_1h_days` (default 365)

Server-settings additions (000094):
- `metrics_load_alert_threshold` (default 4.0 — load_15 above this triggers alert)
- `metrics_mem_alert_pct` (default 90)
- `metrics_disk_alert_pct` (default 90)
- `metrics_alert_silence_minutes` (default 30 — debounce window)

**Verify:**
- migration up + down on a throwaway DB
- `jabali-metrics-retention.timer` enabled + active after install
- one tick of the retention command on an empty DB exits 0 with no errors

**Out of scope this step:** any sampler running yet. Tables + timer only.

---

### Step 2: WAVE GATE — bandwidth aggregator + nginx log_format

**Files:**
- `panel-agent/internal/commands/metrics_bandwidth.go` (agent cmd
  `metrics.sample_bandwidth`)
- `panel-agent/internal/commands/metrics_bandwidth_test.go`
- `panel-api/internal/reconciler/metrics_bandwidth_tick.go` (60s tick that
  invokes the agent cmd and writes to `bandwidth_samples`)
- nginx vhost template update — `log_format jabali_v1` with `$bytes_sent`,
  `$body_bytes_sent`, `$request_length`, plus `$server_name`. Refresh existing
  vhosts on next reconciler pass.
- `install.sh`: ship `/etc/nginx/conf.d/jabali-log-format.conf` containing
  the `log_format jabali_v1 '...'` directive (loaded at http-context level so
  every vhost can `access_log ... jabali_v1`).

Implementation notes:
- Aggregator parses each `/var/log/nginx/access.<domain>.log` file in the
  60s window since the last sample. Reads the file offset from a small JSON
  manifest under `/var/lib/jabali-metrics/` so we don't double-count after
  panel-agent restart.
- For each line: extract `$server_name`, sum `$bytes_sent`/`$request_length`
  per (domain). One aggregated row per domain per 60s tick.
- nginx logrotate rotates daily; the manifest stores `(inode, offset)` so a
  rotation invalidates the offset and we restart at 0.

**Verify:**
- Curl-bombard a vhost with a known payload size (10 KB request, 20 KB
  response × 100 requests = 1 MB in + 2 MB out).
- After the next 60s tick, `bandwidth_samples` row for that domain shows
  bytes_in ≈ 1 048 576, bytes_out ≈ 2 097 152 (allow ±5 % for nginx framing).
- Logrotate the access log mid-stream; subsequent ticks must NOT show
  duplicate counts.

**Wave gate decision: dispatcher reviews this step before Wave A starts.**
Specifically: row shape, log_format directive, agent command JSON envelope,
manifest format under `/var/lib/jabali-metrics/`. Steps 3-9 build on
all four.

---

### Step 3 (Wave A): server-metrics sampler

**Files:**
- `panel-agent/internal/commands/metrics_server.go`
  (`metrics.sample_server`)
- `panel-agent/internal/commands/metrics_server_test.go`
- `panel-api/internal/reconciler/metrics_server_tick.go`

60s tick samples:
- `/proc/loadavg` → load_1/load_5/load_15
- `/proc/meminfo` → mem_used_kb, mem_total_kb (memtotal − memavailable)
- `df -B1 /` → disk_used_kb, disk_total_kb (root partition only; no
  per-mount split in v1)

Writes to `server_metrics_samples`.

**Verify:**
- After 5 ticks, `SELECT load_15 FROM server_metrics_samples ORDER BY ts
  DESC LIMIT 5` shows 5 rows with sane values matching `cat /proc/loadavg`.

---

### Step 4 (Wave A): per-user cgroup sampler

**Files:**
- `panel-agent/internal/commands/metrics_per_user.go`
  (`metrics.sample_per_user`)
- `panel-agent/internal/commands/metrics_per_user_test.go`
- `panel-api/internal/reconciler/metrics_per_user_tick.go`

Reuses M31's cgroup reader (`internal/cgroupread/`) to walk
`/sys/fs/cgroup/jabali-user.slice/*` and emit:
- cpu.stat → cpu_pct (delta over the tick window)
- memory.current → mem_kb
- io.stat → io_read_kb, io_write_kb (sum across all block devices)

Writes to `per_user_resource_samples`.

CPU percent calculation:
- read `cpu.stat.usage_usec` at sample t and t-1
- pct = (Δusage_usec / 1 000 000) / tick_seconds * 100 / num_cores

**Verify:**
- Create a user slice consuming 50 % of one core via stress-ng for 5 min.
- After 5 ticks, `cpu_pct` rows for that user_id average ≈ 50 / num_cores
  with ±5 % noise.

---

### Step 5 (Wave B): REST API surface

**Files:**
- `panel-api/internal/api/metrics_admin.go`
- `panel-api/internal/api/metrics_user.go`
- `panel-api/internal/app/app.go` (route wiring)

Endpoints:
- `GET /api/v1/admin/metrics/server?from=…&to=…` → server samples + rollups
- `GET /api/v1/admin/metrics/users/:id/usage?from=…&to=…` → per-user samples
- `GET /api/v1/admin/metrics/domains/:id/traffic?from=…&to=…` → per-domain
- `GET /api/v1/admin/metrics/domains?from=…&to=…&sort=bytes_out` → leaderboard
- `GET /api/v1/me/metrics/usage?from=…&to=…` → caller's own per-user samples
- `GET /api/v1/me/metrics/domains/:id/traffic?from=…&to=…` → caller's
  domain (404 if not theirs, no info-leak)

Time-window support: `from` / `to` ISO-8601, max range 90 d to bound
result-set; UI must paginate longer windows or use rollup endpoints.

**Verify:**
- Hit each endpoint with an empty DB → 200 + empty data array.
- Seed one row per table → 200 + that row appears in the right shell.
- Cross-user info-leak check: user A calls
  `/me/metrics/domains/<user-B-domain-id>/traffic` → 404.

---

### Step 6 (Wave B): GoAccess-WS deployment

**Files:**
- `install.sh` — install_goaccess(): apt-install goaccess, drop conf at
  `/etc/jabali-panel/goaccess/jabali.conf`, register as agent commands.
- `panel-agent/internal/commands/goaccess_start.go`
  (`goaccess.start_for_domain`)
- `panel-agent/internal/commands/goaccess_stop.go`
- `panel-agent/internal/commands/goaccess_idle_teardown.go` (called by
  reconciler when no admin tab has polled in 5 min)
- `panel-api/internal/api/goaccess.go`:
  - `POST /api/v1/admin/metrics/realtime/:domain_id/start` →
    transient-unit goaccess process with output to a named pipe / sock,
    panel-api proxies WebSocket connections to it.
  - websocket endpoint `/ws/admin/metrics/realtime/:domain_id`
- panel-ui consumer (Step 7).

GoAccess invocation (per domain, transient):
```
systemd-run --unit=jabali-goaccess-<domain-id>.service \
  goaccess /var/log/nginx/access.<domain>.log \
    --log-format=COMBINED --output=/var/lib/jabali-metrics/goaccess-<id>.html \
    --real-time-html --ws-url=wss://<panel-host>/ws/admin/metrics/realtime/<id> \
    --addr=127.0.0.1 --port=<dynamic-7000-7999>
```

**Verify:**
- Admin opens Metrics → Live → picks a domain → goaccess unit boots within
  5 s, real-time pane shows hits as `curl https://<domain>/x` is called.
- Close the tab, wait 5 min — goaccess unit is gone (`systemctl status` =
  unit not found).
- Two admins watching the same domain share one goaccess unit (refcount in
  panel-api).

---

### Step 7 (Wave C): admin UI

**Files:**
- `panel-ui/src/shells/admin/metrics/MetricsPage.tsx`
- `panel-ui/src/shells/admin/metrics/ServerMetricsCard.tsx`
- `panel-ui/src/shells/admin/metrics/UsersUsageTable.tsx`
- `panel-ui/src/shells/admin/metrics/DomainsTrafficTable.tsx`
- `panel-ui/src/shells/admin/metrics/RealtimeTab.tsx` (GoAccess iframe +
  WebSocket gauge)
- `panel-ui/src/nav.ts` (sidebar entry)
- `panel-ui/src/App.tsx` (route)

Layout (single page with left-tabs):
- Overview — server load/mem/disk last-24h sparklines + current values
- Users — table of users sorted by mem | cpu | io, sparklines per row
- Domains — table of domains sorted by bytes_out | bytes_in,
  sparklines per row
- Live — domain picker + real-time GoAccess pane

Widgets reuse AntD Charts (already a dep via M31) — sparklines as
inline `<Line>` with no axes.

**Verify:**
- Empty DB → table shows "No samples yet" placeholder per tab.
- Seeded DB → sparklines render, sort columns work, hover shows precise
  value + timestamp.

---

### Step 8 (Wave C): user UI — expand /jabali-panel/usage

**Files:**
- `panel-ui/src/shells/user/MyProfileUsageCard.tsx` (existing — extend)
- `panel-ui/src/shells/user/UserDomainTrafficCard.tsx` (new)
- `panel-ui/src/shells/user/UserDomainList.tsx` (add a sparkline column)

Add to the existing Resource usage card a 24 h trend sparkline for cpu_pct
and mem_kb. Add a new card to MyProfile: per-domain bandwidth last 24 h.

**Verify:**
- A user with no traffic sees "No traffic yet" placeholder, not an empty
  chart.
- A user with traffic sees one sparkline per domain, summed to a "total
  bandwidth used" header.

---

### Step 9 (Wave D): load / mem / disk alerts → M14

**Files:**
- `panel-api/internal/notifications/sources/server_metrics_alerts.go`
  (new event source)
- `panel-api/internal/db/migrations/000095_server_metrics_alert_state.up.sql`
  (state table for debounce — last_fired_at per (rule, host))
- `panel-api/internal/notifications/eventkinds.go` — three new EventKinds:
  `server.load_high`, `server.mem_high`, `server.disk_high`

Rule:
- After every server-metrics tick (Step 3), evaluate the three thresholds
  from server_settings.
- For each threshold breached, look up `last_fired_at` for that rule.
- If null OR (now − last_fired_at) > silence_minutes: fire the M14 event,
  write last_fired_at = now.
- Otherwise: silent (debounced).
- When the breach clears (value back below threshold), fire a `*.cleared`
  follow-up M14 event so the operator knows it's resolved.

**Verify:**
- Set load threshold to 0.01, run `stress-ng -c 1 --timeout 60s` → fire
  `server.load_high` notification within ~1-2 ticks.
- Subsequent ticks within the silence window do NOT re-fire.
- After the silence window passes and load is still high, re-fire.

---

### Step 10 (Wave D): cgroup-reassign on package change

**Files:**
- `panel-api/internal/api/packages.go` (existing — hook on package update)
- `panel-api/internal/reconciler/cgroup_reassign.go` (new)
- `panel-agent/internal/commands/slice_apply_limits.go` (existing — verify
  idempotent; if not, fold the diff)

When admin updates a user's package OR moves a user between packages,
the reconciler tick after the change applies the new package's
`MemoryHigh=` / `CPUWeight=` / `IOWeight=` to the user's slice via the
existing `slice.apply_limits` agent command.

Idempotency rule: if the running slice config already matches, the
agent command is a no-op (no `systemctl set-property` call).

**Verify:**
- User A on package P1 (MemoryHigh=512M).
- Admin moves user A → package P2 (MemoryHigh=1G).
- Within one reconciler tick, `systemctl show user-<uid>.slice -p
  MemoryHigh` reports 1G.
- Re-running the reconciler tick a second time logs no
  `set-property` call (idempotent).

---

### Step 11: runbook + E2E + memory entry

**Files:**
- `plans/m34-deep-stats-monitoring-runbook.md`
- `panel-ui/tests/e2e/metrics-admin.spec.ts`
- `panel-ui/tests/e2e/metrics-user.spec.ts`
- memory: `project_m34_deep_stats_shipped.md` (after merge)

Runbook covers:
- where samples live (which table, which retention tier, how to query)
- how to bump the alert thresholds
- how to manually re-run the retention pass
- how to debug GoAccess-WS not connecting (units, ports, panel-api proxy)
- recovery: dropping all metrics tables and rebooting the samplers from
  zero — the sampler manifest under `/var/lib/jabali-metrics/` must be
  cleaned in lockstep.

E2E spec coverage:
- admin Metrics Overview tab renders with seeded data
- admin Users tab sort + filter
- admin Domains tab leaderboard sort
- user MyProfile shows usage card + per-domain traffic
- cross-user info-leak: user B cannot fetch user A's per-domain traffic

---

## Out of scope

- Predictive forecasting / anomaly detection (would need a real timeseries
  DB; revisit when MariaDB stops scaling).
- Grafana export — admin can already query MariaDB; if they want Grafana,
  they wire it themselves. Native dashboard could land as M34.1 later.
- Alert escalation rules — M14 ships flat dispatch; tiered escalation
  (page admin then on-call after 15 min) is M14.x territory.
- Per-mount disk samples — root partition only in v1.
- Per-process top-talkers within a user slice (would need eBPF; reserved
  for malware in M33).
- Sampling of non-jabali processes / system services. Server-metrics
  step (Step 3) is host-wide; per-user step (Step 4) is jabali-user.slice
  only.

---

## Numbering note

`docs/BLUEPRINT.md` line 884 still says "M13: Stats & monitoring (PLANNED)".
The number M13 was reused (and shipped) for SSH shell sandbox via ADR-0067 /
commit 127243d. This work ships under **M34** to avoid review-time
confusion; the BLUEPRINT M13 row gets a one-line redirect when this
milestone lands ("see plans/m34-deep-stats-monitoring.md — number reassigned
to M34 because M13 was reused for the SSH sandbox milestone").
