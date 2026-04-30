# M30 Backup & Restore — Operator Runbook

Companion to ADR-0075 + the wire-format reference in
[`docs/runbooks/backup-format.md`](../docs/runbooks/backup-format.md).

## 1. Account backup flow (admin)

1. **Admin → Sidebar → Backups → Create Backup.**
2. Pick the user; optionally list databases / mailboxes (comma-separated).
3. Submit. The drawer fires `POST /api/v1/admin/users/<id>/backups` which:
   - Inserts a `backup_jobs` row (status=`queued`).
   - Dispatches `backup.create` on the agent.
   - Marks the row `running` once the agent acknowledges.
4. Agent orchestrator runs stages: `home` → `db` → `mail` → `manifest`.
5. Each stage produces a separate restic snapshot tagged with the job-id.
6. On completion, refetch the list — status flips to `succeeded` /
   `partial` / `failed`. Download materializes via the admin row's
   Download button.

## 2. Account backup flow (user)

User Profile page → "My backups" card → "Generate full backup". Same
agent path; `/me/backups` scopes to the caller's `user_id` via the
Kratos session (cross-user attempts return 404, not 403, per plan §9).

## 3. System backup flow

1. **Admin → Backups → System backups tab → Create system backup.**
2. Default `include_accounts=true` fans out per-user account backups
   under the same job-id.
3. Stages: `panel_config` → `service_config` → `mail_state` → `tls` →
   `security` → `manifest`.
4. **Restore is CLI-only** in v1 (ADR-0075):

   ```bash
   jabali system restore --snapshot=<id> --include-accounts --force
   ```

5. The Tab card prints the exact command pre-filled with the most
   recent successful snapshot ID.

## 4. Disaster recovery (bare-metal, ADR-0080)

Source: any reachable restic repo (SFTP, S3, B2, Azure, GCS, REST,
local). Destination: a clean Debian 13 / Ubuntu 24.04 VPS.

### One-shot install + restore

```bash
# On the recovery host (root shell)
bash <(curl -fsSL https://git.linux-hosting.co.il/shukivaknin/jabali2/raw/branch/main/install.sh) \
    --restore-from=sftp:user@backup-host:/path/to/repo \
    --restore-credentials=/root/dest.env \
    --restore-password=/root/restic-repo.password \
    --restore-snapshot=latest
```

The installer:

1. Lays the base packages, panel binaries, and systemd units.
2. Installs the operator-supplied restic password +
   destination-credentials env file at the canonical paths.
3. Skips the bootstrap-admin seed (the restored panel_db brings the
   real admin row).
4. Calls `jabali system restore --remote-url=… --snapshot=latest
   --include-accounts --apply --force`.
5. Apply phase runs inside `panel-agent` and:
   - Pipes each `panel_db` snapshot's SQL back into MariaDB via
     unix-socket auth.
   - Rsyncs the staged `panel_config` tree onto `/etc/jabali-panel`
     (excluding `restic-repo.password` so the operator-supplied file
     wins).
   - Rsyncs the staged `tls` tree onto `/etc/letsencrypt`.
6. Restarts `jabali-agent.service` + `jabali-panel.service`.

### Inputs the operator must keep offline

- **restic password file.** Without it the repo is opaque. Store
  alongside (but not inside) the backup. Pass via `--restore-password=
  <path>` or `JABALI_RESTORE_PASSWORD=<value>` env.
- **destination credentials env file** (SFTP only when password
  auth: `SSHPASS=<pw>`; S3/B2/Azure/GCS: backend env vars). Pass via
  `--restore-credentials=<path>`. Format: `KEY=VALUE` per line, 0600
  root:root.
- **`--restore-from` URL.** Same syntax as `restic --repo`:
  `sftp:user@host:/path`, `s3:s3.amazonaws.com/bucket`, `b2:bucket`,
  `/var/lib/jabali-backups/repo`, etc.

### Auto-applied stages

| Stage          | Action                                                        |
|----------------|---------------------------------------------------------------|
| `panel_db`     | Pipe each `<db>.sql` into MariaDB via unix socket             |
| `panel_config` | Rsync onto `/etc/jabali-panel` (excludes `restic-repo.password`) |
| `tls`          | Rsync onto `/etc/letsencrypt`                                 |

### Staged-but-not-auto-applied (operator judgement)

| Stage            | Why deferred                                                                                  |
|------------------|----------------------------------------------------------------------------------------------|
| `mail_state`     | Stalwart RocksDB — must stop service, rsync `/var/lib/stalwart`, start service. Do by hand.  |
| `service_config` | install.sh has already written canonical nginx/php configs; auto-overwrite would clobber.    |
| `security`       | UFW + CrowdSec + ModSec rules; review diff before applying.                                  |
| `os_users`       | Filtered `/etc/passwd` + `/etc/shadow`; needs careful merge with the new host's accounts.     |
| `data_state`     | Per-user `account_full` payloads — separate per-user restore workflow.                       |

Each shows up under `/var/lib/jabali-backups/restore-staging/<job-id>/<stage>/`.

### Inspect-only (don't apply)

```bash
jabali system restore \
    --remote-url=sftp:user@backup-host:/path/to/repo \
    --credentials-ref=/root/dest.env \
    --snapshot=latest \
    --no-apply \
    --force
```

Stages files only. Operator inspects, then either re-runs with
`--apply` or applies by hand.

### Verify

```bash
systemctl is-active jabali-panel jabali-agent
curl -k https://localhost/api/v1/health
journalctl -u jabali-panel -n 100 --no-pager
```

### Restore from local repo (single-VM exercise)

For drill / dry-run on the same host where the backup was taken:

```bash
jabali system restore \
    --remote-url=/var/lib/jabali-backups/repo \
    --snapshot=latest \
    --no-apply \
    --force
```

Skip `--apply` so the live host's panel_db isn't clobbered. Inspect
the staged directory, then apply manually if everything looks sane.

## 5. Retention

`jabali-backup-retention.timer` fires daily 04:30 and runs
`jabali backup retention apply`. The command iterates every enabled
`backup_schedules` row whose `keep_{daily,weekly,monthly}` is non-NULL
and runs:

```
restic forget --tag schedule-id=<id> \
    --keep-daily=<N> --keep-weekly=<N> --keep-monthly=<N>
```

per schedule, then a single `restic prune` at the end so blob removal
runs once per timer fire. Snapshots are tagged with `schedule-id=<id>`
at backup time so the per-schedule forget targets only that
schedule's chain. Manual (non-scheduled) backups have no
`schedule-id` tag and are NEVER auto-pruned.

Manual sweep:

```bash
sudo -u jabali jabali backup retention apply
```

Adjust per-schedule policy via the Schedules drawer in the admin UI
(Backups → Schedules → Edit), or directly:

```sql
UPDATE backup_schedules SET
    keep_daily = 14,
    keep_weekly = 8,
    keep_monthly = 12
WHERE id = '01J5SCHED…';
```

Server-wide `server_settings.backup_keep_*` columns still exist for
backwards compatibility but the retention CLI no longer reads them
(per-schedule is the source of truth).

### Concurrency cap

`server_settings.backup_max_concurrent_jobs` (default 2) caps how
many backup_jobs the in-process dispatcher will keep in
status=running at once. Adjust from Backups → Settings, or:

```sql
UPDATE server_settings SET backup_max_concurrent_jobs = 4 WHERE id = 1;
```

## 6. Repo integrity

```bash
sudo restic --repo /var/lib/jabali-backups/repo \
            --password-file /etc/jabali-panel/restic-repo.password \
            check --read-data-subset=10%
```

Schedule on a separate `*-check.timer` (M30.1 work).

## 7. Common failures

| Symptom | Cause | Fix |
|---|---|---|
| `du: cannot read directory: Permission denied` during home pre-pass | Tenant left mode `0700` directory | Skip via `restic-excludes.list` (file-level exclude) |
| `failed_precondition: backup refused: /home/<u> is N bytes` | Logical size > 50 GiB ceiling | SFTP+rsync the home dir; backup the mailbox / DB separately |
| `mailbox_export_skipped:stalwart_down` warning | Stalwart inactive at backup time | Restart `jabali-stalwart.service` and rerun |
| `another restore is in progress` | Global flock held | Check `journalctl -u jabali-agent` for the running restore; do not force-clear the lock |
| `restic init` repeats every install | First-boot guard flips on existing `repo/config` blob — should be one-shot | Inspect `/var/lib/jabali-backups/repo/config`; if missing, repo is wedged — restore from a backup repo or re-init (data loss) |

## 8. Out of scope (M30.1+)

- Scheduled backups (cron-style)
- Remote backends enabled (S3 / SFTP / B2)
- Per-user repos (multi-tenant)
- `restic check` integration with notifications
