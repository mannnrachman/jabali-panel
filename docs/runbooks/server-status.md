# Server Status — runbook

Operator notes for `/jabali-admin/server-status` (M31, ADR-0065).

## What you see

| Section | Source | Refresh |
|---|---|---|
| CPU / Memory / Swap / Load meters | `system.cpu_usage` + `system.info` | 5s |
| Disks table | `system.info → partitions` | 5s |
| Network table | `system.network` | 5s |
| Services summary card | `system.service_details` | 5s |
| System Information card | `system.info` + `system.network` (first non-loopback IPv4) | 5s |
| User slices card | `system.user_slices` (cgroup v2 per-user) | 5s |
| Processes card | `system.processes` | 5s |
| Updates card | `system.update_check` + `system.apt_check` | manual |
| Queues card | not yet wired (placeholder, M31.1) | — |
| Alerts banner | `synthesizeAlerts` panel-api side | 5s |

Layout: AntD `<Masonry columns={{xs:1,sm:1,md:2,lg:3}}>` over an
items array. AntD Masonry only renders nodes from `items[].children`
— children passed between `<Masonry>` tags render as nothing (the
mistake that shipped a black page in the first cut). Order in the
items array determines visual order (column-first packing): meters
first (CPU/Memory/Swap/Load), then Disks/Network, then Services and
System Information, then User slices / Processes / Queues / Updates.

Polling pauses when the browser tab is hidden (`refetchIntervalInBackground:
false`). One idle admin tab = zero load on the agent.

## Threshold definitions

Hard-coded in `panel-api/internal/api/server_status.go`:

| Metric | Warning | Critical |
|---|---|---|
| Disk used | ≥ 80% | ≥ 95% |
| 1m load | > 1× cores | > 2× cores |
| Service `failed` | — | always |
| Service `inactive` + UnitFileState ∈ {enabled, enabled-runtime, static, alias} | — | always |
| Service `inactive` + UnitFileState `disabled` (lazy-started) | suppressed | suppressed |
| Sub-call failure | always | — (warning, not critical) |

Lazy-started services (e.g. `jabali-webmail` boots only on the first
domain.email_enable) stay disabled+inactive on hosts with no mail
domains; flagging them critical would paint a permanent red banner.
The aggregator reads `UnitFileState` from `systemctl show` and only
escalates inactive units when the operator expects them to be running.

Adjusting thresholds = one-line edit in `synthesizeAlerts`. No reboot
needed; `jabali update -f` rolls it out.

## Common scenarios

### "Service X is red"

The aggregator surfaces a critical alert when `ActiveState` is
`inactive` or `failed`. Investigate via SSH:

```bash
systemctl status <unit> --no-pager
journalctl -u <unit> -n 200 --no-pager
```

Service controls toggle on the page exposes Start / Restart / Stop
buttons via the agent. Each click hits a confirm popconfirm before the
action runs.

### "Network rates show — for 5–10 seconds"

First call after a `jabali-agent` restart has no prior `/proc/net/dev`
sample to delta against. The slice returns `warming_up: true` and the
UI renders "—" rather than 0 B/s. The next refresh has numbers.

### "Disks table shows nothing"

`system.info` probes a fixed mount list (`/, /home, /var, /tmp`) via
`syscall.Statfs`. If they all fail (chroot, fresh container) the
partitions array is empty. Check `journalctl -u jabali-agent` for
errors.

### "An agent sub-call timed out"

Each sub-call has a 5s deadline. A genuinely overloaded host (load >
50, slow `/proc` walk) will keep flagging `processes: timeout`. The
envelope still ships with whatever succeeded; the failed slice becomes
a warning alert.

To investigate:

```bash
ssh root@host
journalctl -u jabali-agent -n 200 --no-pager | grep -i timeout
```

If a sub-call is consistently slow on a healthy host, that's a bug —
file an issue.

### "NTP unsynced badge is orange"

`timedatectl show -p NTPSynchronized --value` returned non-`yes`.
Either `systemd-timesyncd.service` isn't running, or the host has a
stale clock. Fix:

```bash
systemctl start systemd-timesyncd
timedatectl set-ntp true
```

Wait a minute and refresh.

### "User slices card empty"

`system.user_slices` walks `/sys/fs/cgroup/jabali.slice/jabali-user.slice/`
for `jabali-user-*.slice` directories. Empty card means no per-user
slices have been provisioned (fresh host with no Linux users) OR the
M18 cgroup hierarchy didn't initialise. Verify:

```bash
ls /sys/fs/cgroup/jabali.slice/jabali-user.slice/
systemctl status jabali-user.slice
```

If the parent slice is missing, the agent returns an empty `slices`
array rather than erroring — the card renders "No per-user slices on
this host." rather than "—".

### "Service Restart vs Reload button"

Per-row action button defaults to **Restart**. nginx, pdns, and
pdns-recursor expose **Reload** instead — these accept `systemctl
reload` to re-read config without dropping in-flight connections.
The action allow-list lives in `ServicesSummaryCard.reloadCapable`;
unit names in that set route to the agent's `service.reload` command
(else `service.restart`). Both verbs are gated by the agent's
`isAllowedService` allow-list (`service_list.go`).

## Adding a new metric

1. Decide if it belongs in an existing slice (host / cpu / network /
   processes / services). If yes, extend the agent command's response
   struct + the matching field in `useServerStatus.ts`.
2. New top-level slice: add a new agent command, register in init(),
   wire into the aggregator's `errgroup` block in `server_status.go`.
   Each sub-call gets a 5s timeout and counts against the cap of 8.
3. Threshold rules go in `synthesizeAlerts`.
4. UI card lands under `panel-ui/src/shells/admin/server-status/`.

## Known limitations

- Per-process CPU% is omitted (deferred to a follow-up — needs two
  /proc/<pid>/stat samples and per-pid state in the agent).
- Queue stats card is a placeholder. MariaDB connections, nginx active
  conns, stalwart queue size all show "—" until the M31.1 extension
  lands.
- No historical graphs. Live numbers only. Long-term trends belong in
  a separate metrics store.

## Files

- Agent: `panel-agent/internal/commands/system_{network,processes,cpu_usage,service_details,user_slices}.go`, `service_{restart,reload,lifecycle}.go`
- panel-api: `panel-api/internal/api/server_status.go`, `admin_services.go`, `admin_counts.go`
- UI cards: `panel-ui/src/shells/admin/server-status/{ServerStatusPage,SystemInfoCard,ServicesSummaryCard,UserSlicesCard,MetersGrid,DisksTable,NetworkTable,ProcessesCard,QueuesCard,UpdatesCard,AlertsBanner}.tsx`
- UI hook: `panel-ui/src/hooks/useServerStatus.ts`
- ADR: `docs/adr/0065-server-status.md`
- Plan: `plans/m31-server-status.md`
