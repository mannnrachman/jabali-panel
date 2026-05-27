# Cron Jobs

`/jabali-panel/cron`. Scheduled commands that run under your account (M8).

## Adding a cron job

Click **Add cron job**, supply:

- **Name** — operator label.
- **Schedule** — 5-field cron (`min hour day month dow`). Examples: `0 3 * * *` (daily at 03:00), `*/15 * * * *` (every 15 minutes), `0 0 1 * *` (first day of each month at midnight).
- **Command** — must be on the allowlist (see below).

On save, the agent creates two systemd-user units in your home:

- `~/.config/systemd/user/jabali-cron-<id>.service` — the command.
- `~/.config/systemd/user/jabali-cron-<id>.timer` — the schedule.

Both are owned by you. The first time you add a cron job, systemd lingering is enabled for your account (`loginctl enable-linger <your-username>`) so timers fire even when you are not actively logged in.

## Command allowlist

The panel does not allow arbitrary shell commands. The allowlist includes:

- `php` (with any args)
- `wp` (WP-CLI; with any args)
- `python` / `python3`
- `node`
- `curl` (limited to your domains)

The allowlist is configurable by the operator; ask if you need a command not currently listed.

## Why systemd-user, not crontab

- Per-job journal logs (`journalctl --user -u jabali-cron-<id>`).
- `OnFailure=` triggers a notification through the M14 dispatcher.
- `RandomizedDelaySec=` provides natural jitter without adding `sleep $RANDOM` to your command.
- You do not need an interactive shell to manage your cron jobs (and you do not have one).

## Editing and deleting

Per-row **Edit** changes the schedule or command. **Delete** removes both unit files; the reconciler converges within 60 seconds.

## Running on demand

Per-row **Run now** triggers the service unit immediately, bypassing the timer. Useful for testing a new cron job without waiting for the next scheduled fire.

## Common cron patterns

- WordPress: `wp cron event run --due-now --url=https://example.com` every 15 minutes (replaces WP's built-in pseudo-cron, which fires only on page hits).
- Maintenance: a custom PHP script to clean up old uploads daily.
- Backups: a `mysqldump` to your own off-site target (this is on top of the panel's `account_full` backups).

## Failure handling

If a cron job exits non-zero, the `OnFailure=` hook fires the `cron_failed` notification event. You can route this to your in-app bell or email under Profile → Notifications.
