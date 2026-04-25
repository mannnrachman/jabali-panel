# Server Status — runbook

Operator notes for `/jabali-admin/server-status` (M31, ADR-0065).

## What you see

| Section | Source | Refresh |
|---|---|---|
| Host header | `system.info` (agent) | 5s |
| CPU/Mem/Swap/Load meters | `system.cpu_usage` + `system.info` | 5s |
| Disks table | `system.info → partitions` | 5s |
| Network table | `system.network` | 5s |
| Services grid | `system.service_details` | 5s |
| Processes card | `system.processes` | 5s |
| Updates card | `system.update_check` + `system.apt_check` | manual |
| Queues card | not yet wired (placeholder, M31.1) | — |
| Alerts banner | `synthesizeAlerts` panel-api side | 5s |

Polling pauses when the browser tab is hidden (`refetchIntervalInBackground:
false`). One idle admin tab = zero load on the agent.

## Threshold definitions

Hard-coded in `panel-api/internal/api/server_status.go`:

| Metric | Warning | Critical |
|---|---|---|
| Disk used | ≥ 80% | ≥ 95% |
| 1m load | > 1× cores | > 2× cores |
| Service ActiveState | — | `inactive` or `failed` |
| Sub-call failure | always | — (warning, not critical) |

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

- Agent: `panel-agent/internal/commands/system_{network,processes,cpu_usage,service_details}.go`
- panel-api: `panel-api/internal/api/server_status.go`, `admin_services.go`
- UI: `panel-ui/src/shells/admin/server-status/*`, `panel-ui/src/hooks/useServerStatus.ts`
- ADR: `docs/adr/0065-server-status.md`
- Plan: `plans/m31-server-status.md`
