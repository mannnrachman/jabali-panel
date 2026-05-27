# Server Updates

`/jabali-admin/updates`. M29. Run `jabali update` from the panel UI as a transient systemd unit (ADR-0064).

## What an update does

1. `git fetch origin main`
2. `git reset --hard origin/main`
3. Rebuild `jabali-panel-api` and `jabali-agent` (Go).
4. `npm ci && npm run build` for `panel-ui`.
5. Apply pending database migrations.
6. Re-apply install-time host configurations the agent owns (nginx drop-ins, php-fpm pool directories, AppSec rules, FastCGI cache keyzone). The principle is "`install.sh` is truth, not runbook" — anything needed for a fresh host to work is also re-applied on every update.
7. Reload nginx, restart the panel API, restart the agent, restart Bulwark, in that order.
8. Report success or the first failure line.

## Why transient systemd units

If the update were to run inside the panel API itself, the panel restart in step 7 would kill the update process mid-step. The page would then look hung; the operator would have no idea whether the update succeeded.

Running each update as a `systemd-run --transient --unit=jabali-update-<timestamp>` job sidesteps this. The page subscribes to the unit's journal, so:

- The panel can restart itself mid-update without interrupting the update process.
- The operator can close and reopen the page; the live log reattaches.
- A failed update leaves a named unit (`jabali-update-2026-05-27-…`) that `journalctl -u <unit>` retains for the operator to inspect afterwards.

The transient-unit pattern was live-verified on 192.168.100.150.

## Update window

Server Settings → Updates → **Update window**. If set, `jabali update --auto` refuses to run outside the window. UI-initiated updates ignore the window (operator-driven, presumed deliberate).

## Common failure modes

| Symptom | Cause | Resolution |
|---|---|---|
| `git reset --hard` fails | Local edits to a tracked file. | Investigate via `git status` before discarding; once safe, retry. |
| `npm ci` ENOTEMPTY | Race condition cleaning `node_modules`. | Retry; second run succeeds. |
| `npm run build` exit 137 | Vite OOM on a small VM. | The installer caps `NODE_OPTIONS=--max-old-space-size` and auto-creates swap on first OOM. If it persists, increase VM RAM. |
| Migration `Dirty database version N` | A previous migration failed mid-flight. | `jabali migrate up` to retry. |
| Service fails to start in step 7 | Unit file shipped by the new commit references a path that does not exist yet. | `jabali repair --diagnose` will identify; usually a follow-up commit fixes within hours. |

## Post-update hint

On any failure, the panel prints a hint pointing at `jabali repair --diagnose`. The hint was added after recurring deploy scars where the operator needed `repair` next anyway.

## CLI

```bash
jabali update          # interactive
jabali update --auto   # unattended, respects update window
```
