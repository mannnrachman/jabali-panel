# M30 Backup Format Reference

Operator-facing reference for the wire shape of Jabali backups (M30 / ADR-0075).

## Repository

| Path | Owner / mode | Purpose |
|---|---|---|
| `/var/lib/jabali-backups/repo/` | `root:jabali` `0750` | Single shared restic repo |
| `/etc/jabali-panel/restic-repo.password` | `root:jabali` `0640` | Repo encryption password |
| `/var/lib/jabali-backups/.restore.lock` | `root:jabali` `0640` | Global restore flock |
| `/var/lib/jabali-backups/restore-staging/<job-id>/` | per-stage | Materialized stage dirs during restore |

Operators **must back up `/etc/jabali-panel/restic-repo.password` to safe
out-of-band storage**. Losing the password = losing every snapshot. The
panel does not auto-rotate this password (rotating it would invalidate
every existing snapshot).

## Snapshot tagging

Every Jabali-managed snapshot carries:

```
jabali                      # blanket retention scope
kind=<account_backup|system_backup>
job-id=<ULID>               # links all stage snapshots in one run
stage=<name>                # see stage list below
user-id=<ULID>              # account_backup only
system=<hostname>           # system_backup only
db=<dbname>                 # stage=db snapshots only
```

`restic forget --tag jabali --keep-daily=N --keep-weekly=N --keep-monthly=N --prune`
is therefore safe: it touches **only** Jabali snapshots, not foreign
snapshots an operator might be storing in the same repo.

## Stage list

### Account backup

| Stage | Source | Snapshot type |
|---|---|---|
| `home` | `/home/<u>/` (with excludes) | path |
| `db` | `mariadb-dump <db>` per-database | stdin |
| `mail` | `stalwart-cli account export …` per mailbox into staging dir | path |
| `dns` | `pdnsutil list-zone <zone>` per zone (Step 6+) | path |
| `cron` | YAML rendering of `cron_jobs` (Step 6+) | path |
| `ssh` | YAML rendering of `ssh_keys` (Step 6+) | path |
| `apps` | YAML rendering of `application_installs` (Step 6+) | path |
| `php` | YAML rendering of `php_pool*` (Step 6+) | path |
| `manifest` | `manifest.json` (this file) | stdin |

### System backup

| Stage | Source |
|---|---|
| `panel_db` | mariadb-dump of `jabali_panel`, `jabali_kratos`, `jabali_pdns` |
| `panel_config` | `/etc/jabali-panel/` (excluding `restic-repo.password`) |
| `service_config` | `/etc/stalwart/`, `/etc/powerdns/`, `/etc/nginx/sites-*`, `/etc/php/*/fpm`, `/etc/systemd/system/jabali-*.service.d/` |
| `mail_state` | `/var/lib/stalwart/` (RocksDB) |
| `tls` | `/etc/letsencrypt/` |
| `security` | `/etc/crowdsec/`, `/etc/ufw/user.rules`, `/etc/ufw/user6.rules`, `/etc/modsecurity/` |
| `os_users` | filtered `/etc/passwd` `/etc/shadow` `/etc/group` `/etc/gshadow` (uid >= 1000 OR primary group `jabali`/`jabali-mail`/`jabali-sockets`/`pdns`) |
| `manifest` | `system_manifest.json` |

## Manifest schema (account_backup)

```json
{
  "schema_version": 1,
  "kind": "account_backup",
  "job_id": "01J5JOB000000000000000001",
  "created_at": "2026-04-28T12:00:00Z",
  "source": {
    "hostname": "mx.example.com",
    "panel_sha": "abc123…",
    "panel_version": "v0.2.10"
  },
  "user": {
    "id": "01J5USER000000000000000001",
    "username": "alice",
    "email": "alice@example.com",
    "uid_at_source": 1001,
    "is_admin": false
  },
  "restic": {
    "snapshot_id": "abc12345",
    "parent_snapshot": "fe98dc76",
    "bytes_added": 12345678,
    "bytes_total": 234567890
  },
  "stages": [
    { "name": "home",      "status": "ok",      "tag": "stage=home",     "snapshot_id": "snapH" },
    { "name": "db",        "status": "ok",      "tag": "stage=db,db=alice_wp", "snapshot_id": "snapDB1", "items": ["alice_wp"] },
    { "name": "mail",      "status": "skipped", "warnings": ["mailbox_export_skipped:stalwart_down"] },
    { "name": "manifest",  "status": "ok",      "tag": "stage=manifest" }
  ],
  "warnings": []
}
```

## Manifest schema (system_backup)

Same shape minus `user{}`; adds `linked_account_jobs: []` listing the
per-user account_backup job-IDs that ran under the same system job-id.

## Restore: hand-rolled equivalents

If the panel is offline and you need to extract data manually:

```bash
# List every snapshot for a job-id
restic --repo /var/lib/jabali-backups/repo \
       --password-file /etc/jabali-panel/restic-repo.password \
       snapshots --tag job-id=<JOB_ID>

# Dump the manifest snapshot to stdout
restic --repo /var/lib/jabali-backups/repo \
       --password-file /etc/jabali-panel/restic-repo.password \
       dump <manifest_snapshot_id> manifest.json | jq .

# Restore one stage
restic --repo /var/lib/jabali-backups/repo \
       --password-file /etc/jabali-panel/restic-repo.password \
       restore <stage_snapshot_id> --target /tmp/recovery
```

## Forward compat

`schema_version` bumps invalidate older restorers — `AccountManifestFromBytes`
returns `ErrUnsupportedSchema` rather than guessing. M30.1+ raises the
version when adding fields that older code can't ignore safely.

## Failure modes documented in ADR-0075

- Lost password → repo unrecoverable (no recovery — back up the file).
- Repo corruption → `restic check --read-data-subset=10%` runs in the
  retention timer post-prune; failures alert.
- Stalwart down at backup time → mail stage skipped, manifest records.
- Restic version drift between backup host (0.16) and restore host
  (older) → install.sh pins the floor at 0.16.
