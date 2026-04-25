# M29 Admin Updates + Support — runbook

Operator notes for the `/jabali-admin/updates` and `/jabali-admin/support`
pages. ADR-0064 covers the diagnostic-encryption decisions.

## Architecture

```
 UI (System Updates page, Support page)
  │  GET /admin/updates/{jabali,apt}/check
  │  POST /admin/updates/{jabali,apt}/run
  │  GET /admin/updates/{jabali,apt}/status?since=…
  │  DELETE /admin/updates/{jabali,apt}
  │  POST /admin/support/diagnostic
  ▼
 panel-api (admin_updates.go, admin_support.go)
  │  thin proxy. RequireAdmin() middleware on every route.
  │  No state. Job state lives in systemd.
  ▼ agentwire over /run/jabali/agent.sock
 panel-agent (root)
  │  system.update_check    git fetch + rev-list
  │  system.update_run      systemd-run --unit=jabali-update-oneshot.service
  │  system.update_status   systemctl is-active + journalctl --since=…
  │  system.apt_check       apt-get update + apt list --upgradable
  │  system.apt_run         systemd-run --unit=jabali-apt-oneshot.service
  │  system.apt_status      systemctl + journalctl
  │  system.unit_stop       systemctl stop <allowlisted-unit>
  │  system.diagnostic_report  Collect + Redact + age.Encrypt
```

## Why transient systemd units, not agent children

`jabali update -f` ends with `→ restart services`, which restarts
`jabali-agent.service`. If the update were a child of `jabali-agent`
(via `cmd.Run()`), systemd would SIGTERM the entire cgroup at restart
time and the update would die mid-rebuild. Same for `apt-get
dist-upgrade` when it pulls in a libc / openssh / mariadb upgrade.

`systemd-run --unit=jabali-<kind>-oneshot.service --no-block` puts the
work in its OWN transient cgroup. The agent restart can't reach it. Job
status reads via `journalctl -u <unit>` — survives the bounce.

Trade-off: state is global to the host (one update at a time), not
per-session. That's correct for this feature: two concurrent
`jabali update` invocations would corrupt the build.

## Operator tasks

### Cancel a stuck update

Either via the UI ("Stop" button while the alert says "Update in progress")
or directly on the host:

```bash
ssh root@host
systemctl stop jabali-update-oneshot.service
# or for apt:
systemctl stop jabali-apt-oneshot.service
```

### Recover from a half-applied `apt dist-upgrade`

```bash
ssh root@host
journalctl -u jabali-apt-oneshot.service -n 200 --no-pager
dpkg --configure -a
apt-get -f install
```

### Pre-update snapshot checklist

Before clicking "Apply updates" on a production host, take a VM/disk
snapshot. The dist-upgrade may pull in libc, openssh, or mariadb minor
bumps that touch on-disk format. The runbook says it; the UI Alert
says it. Believe both.

### Read a diagnostic report (Jabali team only)

Operator emails `webmaster@jabali-panel.com` with the enclosed link
+ password. To decrypt:

1. Open the link in any browser. Page renders the enclosed UI.
2. Paste the password into the prompt. Enclosed decrypts client-side.
3. Download the `jabali-diagnostic-<host>-<ts>.tar` asset.
4. `tar -xvf jabali-diagnostic-*.tar` — service journals, host info,
   network listeners, package list. All entries are pre-redacted.

No private key custody, no CLI tools. Runs on any device with a
browser and the password.

### When the URL is missing or expired

Notes have a 7-day TTL on the enclosed deployment. If the link 404s,
ask the operator to re-run "Send Diagnostic Report" — fresh URL +
password. No state needs to be cleaned on the panel side; old reports
auto-rotate out of the enclosed store.

### Anti-phishing reminder

The enclosed URL is the only credential. Treat password leakage like
any other secret: never share in screenshots, never copy-paste in
public chat. The mail body is structured so the operator's mail
client is the natural transport — direct to inbox, encrypted in
transit by their MTA → ours.

## Troubleshooting

**`Check for updates` (jabali) returns 502.** Agent can't reach origin.
Check `journalctl -u jabali-agent` for `git fetch` errors. Often a
firewall rule on the host blocks `git.linux-hosting.co.il`.

**Apt check returns "package list" empty when `apt list --upgradable`
shows entries.** Locale issue — agent should set `LC_ALL=C` for every
apt invocation. Verify `panel-agent/internal/commands/system_apt_check.go`
sets it via `cmd.Env`.

**Diagnostic modal hangs.** Default panel-api timeout is 90 s. On a
busy host with 10 services × 200 lines of journal each, collection can
exceed this. Increase the timeout in `admin_support.go` or check the
agent's `systemctl status` calls aren't waiting on a hung dbus.

**"Stop" button doesn't kill the update.** systemd's TimeoutStopSec for
the transient unit defaults to 90 s. After that, systemd SIGKILLs.
Operator can short-circuit with `systemctl kill -s SIGKILL
jabali-update-oneshot.service` on the host.

## Files

- API: `panel-api/internal/api/admin_updates.go`, `admin_support.go`
- Agent commands: `panel-agent/internal/commands/system_*.go`
- Diagnostic engine: `panel-agent/internal/diagnostic/`
- UI: `panel-ui/src/shells/admin/updates/SystemUpdatesPage.tsx`,
  `panel-ui/src/shells/admin/support/SupportPage.tsx`
- Hooks: `panel-ui/src/hooks/useSystemUpdates.ts`, `useSupport.ts`
- Config: `panel-ui/src/config/support-links.ts` (placeholder URLs;
  swap to real before public release)
- ADR: `docs/adr/0064-diagnostic-report-age-encryption.md`

## Out of scope

- Selective package upgrade (only `dist-upgrade` is wired). If the
  operator wants to skip a noisy package, they SSH in.
- Multi-host orchestration (one agent per host).
- Support-plan signup flow — the buttons are external links only.
- Live-VM key custody — placeholder recipient ships with M29 + a
  follow-up swap is required before public release.
