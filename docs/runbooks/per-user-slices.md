# Per-user systemd slices — operator runbook

Every hosting user on a Jabali box runs inside a nested systemd slice:

```
jabali.slice                       (cgroup root for the panel)
└─ jabali-user.slice               (shared memory cap — 80% of host RAM)
   └─ jabali-user-<user>.slice     (per-user container)
      └─ jabali-fpm@<user>.service (PHP-FPM master running as <user>)
         └─ pool worker(s)
```

See [ADR-0025](../adr/0025-per-user-systemd-slices.md) for the design rationale.

## What's captured

These processes end up inside `jabali-user-<user>.slice`, so CPU/memory
accounting, cgroup limits, and slice-wide kills work:

- **PHP-FPM master + workers** — started by `jabali-fpm@<user>.service`,
  placed in the slice via the unit drop-in at
  `/etc/systemd/system/jabali-fpm@<user>.service.d/slice.conf`.
- **Interactive shells** — SSH logins, `su <user>`, `sudo -u <user>`.
  Captured because `user.slice.ensure` enables linger and writes a drop-in
  at `/etc/systemd/system/user@<uid>.service.d/jabali.conf` that sets
  `Slice=jabali-user-<user>.slice` on the user's login manager.
- **systemd-user timers** — anything the user schedules via
  `systemctl --user ...`. Runs inside the user manager, which is inside
  the slice.

## What's NOT captured

- **Traditional crontabs** — lines under `/var/spool/cron/crontabs/<user>`
  are executed by `cron.service` (part of `system.slice`), not by the
  user's manager. They run as `<user>` for permissions but escape the
  slice hierarchy, so their memory/CPU usage counts against system.slice
  instead.
- **CGI / mod_php processes** — Jabali doesn't run these, but if you bolt
  on Apache or a custom runtime that forks under a non-systemd parent,
  those children land wherever the parent is.

## Converting a user's crontab to systemd-user timers

One-time per user; do this when you need the cron job accounted under
the slice (e.g. because cron is stealing RAM during a busy backup run).

1. As root, print the existing crontab:
   ```
   crontab -u <user> -l
   ```
2. For each line `M H DOM MON DOW command`, create two files in
   `/home/<user>/.config/systemd/user/`:
   - **Service:** `mytask.service`
     ```
     [Unit]
     Description=<what the job does>

     [Service]
     Type=oneshot
     ExecStart=/bin/sh -c '<command>'
     ```
   - **Timer:** `mytask.timer`
     ```
     [Unit]
     Description=Run mytask on schedule

     [Timer]
     OnCalendar=<cron → calendar, see systemd.time(7)>
     Persistent=true

     [Install]
     WantedBy=timers.target
     ```
3. As the user, enable it:
   ```
   sudo -iu <user> systemctl --user daemon-reload
   sudo -iu <user> systemctl --user enable --now mytask.timer
   ```
4. Remove the cron line:
   ```
   crontab -u <user> -r     # if it was the only line
   ```

Common cron → calendar translations:

| cron                  | `OnCalendar=`                |
|-----------------------|------------------------------|
| `0 * * * *`           | `hourly` (or `*:00:00`)      |
| `0 0 * * *`           | `daily`                      |
| `0 0 * * 0`           | `weekly`                     |
| `*/5 * * * *`         | `*:0/5`                      |
| `0 3 1 * *`           | `*-*-01 03:00:00`            |

## Troubleshooting

### `systemctl is-active jabali-fpm@<user>.service` → `activating (auto-restart)`

Check `journalctl -u jabali-fpm@<user>.service -n 30`. Common causes:

- **`Permission denied` writing `/var/log/php-fpm-<user>.log`** —
  `fpm-pre-start` should pre-create + chown this. Re-run
  `jabali-panel update` to re-sync the shim, then restart the service.
- **`unable to bind listening socket` on `/run/php/jabali-<user>/fpm.sock`** —
  directory missing. `fpm-pre-start` creates it; again, re-sync the shim.
- **`failed to chown() the socket`** — the hosting user is not in the
  `www-data` group. Run `usermod -aG www-data <user>` and restart.
  `user.slice.ensure` now does this automatically.

### `loginctl user-status <user>` says `Linger: no`

`user.slice.ensure` should call `loginctl enable-linger <user>`. If it
doesn't stick after a full reconcile, check
`/var/lib/systemd/linger/<user>` — that's the marker file. Create it
manually as root if needed:

```
touch /var/lib/systemd/linger/<user>
```

### Shell session escapes the slice

Verify:

```
loginctl user-status <user> | grep Linger
cat /etc/systemd/system/user@$(id -u <user>).service.d/jabali.conf
```

The drop-in must contain `Slice=jabali-user-<user>.slice`. If it
doesn't, re-run the reconciler — `user.slice.ensure` writes it.

## Cutover rollback

If `jabali-panel admin slice-cutover` fails probes and auto-rollback
didn't work, manually:

```
for v in 8.5 8.4 8.3 8.2 8.1 8.0 7.4; do
  systemctl unmask  php${v}-fpm.service 2>/dev/null
  systemctl enable  php${v}-fpm.service 2>/dev/null
  systemctl start   php${v}-fpm.service 2>/dev/null
done
```

Per-user units stay harmlessly running on their own sockets. After
fixing the root cause (usually a missing healthcheck file or a user
without a bound PHP domain), re-run cutover.
