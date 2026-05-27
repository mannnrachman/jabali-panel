# Backups

Two backup kinds:

- **account_full** — one user's entire account (home dir + DBs + mail + DNS + cron + apps).
- **system_backup** — the whole panel host (panel DB × 3 + OS users + every site).

Both are restic-backed (deduplicated, encrypted at rest, multi-destination).

## Destinations

`/jabali-admin/backups` → Destinations:

| Type | Config |
|---|---|
| Local | filesystem path on the panel host |
| SFTP | host, user, key path |
| S3 | endpoint, bucket, access key + secret |
| Backblaze B2 | account ID, application key |
| Azure Blob | account name, key, container |
| Google Cloud Storage | bucket, service-account JSON |
| Restic REST server | URL, optional auth |

Multiple destinations per backup are supported — restic writes to each.

## Schedules

`/jabali-admin/backups` → Schedules:

- Pick the backup kind, the user(s) (for `account_full`), the destination, the cron schedule, and the retention policy.
- Schedules become `systemd` system timers managed by the agent.

Retention policies use restic's `--keep-daily`, `--keep-weekly`, `--keep-monthly`, `--keep-yearly` flags.

## system_backup contents

7 stages:

1. **panel_db × 3** — three rolling `mariabackup` dumps of the panel DB itself.
2. **OS users** — `/etc/passwd`, `/etc/shadow`, `/etc/group`, `/etc/gshadow` snapshot.
3. **Stalwart state** — Stalwart's internal data dir (mailboxes are inside).
4. **Kratos state** — identity store.
5. **Hosted sites** — every `/home/<user>` tree.
6. **Config snapshot** — `/etc/nginx/`, `/etc/php/`, `/etc/powerdns/`, `/etc/letsencrypt/`, `/etc/jabali/`, install-marker drop-ins.
7. **Restic snapshot** — wraps all of the above into one restic snapshot per destination.

## Restore

Restores are admin-only (`/jabali-admin/backups` → Restore tab):

- Pick a destination, browse snapshots, pick one.
- For `account_full`: choose target user (overwrite or new).
- For `system_backup`: typically restored on a fresh host — the panel is bootstrapped, then `jabali system restore` is run from the CLI.

Round-trip is live-verified on 192.168.100.150 as of M30.1.

## CLI

```bash
jabali destination list
jabali destination create --type sftp --name daily-offsite --host backup.example.com --user backups --key /root/.ssh/backup
jabali destination test daily-offsite

jabali backup schedule list
jabali backup schedule create --kind account_full --user <id> --destination daily-offsite --cron "0 3 * * *" --keep-daily 7 --keep-weekly 4

jabali system restore --snapshot <id> --destination daily-offsite
```

## What you don't get

- **Bare-metal disk image** — not in scope. Use your hypervisor's snapshot tools for that.
- **Application-aware restore for non-WP apps** — restic restores the files + DB; you may need to update site URLs by hand if the FQDN changes.
- **Continuous (WAL-style) DB shipping** — only periodic snapshots. For sub-minute RPO you need a separate replication setup.
