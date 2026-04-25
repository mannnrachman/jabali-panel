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

### Read a diagnostic ciphertext (Jabali team only)

Operators paste the output into a GitHub issue. To decrypt:

```bash
# private key is in the team password manager, NOT on any managed host
echo 'AGE-SECRET-KEY-…' > /tmp/jabali-team-priv.txt
chmod 600 /tmp/jabali-team-priv.txt
echo '<paste base64 ciphertext>' | base64 -d | age -d -i /tmp/jabali-team-priv.txt > /tmp/bundle.tar
tar -tvf /tmp/bundle.tar    # view contents
tar -xf /tmp/bundle.tar -C /tmp/diag/
shred -u /tmp/jabali-team-priv.txt
```

### Rotate the diagnostic recipient

1. Generate a new keypair off-host: `age-keygen -o new-priv.txt`. The
   pubkey is in the file header.
2. Save `new-priv.txt` to the team password manager. KEEP THE OLD ONE
   — old reports use the old key.
3. Edit `panel-agent/internal/diagnostic/recipient.go`, bump the
   `RecipientPublicKey` constant.
4. Run the test: `go test ./panel-agent/internal/diagnostic/...` —
   round-trip uses a generated identity, not the constant, so this
   doesn't break.
5. Cut a release; managed hosts pick up the new pubkey via
   `jabali update`.

The placeholder pubkey shipped with M29 (`age13trnrev8dmdva5tsj…`) MUST
be swapped before the public release. Tracked here.

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
