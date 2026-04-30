# M42 — AIDE FIM runbook

Companion to plans/m42-aide-fim-system-integrity.md and ADR-0087.

## What it is, in one sentence

`/usr/bin/aide` daily-checks SHA-256 hashes of system binaries +
configs (`/bin /sbin /usr/bin /usr/sbin /lib /etc /boot /root`),
excluding paths the panel writes to. Diffs surface in the admin
Security → AIDE tab + fire `aide.tamper.detected` via M14.

## Files / units / commands

| Where | What |
|---|---|
| `/etc/aide/aide.conf` | Generated config. install.sh is the only writer. |
| `/var/lib/aide/aide.db` | Hash baseline (root:root 0600). |
| `/var/log/aide/aide.report.log` | Append-only check report log. |
| `/var/lib/aide/.jabali-installed` | First-install timestamp (sentinel). |
| `jabali-aide-check.service` + `.timer` | Daily run at 04:30 UTC + 15m jitter. |
| `aide --check` | Manual check. |
| `aide --init` | Re-build DB from current FS state. |
| `jabali aide status` | DB age + tail of last report. |
| `jabali aide rebuild --full` | Operator re-baseline (after deliberate change). |

## Daily checks

```bash
systemctl is-active jabali-aide-check.timer
# Expected: active

du -sh /var/lib/aide/
# Expected: < 100 MB (stable; grows linearly with watched-tree file count)

systemctl status jabali-aide-check.service --no-pager | tail -10
# Last run timestamp + exit code

journalctl -u jabali-aide-check --since today | tail
```

## Initial install workflow

1. `jabali update` runs `install_aide()`:
   - apt installs aide + aide-common.
   - Writes `/etc/aide/aide.conf`.
   - Background `aide --init` starts (~2-5 min on a typical host).
   - Enables `jabali-aide-check.timer`.
2. While init runs, panel UI AIDE tab shows "AIDE DB still
   building".
3. After init: `/var/lib/aide/aide.db` exists, panel UI shows DB
   age = minutes-old, Last check = "—" (no checks yet).
4. Next morning at 04:30 UTC: timer fires, panel UI shows
   Last check + zero diffs (clean baseline).

## Investigating an AIDE alert

When `aide.tamper.detected` fires (M14 dispatches to configured
channels):

1. Visit `/jabali-admin/security?tab=aide`.
2. Sample table shows the diff (added/changed/removed paths).
3. Triage:
   - **Expected change** (kernel bump, package install, manual
     config edit) → re-baseline:
     ```bash
     jabali aide rebuild --full
     ```
   - **Unexpected change** → investigate via:
     ```bash
     # Tail the full report:
     tail -100 /var/log/aide/aide.report.log

     # Check who/when via journal:
     journalctl --since "24 hours ago" | grep -E "<file-path>"

     # If the file is owned by jabali, check git history:
     git -C /opt/jabali2 log --since "1 week ago" -- path/to/file
     ```

## Adding a new path to the exclude list

When a vendor package starts writing to a path AIDE watches:

1. Edit `install.sh install_aide()` — add the path to the
   `AIDE_CONF` heredoc exclude block.
2. `jabali update` — install.sh re-renders `/etc/aide/aide.conf`.
3. `jabali aide rebuild --full` — re-baseline so the path no
   longer appears in checks.

## Re-baseline after `jabali update`

Panel binaries (`/usr/local/bin/jabali-*`) change checksums on every
release. The morning after `jabali update`, AIDE will fire
`aide.tamper.detected` listing those binaries.

```bash
# Verify the only diff is panel binaries (expected):
tail -50 /var/log/aide/aide.report.log

# Re-baseline:
jabali aide rebuild --full
```

Phase 2 will add `jabali aide rebuild --paths /usr/local/bin/jabali-*`
for surgical re-baseline that doesn't reset the whole DB.

## What's NOT covered

- **Off-host DB shipping.** Standard tripwire/AIDE practice (sign
  DB, ship to S3, verify before each check) is deferred to phase 2.
  M30 backup includes `/var/lib/aide` so a snapshot exists in the
  daily backup, which is enough for most threat models.
- **Partial re-baseline.** v1 ships full `aide --init` only. After
  every `jabali update`, operator must re-baseline manually.
- **`/home/`** — out of scope. LMD inotify watches user docroots
  in real time (M33).
- **`/var/log/`** — log rotation guarantees daily changes;
  checksums useless.

## Rollback

Three levels:

1. **Suppress the M14 alert** without disabling AIDE:
   ```bash
   # Disable just the event source by setting the channel
   # routing for aide.tamper.detected to "none" in the panel
   # Notifications → Routing tab.
   ```

2. **Disable the daily check** but keep AIDE installed:
   ```bash
   systemctl disable --now jabali-aide-check.timer
   ```
   Operator still has `aide --check` for manual runs.

3. **Remove AIDE entirely** (last resort):
   ```bash
   systemctl disable --now jabali-aide-check.timer
   apt-get remove --purge aide aide-common
   rm -rf /var/lib/aide /var/log/aide /etc/aide /etc/jabali/.aide-installed
   ```
   The "AIDE" tab will show "AIDE not active".

## Cross-references

- ADR-0087 — design rationale + alternatives.
- M30 backup — `/var/lib/aide` is in the data_state stage.
- M33 / M39 / M40 runbooks — sibling tiers (file scan, exec audit,
  daemon confinement).
