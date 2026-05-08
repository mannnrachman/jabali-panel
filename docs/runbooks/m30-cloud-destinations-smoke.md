# M30 cloud destinations — smoke runbook

Backs the smoke for restic-over-{S3, B2, SFTP} backup destinations.
Run on a fresh Debian 13 VM after `jabali update --force`.

## Prerequisites

- Panel deploy ≥ commit `ffd2046e` (M30.2.y per-destination password
  wiring landed).
- Backup foundation provisioned (`/etc/jabali-panel/restic-repo.password`
  exists OR every destination row has rotated to per-destination
  encryption — both work).
- Operator account with admin role.

## Test matrix

| Backend | URL form | Credentials                                |
|---------|---------------------------------------------------|--------|
| local   | `/var/lib/jabali-backups/repo`                    | none   |
| sftp    | `sftp:user@host:/abs/path`                        | SSH key OR password |
| s3      | `s3:s3.amazonaws.com/bucket/path`                 | AWS_ACCESS_KEY_ID + AWS_SECRET_ACCESS_KEY |
| b2      | `b2:bucket-name:path`                             | B2_ACCOUNT_ID + B2_ACCOUNT_KEY |
| azure   | `azure:container:path`                            | AZURE_ACCOUNT_NAME + AZURE_ACCOUNT_KEY |
| gcs     | `gs:bucket:path`                                  | GOOGLE_PROJECT_ID + GOOGLE_APPLICATION_CREDENTIALS file |
| rest    | `rest:https://user:pass@host/restic-repo`         | embedded in URL |

## Per-backend steps

For each backend, repeat:

### 1. Create destination
```
Admin shell → Backups → Destinations → New
- Name: smoke-<backend>
- Kind: <backend>
- URL: as above
- Credentials: backend-specific (SFTP key dropdown, S3 env, …)
- Enabled: yes
```

### 2. Test connection
Click **Test** on the row. Expect:
- Status badge → green tick within 10 s.
- Toast: `OK — restic init succeeded` (first run) or
  `OK — repo already initialised` (subsequent).

### 3. Rotate password (M30.2)
Click **Rotate password** on the row. Two-step confirm. Expect:
- Reveal Modal pops with a 64-char hex password.
- DB column `password_enc` is now non-NULL on `backup_destinations`:
  ```
  ssh root@<vm> "mariadb jabali_panel \
    -e \"SELECT name, password_rotated_at FROM backup_destinations\""
  ```
- Restic key list confirms rotation:
  ```
  sudo -u root restic --repo <url> --password-file <new-temp> \
    key list
  ```
  Should show exactly one key (the new one); the old one was
  removed by the agent rotate handler.

### 4. Account backup
Pick any user → Backups → New → select destination → Run.
Expect:
- Job row in `backup_jobs` flips
  `queued → running → succeeded` within ~60 s.
- Snapshot count on the destination grows by ~7 (home, db × N, mail
  × N, dns, cron, ssh, manifest).

### 5. Account restore round-trip
Pick the most recent `account_backup` job → Restore → target the
same user → Apply.
Expect:
- `backup.materialize` succeeds (restic dump to
  `/var/lib/jabali-backups/restore-staging/<job>/`).
- `backup.restore` applies the staged tree to /home and replays
  database dumps.
- Job row finishes `succeeded` within ~120 s.
- Smoke marker: pre-restore `/home/<user>/SMOKE` file matches
  pre-mutation contents.

## Reconciler purge

After EVERY enabled destination has been rotated:

```
sleep 60   # wait for next ReconcileAll pass
ls -la /etc/jabali-panel/restic-repo.password
# expect: ls: cannot access ... : No such file or directory
```

Reconciler `reconcileResticLegacyPassword` purges the shared file
once `backup_destinations.password_enc IS NULL` returns 0 rows.
Idempotent — subsequent passes are no-ops.

## Failure escalation

| Symptom | Probable cause | Fix |
|---------|----------------|-----|
| Test returns `agent_unreachable` | jabali-agent.service not running | `systemctl restart jabali-agent` |
| Rotate returns `verify old password` | shared file content drifted from cached row | Re-seed `password_enc` from agent: `POST /admin/backup-destinations/:id/rotate-password` again with cached old-pw |
| Backup hangs > 5 min on `restic init` | network firewall blocks egress | tcpdump on VM; whitelist destination IPs |
| Restore fails on db stage | engine='postgres' but PG service stopped | Server Settings → Databases tab → toggle PostgreSQL ON; retry |
| Reconciler purge never runs | one destination still has `password_enc IS NULL` | Click Rotate on the un-rotated row(s); wait one cycle |
