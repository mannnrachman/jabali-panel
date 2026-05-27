# Backup Destinations

Backups → Destinations. The list of repositories the panel can write restic snapshots to.

## Supported destination types

| Type | Configuration |
|---|---|
| **Local** | Filesystem path on the panel host. Cheapest, no off-host disaster recovery. |
| **SFTP** | Host, user, SSH key path. Off-host on a server you control. |
| **S3** | Endpoint, region, bucket, access key, secret key. Works against AWS S3, MinIO, Wasabi, Cloudflare R2, any S3-compatible service. |
| **Backblaze B2** | Account ID, application key, bucket. |
| **Azure Blob** | Account name, account key, container. |
| **Google Cloud Storage** | Bucket, path to a service-account JSON key. |
| **Restic REST server** | URL, optional bearer token. For self-hosted restic-rest-server (HTTPS recommended; `JABALI_RESTIC_INSECURE_TLS` to allow self-signed). |

## Adding a destination

1. **Add destination** → pick a type → fill in credentials.
2. The form refuses to save until the credentials parse syntactically.
3. Click **Test** before going live. The test action:
   - Connects to the destination.
   - Verifies write permission.
   - If no restic repository exists at the chosen path, runs `restic init` (auto-init).
4. On success, the destination is available for selection in [Schedules](./backup-schedules.md).

## Repository password

Each destination has its own restic repository password, auto-generated and stored encrypted in `db_admin_secrets`. The password is never exposed in the UI; restic cannot read snapshots without it. Operators who want a copy for emergency recovery may export the password to a sealed envelope from the CLI (`jabali destination get <id> --show-password`).

## Multi-destination by schedule

A schedule may target multiple destinations. Restic writes to each in turn. Bandwidth and storage cost are paid once per destination; deduplication happens per repository, not across repositories.

## Health

The Destinations tab shows the last-test result per row. The reconciler re-tests destinations daily; failures fire the `backup_failed` notification event source ([Notifications Events](./notifications-events.md)).

## Removing a destination

Forbidden if any schedule targets it. Reassign or delete the schedules first.

## CLI

```bash
jabali destination list
jabali destination get <id-or-name>
jabali destination create --type sftp --name daily-offsite --host backup.example.com --user backups --key /root/.ssh/backup
jabali destination test daily-offsite
jabali destination update daily-offsite --host new-host.example.com
jabali destination delete daily-offsite
```
