# Updates

M29. `/jabali-admin/updates`.

## What `jabali update` does

1. `git fetch origin main`
2. `git reset --hard origin/main` (the VM is treated as a deploy target; local edits get overwritten — see "VM local mods byte-identical to origin" scar for the rationale).
3. Rebuild `jabali-panel-api` and `jabali-agent` (Go).
4. `npm ci && npm run build` for `panel-ui` (Vite).
5. Run pending DB migrations.
6. Re-apply install-time host configs that the agent owns (nginx drop-ins, php-fpm pool dirs, AppSec rules, FastCGI cache keyzone). This is the "install.sh is truth, not runbook" rule — anything required for a fresh host to work right is re-applied on every update.
7. Reload nginx → restart panel-api → restart agent → restart Bulwark, in that order.
8. Print success or the first failure line.

## How the UI runs it

ADR-0064. Updates from the panel UI fire as **transient systemd units** (`systemd-run --transient --unit=jabali-update-<timestamp>`). This means:

- The update survives the panel restarting itself mid-update (step 7).
- You can watch the live log from a fresh page load even if the browser disconnected.
- `journalctl -u jabali-update-<timestamp>` is the audit trail.

The transient-unit survival was live-verified on 192.168.100.150.

## CLI

```bash
jabali update                # blocking, prints output
jabali update --auto         # for cron / CI; no prompts
```

If the update fails, `jabali update` prints a hint pointing at `jabali repair --diagnose` (M33 added the hint after a string of recurring deploy scars where the operator needed to run repair next anyway).

## What it does *not* update

- The kernel, system packages outside Jabali's drop-ins (use `apt update && apt full-upgrade`).
- PHP itself (use `apt`).
- MariaDB / PostgreSQL major version (manual; both data and config require operator decisions).

## Frequency

There's no auto-update timer enabled by default. The admin runs it manually or sets up their own systemd timer / cron.
