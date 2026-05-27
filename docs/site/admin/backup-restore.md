# Backup Restore

Backups → Restore. Browse snapshots in any destination and restore an account or the whole system.

## Browse

Pick a destination; the page lists every snapshot (newest first) with: timestamp, kind (`account_full` / `system_backup`), subject, size, restic snapshot id.

Filter by kind, by subject (for `account_full`), by date range.

## Restore — `account_full`

Pick a snapshot, then choose:

- **Target user** — restore to the same user (overwrite), to a new user (preserve original), or to an existing different user (uncommon; usually for forensic investigation).
- **Components** — restore everything, or pick subsets (files only, databases only, mailboxes only, DNS only).
- **Dry run** — see what would be touched without writing.

The agent restores in the same per-stage order the backup ran: files first, databases second, mailboxes third, DNS records last. Each stage is idempotent at the file or row level.

## Restore — `system_backup`

System restores are typically performed on a freshly-bootstrapped panel host. Sequence:

1. Provision a clean Debian 13 host and run the standard `bash install.sh`.
2. Configure access to the backup destination (or copy credentials over).
3. On the new panel host:

   ```bash
   jabali destination create --type s3 --name recovery ...
   jabali system restore --snapshot <id> --destination recovery
   ```

4. The restore covers all 7 stages: panel-DB × 3, OS users, Stalwart state, Kratos state, hosted sites, config snapshot, plus the wrapping restic snapshot integrity check.
5. After completion, run `jabali repair --diagnose` to surface any drift between restored state and the fresh host (typically only IP-related mismatches if the new host has a different IP).

Round-trip restore was live-verified on 192.168.100.150.

## Operator-only safety rails

- A restore into an existing user requires typing the user's username as confirmation.
- A `system_backup` restore into a non-empty panel database (i.e. not a fresh host) requires `--force-overwrite`. The default refuses to clobber.
- Every restore writes one `backup.restore` audit row plus per-stage rows.

## What restore does *not* do

- Re-issue Let's Encrypt certificates — they are restored from the snapshot. Run `jabali ssl renew <domain>` for any cert whose expiry is near.
- Reconcile listen IPs — if the new host has different IPs than the snapshot's host, the operator must update [IP Addresses](./ip-addresses.md) before the reconciler succeeds.
- Restart third-party services not under the panel's control.

## CLI

```bash
jabali account restore --user <new-id> --snapshot <id> --destination recovery
jabali system  restore --snapshot <id> --destination recovery
```
