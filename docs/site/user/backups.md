# Backups (User)

`/jabali-panel/backups`. Your account-level backup view.

## What you can see

If the administrator has enabled tenant-visible backups for your package, this page shows:

- **Snapshots** — the list of `account_full` snapshots taken for your account, with timestamp, size, and source destination.
- **Schedules** — the cadence the administrator configured for your account (read-only).
- **Restore** — request restore of a specific snapshot back to your account.

If tenant-visible backups are disabled for your package, the page shows only the [Backup Download](./backup-download.md) card (if even that is enabled).

## Requesting a restore

1. Pick the snapshot you want to restore from.
2. Pick the components — everything, or a subset (files only, databases only, mailboxes only).
3. Pick a target — restore in place (overwrites current state) or to a "staging" location (writes to `/home/<your-username>-restore-<timestamp>/`).
4. Submit. The request is queued for operator approval before running.

Operator approval is required by default to prevent accidental large-scale data overwrites. The operator may grant your package automatic restore approval if they trust you to manage your own data — ask if useful.

## What a restore includes

- Home directory files.
- Databases (each restored into its original DB; the DB is dropped and recreated before the dump replays).
- Mailbox content (each mailbox is restored as-is, including IMAP folder structure and flags).
- DNS zone records.
- Cron job definitions.

## What a restore does *not* include

- Linux account password (you use Kratos for panel login; SFTP uses SSH keys).
- TLS certificates (auto-reissued via Let's Encrypt within minutes of restore).
- Application file ownership for files created by the application after the snapshot.

## Frequency

Snapshot frequency is set by the administrator per package. Typical cadence: nightly with 7 daily / 4 weekly / 12 monthly retention.

## Off-host destination

By default, backups live on the panel host's local disk. If the administrator has configured an off-host destination (SFTP, S3, B2, Azure, GCS, restic REST), snapshots are replicated there automatically — so a panel host failure does not lose the backup data.

## Download

For an ad-hoc download of your account state, use [Backup Download](./backup-download.md). The download produces a fresh snapshot on demand and offers it for direct download.
