# M30 — Backup & Restore

**Goal.** Per-account full backup (cPanel/DA parity) and restore from backup file. Phase 1 = on-demand local + downloadable. Phase 2 (optional, separate milestone) = scheduled remote destinations (S3/SFTP).

A single-account backup bundles, in this order:

1. `/home/<user>/` (rsync-style tar.zst, preserving mode/owner/symlinks; excludes `.cache`, `.npm`, `node_modules`, `.composer/cache`, `__pycache__`, `wp-content/cache`, `wp-content/uploads/cache` — same exclude set cPanel uses by default to keep backups under 5× content size)
2. Per-database SQL dumps (`mariadb-dump --single-transaction --skip-lock-tables --routines --triggers --events`)
3. Mailboxes — JMAP export per mailbox via `stalwart-cli` (M6 schema; one `.eml.zip` per mailbox)
4. DNS zones owned by the user (export from PowerDNS via `pdnsutil list-zone <zone> > <zone>.zone`, one BIND-format file per zone)
5. Cron jobs YAML (from DB)
6. SSH authorized_keys YAML (from DB)
7. Application installs YAML (M19 — type, domain, subdir, version, params)
8. PHP version / pool config YAML (selected version, ini overrides)
9. `manifest.json` — schema version, backup type, source hostname, source panel version, user-id, username, created_at, file inventory with SHA-256 checksums

The whole thing lands as `jabali-backup-<username>-<timestamp>.tar.zst` under `/var/lib/jabali-backups/<user-id>/` (root:jabali, 0750), downloadable through panel-api.

Restore reverses: parse manifest → recreate user (if absent) → restore home → recreate DBs + grants + dump-load → recreate domains + DNS zones → recreate apps + cron + SSH keys → restore mailboxes via JMAP. Idempotent (skip what already exists, error on conflict unless `--overwrite`).

Branch: `m30/backup-restore`. Default mode: branch + ff-merge into `main` after every step (shared codebase pattern).

ADR target: **0065** (last on main: `0063-profiles-yaml-for-remediation-override.md`; ADR `0064` reserved for M29). M30 = ADR-0065.

---

## Constraints + invariants

- **Long-running root op → systemd-run transient unit**, NOT an agent child. A 30-min backup running as a cgroup descendant of `jabali-agent.service` dies on every `jabali update`. Pattern is identical to M29: `systemd-run --unit=jabali-backup-<job-id>.service /usr/local/bin/jabali-internal-backup --user=<u> --out=<path>`. Status via `systemctl is-active` + `journalctl -u jabali-backup-<id> --since=<start>`. No in-process ring buffers.
- **Disk-quota awareness.** Backups land OUTSIDE per-user quota (`/var/lib/jabali-backups/`, root-owned), so restoring a backup doesn't get rejected by the recipient's quota — quota is checked only at home-tar restore time, with EDQUOT mapped to a clear error per the M25.x EDQUOT path (507 / `quota_exceeded`).
- **One backup at a time per user.** Backend tracks a single in-memory job slot per (kind=backup|restore, user-id); second call returns 409 with running `job_id`. Cross-user concurrency is allowed.
- **One restore at a time per host.** Restoring two accounts in parallel would race on `/etc/nginx/sites-enabled` reloads, on PowerDNS zone NOTIFYs, on Stalwart RocksDB writes. Single global slot for restore.
- **Mailbox export depends on Stalwart up.** If `jabali-stalwart.service` is inactive, mailbox stage skips with `manifest.warnings += ["mailbox_export_skipped:stalwart_down"]`. Backup still completes; restore detects missing mailbox dirs and skips that stage too. Operator gets a notification.
- **Hard tar size limit: 50 GB per account.** Above that, refuse with a clear error pointing the operator at SFTP+rsync. cPanel does the same (their `BACKUP_MAX_SIZE_GB`). The home-tar excludes above are enough for typical accounts.
- **No symlink escapes.** `tar --acls --xattrs` IS allowed; `--absolute-names` is NOT. Restore uses `tar -C /home/<u> --no-same-owner --no-same-permissions -xf` and re-chowns post-extract — backup may have been made on a host where `<u>` had a different uid.
- **Manifest schema versioned.** `schema_version: 1` in manifest. Future M30.1 bumps schema; restore refuses unknown versions with a clear error rather than silently doing the wrong thing.
- **Backup encryption: optional, age-based.** Operator can configure a server-wide `age` recipient pubkey via Server Settings → Backup. When set, `manifest.json` declares `encrypted: true` and the bundle is `age -e -r <recipient>`-wrapped. Restore decrypts via operator-supplied identity file (CLI argument; never stored). Default = no encryption (matches cPanel default).
- **`/var/lib/jabali-backups/` retention.** Default 7 days, admin-tunable in Server Settings. Reaper cron runs daily at 04:30, deletes files older than retention. Per-user override via Packages later (Phase 2).
- **Migration high-water-mark on main: 000073** (upload_max_size_mb). M30 takes 000074, 000075, 000076.
- **No reconciler convergence.** Operator-initiated only.

---

## Wave gate (Step 2 = serializer + manifest schema)

Step 1 lays foundation (DB tables + ADR + dirs + retention reaper).
**Step 2 is the wave gate — it pins the manifest schema + tar layout. Steps 3-6 must NOT start before Step 2 lands on `main`.** Once schema is fixed, downstream waves are:

- Wave A (3, 4, 5): independent serializers per content type (home tar, DB dumps, mailbox export). Run in parallel.
- Wave B (6, 7): manifest assembler + agent commands. Sequential.
- Wave C (8, 9): REST + UI + restore. Sequential.

---

## Steps

### Step 1: foundation — DB schema, dirs, retention reaper, ADR-0065

**Files:**
- `panel-api/internal/db/migrations/000074_create_backup_jobs.up.sql` + `.down.sql`
- `panel-api/internal/db/migrations/000075_server_settings_backup_retention.up.sql` + `.down.sql`
- `panel-api/internal/models/backup_job.go`
- `panel-api/internal/repository/backup_job_repository.go`
- `install.sh` — add `install -d -m 0750 -o root -g jabali /var/lib/jabali-backups`
- `install/systemd/jabali-backup-reaper.service` + `.timer` (daily 04:30)
- `panel-api/cmd/server/backup_reaper_cmd.go` (`jabali backup reap` subcommand)
- `docs/adr/0065-backup-restore.md`
- `docs/BLUEPRINT.md` — add M30 section

**Schema (000074):**

```sql
CREATE TABLE backup_jobs (
  id           CHAR(26) NOT NULL PRIMARY KEY,        -- ULID
  user_id      CHAR(26) NOT NULL,
  kind         ENUM('backup','restore') NOT NULL,
  status       ENUM('queued','running','succeeded','failed','cancelled') NOT NULL DEFAULT 'queued',
  systemd_unit VARCHAR(128) NOT NULL,                -- e.g. jabali-backup-<id>.service
  archive_path VARCHAR(512) NOT NULL DEFAULT '',     -- absolute path under /var/lib/jabali-backups
  archive_bytes BIGINT UNSIGNED NOT NULL DEFAULT 0,
  manifest_json JSON DEFAULT NULL,
  warnings_json JSON DEFAULT NULL,
  error_text   TEXT DEFAULT NULL,
  source_hostname VARCHAR(253) NOT NULL DEFAULT '',
  source_panel_sha CHAR(40) NOT NULL DEFAULT '',
  created_at   DATETIME(6) NOT NULL,
  started_at   DATETIME(6) DEFAULT NULL,
  finished_at  DATETIME(6) DEFAULT NULL,
  expires_at   DATETIME(6) DEFAULT NULL,             -- when reaper deletes archive (kind=backup)
  KEY idx_user_id_created (user_id, created_at DESC),
  KEY idx_status (status),
  KEY idx_expires_at (expires_at)
);
```

**Schema (000075):**

```sql
ALTER TABLE server_settings
  ADD COLUMN backup_retention_days  INT UNSIGNED NOT NULL DEFAULT 7,
  ADD COLUMN backup_age_recipient   VARCHAR(256) NOT NULL DEFAULT '';
```

**Reaper:** `jabali backup reap` — DELETE FROM backup_jobs WHERE kind='backup' AND finished_at < NOW() - INTERVAL <retention> DAY → unlink archive_path → log per-user count.

**ADR-0065 covers:**
- Why filesystem (not RocksDB / object store): operators expect downloadable tar files matching cPanel/DA pattern.
- Why tar.zst (not tar.gz): zstd 5-10× faster compression at similar ratios, ~50% smaller binaries.
- Why systemd-run transient units (M29 carry-over).
- Why one-restore-per-host gate.
- Why hard 50 GB ceiling (operational, not technical).
- Why optional age encryption.
- Why we DON'T mirror cPanel's "Daily/Weekly/Monthly" tiered retention in v1.
- Why we DON'T do incremental backups in v1.

**Verification:**
- `migrate up` → `mariadb -e "DESCRIBE backup_jobs"` shows 14 cols; `migrate down` cleanly reverses.
- `ls -la /var/lib/jabali-backups` → `drwxr-x--- root jabali`.
- `systemctl status jabali-backup-reaper.timer` → next firing at 04:30.
- `jabali backup reap --dry-run` → "0 archives to expire" on empty install.

**Exit criteria:**
- Migrations 000074, 000075 land on main.
- ADR-0065 status = `accepted`.
- BLUEPRINT.md has M30 section.
- Reaper timer enabled + active.

---

### Step 2 (WAVE GATE): manifest schema + tar layout pinning

**Files:**
- `internal/backup/manifest.go` — Go types for manifest.json
- `internal/backup/layout.go` — well-known paths inside the tar
- `internal/backup/manifest_test.go` — golden-file tests; backwards-compatibility test for schema_version=1
- `docs/runbooks/backup-format.md` — operator-facing schema reference

**Manifest.json shape (schema_version: 1):**

```json
{
  "schema_version": 1,
  "kind": "account_full",
  "encrypted": false,
  "created_at": "2026-04-25T12:00:00Z",
  "source": {
    "hostname": "mx.example.com",
    "panel_sha": "abc123…",
    "panel_version": "v0.2.10"
  },
  "user": {
    "id": "01KQ…",
    "username": "shukivaknin",
    "email": "shukivaknin@gmail.com",
    "uid_at_source": 1001,
    "is_admin": false
  },
  "stages": [
    { "name": "home",       "status": "ok", "path": "home/home.tar.zst", "bytes": 12345678, "sha256": "…", "warnings": [] },
    { "name": "databases",  "status": "ok", "path": "databases/", "bytes": 234567, "files": [{"db":"shukiv_wp","path":"databases/shukiv_wp.sql.zst","sha256":"…"}], "warnings": [] },
    { "name": "mailboxes",  "status": "skipped", "warnings": ["stalwart_down"] },
    { "name": "dns",        "status": "ok", "path": "dns/", "files": [{"zone":"example.com","path":"dns/example.com.zone","sha256":"…"}] },
    { "name": "cron",       "status": "ok", "path": "cron/cron.yaml", "sha256": "…" },
    { "name": "ssh_keys",   "status": "ok", "path": "ssh_keys/keys.yaml", "sha256": "…" },
    { "name": "apps",       "status": "ok", "path": "apps/apps.yaml", "sha256": "…" },
    { "name": "php",        "status": "ok", "path": "php/php.yaml", "sha256": "…" }
  ]
}
```

**Tar layout (well-known paths):**

```
manifest.json
home/home.tar.zst
databases/<dbname>.sql.zst
mailboxes/<mailbox>.eml.zip
dns/<zone>.zone
cron/cron.yaml
ssh_keys/keys.yaml
apps/apps.yaml
php/php.yaml
```

**Verification:**
- Round-trip test: encode → decode → compare bytes of every field.
- Refuses schema_version=0 with `errors.Is(err, ErrUnsupportedSchema)`.
- Refuses schema_version=999 with same.
- Layout constants are `const`, not `var` — no test can mutate them.

**Exit criteria:**
- `internal/backup/manifest.go` + `layout.go` on main.
- Golden-file test stable (committed `testdata/manifest_v1_golden.json`).
- `docs/runbooks/backup-format.md` documents every field.
- **Wave gate**: this is now the immutable contract. Steps 3-9 build against it.

---

### Step 3 (PARALLEL Wave A): home-tar serializer

**Files:**
- `panel-agent/internal/commands/backup_home_tar.go` — `backup.home_tar`
- `panel-agent/internal/commands/backup_home_tar_test.go`

**Agent command:** `backup.home_tar`

**Params:**
```go
type backupHomeTarParams struct {
  Username string `json:"username"`
  Output   string `json:"output"` // absolute path on host, must be under /var/lib/jabali-backups
}
```

**Behaviour:**
- Validates output path is under `/var/lib/jabali-backups/` (no `..`).
- `tar --create --file=<output> --zstd --acls --xattrs --numeric-owner --exclude-from=<built-in-list> -C /home/<username> .`
- Excludes (hardcoded): `.cache`, `.npm`, `node_modules`, `.composer/cache`, `__pycache__`, `**/*.tmp`, `wp-content/cache`, `wp-content/uploads/cache`, `*.swp`, `.git/objects/pack/*.idx` (huge git packs that rebuild on restore).
- Reports `bytes_written` + `sha256` of the output file.
- 50 GB ceiling enforced via `tar -L 52428800` (KB) + check final size, error if exceeded.

**Verification:**
- Test fixture: 100-MB synthetic home with mix of files + symlinks → tar → extract → diff. SHA-256 stable across runs (reproducible because `--mtime=<manifest.created_at>` is passed so timestamps don't drift).
- Symlink escape attempt (`/home/<u>/escape -> /etc/shadow`) → escape NOT followed (tar archives symlink as symlink, not target).

---

### Step 4 (PARALLEL Wave A): per-database SQL dumps

**Files:**
- `panel-agent/internal/commands/backup_databases.go` — `backup.databases`
- `panel-agent/internal/commands/backup_databases_test.go`

**Agent command:** `backup.databases`

**Params:**
```go
type backupDatabasesParams struct {
  Username string   `json:"username"`
  Databases []string `json:"databases"` // pre-filtered to user-owned set; agent re-validates
  OutputDir string  `json:"output_dir"` // /var/lib/jabali-backups/<user-id>/<job-id>/databases/
}
```

**Behaviour:**
- For each db: `mariadb-dump --single-transaction --skip-lock-tables --routines --triggers --events --hex-blob <db> | zstd -19 -o <output_dir>/<db>.sql.zst`
- Uses MariaDB unix socket (M25.1).
- Re-validates each database belongs to `<username>` by checking `database_users.granted_user` matches in DB before dumping (defense-in-depth — panel-api should pre-filter, but if it sends a foreign db name we refuse).

**Verification:**
- Round-trip: dump → drop → load → diff `INFORMATION_SCHEMA.TABLES`. Zero diff.
- Foreign db name in input → refused with `permission_denied` agent error.

---

### Step 5 (PARALLEL Wave A): mailbox export via stalwart-cli

**Files:**
- `panel-agent/internal/commands/backup_mailboxes.go` — `backup.mailboxes`
- `panel-agent/internal/commands/backup_mailboxes_test.go`

**Agent command:** `backup.mailboxes`

**Params:**
```go
type backupMailboxesParams struct {
  Username string   `json:"username"`
  Mailboxes []string `json:"mailboxes"` // user@domain list
  OutputDir string  `json:"output_dir"` // /var/lib/jabali-backups/<user-id>/<job-id>/mailboxes/
}
```

**Behaviour:**
- Per mailbox: `stalwart-cli account export <user@domain> --format=mbox --output=<output_dir>/<user@domain>.mbox.zst` (zstd-compressed mbox; smaller and faster than zip).
- If `jabali-stalwart.service` is inactive: return `mailbox_export_skipped:stalwart_down` warning, succeed with zero outputs.

**Verification:**
- 100-message mailbox round-trip (export → import to test instance → message count + Message-ID set match).
- Stalwart-down test: `systemctl stop jabali-stalwart` → command returns success + warning, no panic.

---

### Step 6 (Wave B): assembler + `backup.create` agent command

**Files:**
- `panel-agent/internal/commands/backup_create.go` — `backup.create`, `backup.cancel`, `backup.status`
- `internal/backup/assembler.go` — orchestrates stage agent calls + writes manifest.json
- `panel-api/internal/api/backups.go` — REST stub (returns 501 NotImplemented; full impl in Step 8)

**Agent command:** `backup.create`

**Params:**
```go
type backupCreateParams struct {
  JobID    string `json:"job_id"`     // ULID, panel-api-supplied
  Username string `json:"username"`
  UserID   string `json:"user_id"`
  Email    string `json:"email"`
  IsAdmin  bool   `json:"is_admin"`
  Databases []string `json:"databases"`
  Mailboxes []string `json:"mailboxes"`
  Zones     []string `json:"zones"`
  CronJobs  []byte   `json:"cron_jobs_yaml"`     // panel-api pre-renders
  SSHKeys   []byte   `json:"ssh_keys_yaml"`
  Apps      []byte   `json:"apps_yaml"`
  PHP       []byte   `json:"php_yaml"`
  AgeRecipient string `json:"age_recipient,omitempty"` // empty = no encryption
}
```

**Behaviour:**
- Spawns transient unit: `systemd-run --unit=jabali-backup-<JobID>.service /usr/local/bin/jabali-internal-backup-worker --params=/run/jabali/backup-<JobID>.json`
- Worker:
  1. Creates job dir under `/var/lib/jabali-backups/<user-id>/<JobID>/`
  2. Calls home_tar / databases / mailboxes / DNS export inline (worker is root, can call agent commands directly via internal dispatcher OR exec the same command paths)
  3. Writes YAML files (cron / ssh_keys / apps / php) from supplied bytes
  4. Writes `manifest.json` with all stage results + warnings
  5. Tars + zstds the whole job dir → `/var/lib/jabali-backups/<user-id>/jabali-backup-<username>-<timestamp>.tar.zst`
  6. If `AgeRecipient` set: pipes through `age -e -r <recipient>` before final write
  7. Removes job dir; leaves only the final tarball
  8. Updates `backup_jobs` row via panel-api callback (POST /internal/backup-job/<id>) — internal endpoint authenticated by shared HMAC secret in `/etc/jabali-panel/backup-worker.secret` (root:jabali 0640)
- `backup.status` reads `systemctl is-active <unit>` + journal tail.
- `backup.cancel` runs `systemctl stop <unit>` + cleans the job dir.

**Why the worker isn't a normal agent child:** agent restarts during long backups (operator runs `jabali update` mid-backup) would SIGTERM the in-flight backup. Transient unit is independent of agent's lifecycle.

**Verification:**
- 1-GB synthetic account → backup completes in <2 min, archive lands at expected path, manifest passes schema_version=1 round-trip test.
- Mid-backup `systemctl restart jabali-agent` → backup completes (worker is independent of agent unit).
- Cancel mid-backup → unit dead, job dir removed, archive_path empty, status=cancelled.

---

### Step 7 (Wave B): `backup.restore` agent command

**Files:**
- `panel-agent/internal/commands/backup_restore.go` — `backup.restore`, `backup.restore_status`
- `internal/backup/restorer.go` — orchestrates restoration

**Agent command:** `backup.restore`

**Params:**
```go
type backupRestoreParams struct {
  JobID         string `json:"job_id"`
  ArchivePath   string `json:"archive_path"`     // absolute, under /var/lib/jabali-backups
  TargetUserID  string `json:"target_user_id"`   // create or replace
  Overwrite     bool   `json:"overwrite"`        // false = error on conflict, true = replace
  AgeIdentityFile string `json:"age_identity_file,omitempty"` // CLI-supplied if encrypted
}
```

**Behaviour:**
- Spawns transient unit: `systemd-run --unit=jabali-restore-<JobID>.service /usr/local/bin/jabali-internal-restore-worker ...`
- Worker:
  1. Acquires global restore lock (`flock /var/lib/jabali-backups/.restore.lock`); refuses if held.
  2. If encrypted: decrypts via `age -d -i <identity> < archive | tar -I zstd -xf - -C <tmpdir>`; else `tar -I zstd -xf <archive> -C <tmpdir>`.
  3. Validates manifest.json schema_version + checksums on every file.
  4. If target user exists + `!Overwrite` → error.
  5. Creates user (panel-api callback to provision via M20 Kratos flow + Linux user creation).
  6. Restores stages in order: home → databases → DNS → cron → ssh_keys → apps → php → mailboxes (last because Stalwart needs the user to exist).
  7. Each stage is independently abortable; on any stage failure, RECORDS the failure and CONTINUES — partial restore is better than zero. Final job status = `partial` if any stage failed, `succeeded` if all passed.
- `backup.restore_status` mirrors backup.status pattern.

**Idempotency rules:**
- Domain already exists with same name → skip (assume same user)
- Database already exists → error unless `Overwrite=true`
- Cron job duplicate → skip (same name + command + schedule)

**Verification:**
- Round-trip: backup → restore on a clean panel → `diff -r /home/<u>` = empty; `mariadb-dump` of restored db = same as original.
- Overwrite=false on existing user → returns `already_exists` agent error before touching anything.
- Lock contention: two restores in parallel → second returns `unavailable: another restore in progress`.

---

### Step 8 (Wave C): REST endpoints + admin UI

**Files:**
- `panel-api/internal/api/backups.go` — full impl
- `panel-api/internal/agent/backup_*.go` — agent param structs (cross-boundary contract)
- `panel-ui/src/shells/admin/backups/AdminBackupsPage.tsx`
- `panel-ui/src/shells/admin/backups/CreateBackupDrawer.tsx`
- `panel-ui/src/shells/admin/backups/RestoreBackupDrawer.tsx`
- `panel-ui/src/shells/admin/backups/BackupJobsList.tsx`
- `panel-ui/src/App.tsx` — route `/jabali-admin/backups`

**REST routes (all RequireAdmin):**
- `POST /admin/users/:id/backups` — create backup for user; returns `{job_id, systemd_unit}`
- `GET /admin/users/:id/backups` — list this user's backup jobs (most recent first, paginated)
- `GET /admin/backups` — list ALL jobs across users (admin overview)
- `GET /admin/backups/:job_id` — single job detail incl. manifest + warnings
- `GET /admin/backups/:job_id/status` — live status (calls agent backup.status)
- `GET /admin/backups/:job_id/download` — streams archive (auth-gated download; 404 if expired/reaped)
- `POST /admin/backups/:job_id/cancel` — running job only
- `DELETE /admin/backups/:job_id` — manual delete (unlinks archive + DB row)
- `POST /admin/backups/restore` — body `{archive_path, target_user_id, overwrite, age_identity_file?}`; returns `{job_id, systemd_unit}` (kind=restore)

**UI shape:**
- `/jabali-admin/backups` page = single Card with two tabs: `Backups` (list of all jobs across users) and `Create Backup` (button opens CreateBackupDrawer with user picker + checkboxes for stages: Home / DBs / Mailboxes / DNS / Cron / Keys / Apps / PHP, all checked by default).
- Each row in list: User · Created at · Size · Status (badge) · Actions (Download / Cancel if running / Delete / Restore-elsewhere).
- Restore action opens RestoreBackupDrawer: target user picker (existing or "create new from manifest"), Overwrite toggle, age identity file upload.
- Live polling via TanStack Query refetchInterval=2s for any job in `running` state.

**Verification:**
- Playwright spec at `panel-ui/tests/e2e/admin-backups.spec.ts`:
  1. Create backup for user `shukivaknin` → assert job appears in list with status=queued
  2. Wait up to 60s for status=succeeded
  3. Click Download → file downloads, magic bytes check `28 b5 2f fd` (zstd)
  4. Restore to a different user-id → wait succeeded → assert /home/<new-user> populated
- Bundle size delta acceptable (the 1.95 MB single-chunk concern from M21 still applies; this should add < 30 KB minified).

---

### Step 9 (Wave C): user-shell "Download my backup"

**Files:**
- `panel-ui/src/shells/user/MyProfileBackupCard.tsx`
- `panel-api/internal/api/user_backups.go` — `POST /me/backups`, `GET /me/backups`, `GET /me/backups/:id/download` (RequireAuth, scope = self only)

**Shape:** A card on the user dashboard's MyProfile page with:
- "Generate full backup" button → POST /me/backups → drawer shows progress (poll backup.status)
- List of recent self-backups (only the user's own; admin view in /jabali-admin/backups stays separate)
- Download button per row
- Notification on completion (M14 hook — new event source `backup_succeeded` / `backup_failed`)

Self-backup uses identical flow as admin-initiated. Difference: Auth claim must match `target_user_id`.

**Verification:**
- Playwright user-shell spec: log in as `shukivaknin` → click Generate → wait succeeded → download.
- Cross-user attempt: log in as `alice` → GET /me/backups/<job-id-belonging-to-bob> → 404, not 403, to avoid leaking existence.
- Concurrency: user clicks Generate twice rapidly → first returns 200, second returns 409 with the running job_id.

**Exit criteria for the whole milestone:**
- All 9 steps merged to main.
- `make test` green.
- Playwright suites green (admin + user).
- Runbook at `plans/m30-backup-restore-runbook.md` covers: how to read a manifest, how to restore from a tarball via CLI without UI, how to decrypt an age-encrypted backup, retention reaper troubleshooting.
- ADR-0065 marked `accepted`.

---

## Out of scope (defer to M30.1 / M30.2)

- **Scheduled backups** (cron-style) — M30.1.
- **Remote destinations** (S3 / SFTP / FTP / B2) — M30.1.
- **Incremental / differential backups** — M30.2 if at all (rsync hardlinks add complexity vs. tar.zst's compression ratio).
- **Per-package retention overrides** — M30.1.
- **Restore from cPanel/DA/Hestia/WHM tarballs** — that's M15 (migration importers), separate codepath.
- **Backup encryption with PGP / GPG** — age is enough; supporting both adds verification surface.
- **Backup verification command** (`jabali backup verify <archive>` to walk manifest checksums) — useful but not blocking; deferred.

---

## Risk register

| Risk | Mitigation |
|---|---|
| Backup runs across `jabali update` and dies | Transient systemd unit, not agent child (Step 6) |
| Two parallel backups for same user race on output dir | One-job-per-user-per-kind slot in panel-api (409 second call) |
| Two parallel restores race on nginx reload | Global restore flock (Step 7) |
| Restored home blows past recipient's quota | EDQUOT mapped per M25.x (507 / `quota_exceeded`); restore stage fails, manifest records warning; restore continues with other stages |
| Mailbox export takes hours on a 50 GB mailbox | Hard 50 GB account cap; UI surfaces warning to use SFTP for whales |
| age recipient pubkey misconfigured | Validate format in Server Settings save handler; reject obviously invalid input; document in runbook |
| Restore on a host with different uid for username | `--no-same-owner` + post-extract chown -R `<u>:www-data` `/home/<u>` |
| Stalwart down at backup time | Skip mailbox stage with warning; backup completes; same on restore |
| Backups accumulate, fill disk | Reaper cron (Step 1) + Server Settings `backup_retention_days` |
| Backup archive readable by non-admin | `/var/lib/jabali-backups/` 0750 root:jabali; download endpoint RequireAdmin (or self-scope check) |

---

## Implementation order summary

```
Step 1  ──┐
          ├──> Step 2 (gate) ──┬──> Step 3 (home) ──┐
          │                    ├──> Step 4 (dbs)  ──┤
          │                    └──> Step 5 (mail) ──┴──> Step 6 (assemble) ──┬──> Step 8 (REST + admin UI) ──> Step 9 (user UI)
          │                                                                  │
          │                                                                  └──> Step 7 (restore)
          │
          (ADR-0065 + reaper land here)
```

Step 8 depends on Step 6 (admin can backup) AND Step 7 (admin can restore). Both must land before Step 8 ships to main. Step 9 is the user-side carbon-copy of Step 8.

---

## Dispatchable starting point

Step 1 + Step 2 are dispatchable today. Steps 3-5 are parallel-dispatchable AFTER Step 2 lands. Steps 6-9 are sequential and must wait for their predecessors.

Total estimated commits: ~25-30 (manifest types + tests, three parallel serializers, two workers, REST handlers, two UI pages, runbook).
