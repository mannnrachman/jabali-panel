# M30 — Backup & Restore (restic-backed)

**Goal.** Two backup kinds, one restic backbone, full restore for both:

1. **Account-full backup** — per-user (cPanel/DA parity). Bundle = home + DBs + mailboxes + DNS + cron + SSH + apps + PHP + manifest.
2. **System backup** — entire Jabali panel state. Bundle = all panel/Kratos/PowerDNS DBs + every `/etc/jabali-panel/*` config + service drop-ins (`/etc/stalwart`, `/etc/powerdns`, `/etc/nginx/sites-*`, `/etc/php/*/fpm`, `/etc/systemd/system/jabali-*.service.d`) + Stalwart RocksDB (`/var/lib/stalwart`) + Let's Encrypt state (`/etc/letsencrypt`) + UFW + CrowdSec config + every account-full backup of every user, gathered under a single `job-id`. Restore-from-system on a clean OS produces a working panel byte-for-byte.

Restic gives us native dedup, incremental snapshots, AES-256-GCM encryption, and pluggable backends (local fs / S3 / B2 / SFTP / REST server) for free — features the original tar.zst plan deferred to M30.1/M30.2.

A single-account backup ingests the same logical content as before:

1. `/home/<user>/` (with smart excludes — `.cache`, `.npm`, `node_modules`, `.composer/cache`, `__pycache__`, `wp-content/cache`, etc.)
2. Per-database SQL dumps (`mariadb-dump --single-transaction --skip-lock-tables --routines --triggers --events`)
3. Mailboxes — JMAP/mbox export per mailbox via `stalwart-cli`
4. DNS zones owned by the user (BIND format via `pdnsutil list-zone`)
5. Cron jobs YAML (from DB)
6. SSH authorized_keys YAML (from DB)
7. Application installs YAML (M19 — type, domain, subdir, version, params)
8. PHP version / pool config YAML (selected version, ini overrides)
9. `manifest.json` — schema version, backup type, source hostname, panel sha, user-id, username, created_at, stage statuses

… but the bundle now becomes a **restic snapshot**, not a tar.zst on disk. The serializers write into a per-job staging dir under `/run/jabali-backup/<job-id>/` (tmpfs); `restic backup --tag <stage>` ingests; staging dir is unlinked. Restic deduplicates across runs and across users (single shared repo).

Operator UX paths:
- **Admin/user download** → `restic restore <snapshot-id> --target <tmpdir>` materializes, panel-api tars the result with zstd, streams response, unlinks. Download is portable tar.zst the user can extract anywhere.
- **In-place restore** → `restic restore` directly into a fresh staging area, then per-stage import (DB load, DNS upsert, mailbox JMAP import) using the same orchestrator as backup, run in reverse.
- **Retention** → `restic forget --keep-daily=N --keep-weekly=M --keep-monthly=K --prune` on a daily timer, replacing the hand-rolled reaper.
- **Remote destinations** → set `RESTIC_REPOSITORY` to `s3:…` / `sftp:…` / `b2:…` in Server Settings. Native to restic, no Jabali code.

Branch: `m30/backup-restore`. Default mode: branch + ff-merge into `main` after every step.

ADR target: **0075**. (ADRs 0065-0074 already on `main`: 0065 server-status, 0066 LE panel cert, 0067 SSH shell sandbox, 0068 cgroup metrics, 0069 status masonry, 0070 LE SAN, 0071 mariadb loopback, 0072 malware stack, 0073 stalwart aliases, 0074 lazy-service-alert-suppression — renumbered 0067 → 0074 by dispatcher in commit 183ae18 to resolve a 2-min collision with SSH sandbox. Pre-existing dnssec collision at 0057 resolved in this branch by renumbering dnssec → 0076; webpush keeps 0057.)

---

## Constraints + invariants

- **Single shared restic repo, server-managed password.** `/var/lib/jabali-backups/repo/` (root:jabali 0750). Password generated at install time, stored at `/etc/jabali-panel/restic-repo.password` (root:jabali 0640 — matches the kratos-db-password / pdns.env / sso.key conventions where the panel user reads via group membership; 0600 owner-only would lock the retention service out). Single repo because (a) hosting workloads dedup heavily across accounts (every WordPress install shares the core), and (b) operators are single-tenant — admin already has root, so per-user passwords add no isolation. Per-tenant repos can land later if multi-tenant ever happens.
- **Snapshot tagging is the partition.** Every snapshot carries (a) `kind=<account_full|system>`, (b) `job-id=<ULID>` linking all stage-snapshots from one run, (c) `stage=<name>`, (d) `jabali` blanket. Account-full additionally carries `user-id=<ULID>`. System backups carry `system=<server-hostname>` so multi-host operators can disambiguate. UI lists filter on these tags server-side; users only ever see their own user-id's snapshots, system backups are admin-only.
- **Long-running root op → systemd-run transient unit**, NOT an agent child. A 30-min backup running as a cgroup descendant of `jabali-agent.service` dies on every `jabali update`. Identical pattern to M29: `systemd-run --unit=jabali-backup-<job-id>.service /usr/local/bin/jabali-internal-backup-worker --params=/run/jabali/backup-<id>.json`. Status via `systemctl is-active` + `journalctl -u <unit>`.
- **Disk-quota awareness.** Repo lives outside per-user quota. Restore-to-home is the only stage that hits quota; EDQUOT maps to 507/`quota_exceeded` per the M25.x path; restore stage records the failure but other stages still run (partial restore preferred over zero).
- **One backup at a time per (kind, user-id).** account_full backups for different users can run in parallel; only one per user. Only one system backup globally (it touches every account anyway, and overlaps with account_full would race on shared dump consistency). Backend tracks single in-memory job slots; second call returns 409 with running `job_id`.
- **One restore at a time per host.** Two parallel restores would race on `/etc/nginx/sites-enabled` reloads, PowerDNS NOTIFY, Stalwart RocksDB, MariaDB DDL. Single global flock at `/var/lib/jabali-backups/.restore.lock` covers both restore-account and restore-system.
- **System restore is a special boot path, not a normal flow.** A system restore on a live panel is dangerous (it's overwriting itself). Two modes:
  1. **Bare-metal recovery.** Operator does fresh OS install → `bash <(curl install.sh)` → `jabali system restore --from-snapshot=<id> --remote-url=<repo>` (CLI only, no UI for this — UI requires a running panel which is what restore is recovering). Worker stops jabali-panel + jabali-agent first to remove the moving target, restores DBs and configs, then starts services.
  2. **Migration to a new host.** Same flow on the destination: install fresh, then restore. The original host stays untouched.
  Live in-place system restore on a running panel is INTENTIONALLY NOT EXPOSED via the UI in v1 — too easy to footgun. CLI `--force` flag for operators who know what they're doing.
- **Mailbox export depends on Stalwart up.** If `jabali-stalwart.service` is inactive, mailbox stage skips with `manifest.warnings += ["mailbox_export_skipped:stalwart_down"]`. Backup still completes; restore detects missing mailbox tag and skips that stage too.
- **Hard 50 GB account cap (logical content size, NOT repo size).** Above that, refuse with a clear error pointing the operator at SFTP+rsync. Logical size measured via pre-pass `du -sb` so we don't ingest then cancel.
- **No symlink escapes during restore.** `restic restore` doesn't honor absolute symlinks anyway, but the post-restore `tar` for download uses `--no-same-owner --no-same-permissions` and re-chowns post-extract.
- **Manifest schema versioned.** `schema_version: 1` in manifest, stored as the first object in the snapshot (path `/manifest.json` inside the staging dir). Future M30.1 bumps schema; restore refuses unknown versions.
- **Restic repo passwords NEVER leave the host.** Download materializes content first, then re-tars. The user gets a plain tar.zst — they don't need restic installed to read their backup.
- **Migration high-water-mark on main: 000083** (notifications_sms_kind, landed during the M30 Step 1 foundation rebase 2026-04-28). M30 Step 1 takes 000084 (backup_jobs) + 000085 (server_settings restic columns). Pre-merge collision check: `ls panel-api/internal/db/migrations/ | sed 's/\.\(up\|down\)\.sql$//' | sort -u | awk -F_ '{print $1}' | uniq -d` must be empty.
- **No reconciler convergence.** Operator-initiated only. Daily snapshots are a `systemd timer`, not the reconciler tick.

---

## Wave gate (Step 2 = restic init + manifest schema)

Step 1 lays foundation (DB tables + ADR + dirs + apt install of restic + Server Settings).
**Step 2 is the wave gate — it pins (a) the restic repo location + password lifecycle, (b) the manifest.json schema, (c) the snapshot tagging convention.** Steps 3-9 must not start before Step 2 lands.

Wave A (3, 4, 5): independent serializers per stage (home, DB, mailbox). Run in parallel — each writes to `/run/jabali-backup/<job>/<stage>/` and runs `restic backup --tag stage=<name> ...` independently.

Wave B (6, 7): orchestrator + cancel + restore worker. Sequential.

Wave C (8, 9): REST + admin UI + user UI. Sequential.

---

## Steps

### Step 1: foundation — DB schema, dirs, restic install, retention timer, ADR-0075

**Files:**
- `panel-api/internal/db/migrations/000084_create_backup_jobs.up.sql` + `.down.sql`
- `panel-api/internal/db/migrations/000085_server_settings_backup.up.sql` + `.down.sql`
- `panel-api/internal/models/backup_job.go`
- `panel-api/internal/repository/backup_job_repository.go`
- `install.sh`:
  - `apt-get install -y restic` (Debian 13 ships restic 0.16; pin via `dpkg --status` check)
  - `install -d -m 0750 -o root -g jabali /var/lib/jabali-backups /var/lib/jabali-backups/repo`
  - First-boot: if no `/etc/jabali-panel/restic-repo.password`, generate `openssl rand -base64 32 > <file>`; install with `-m 0640 -o root -g jabali` (so the retention service running as `jabali` can read via group); then `restic init --repo /var/lib/jabali-backups/repo --password-file <file>` (idempotent; `restic init` fails fast on existing repo, swallow that error).
- `install/systemd/jabali-backup-retention.service` + `.timer` (daily 04:30) — runs `jabali backup retention apply`
- `panel-api/cmd/server/backup_retention_cmd.go` (`jabali backup retention apply` → `restic forget --keep-daily=N --keep-weekly=M --keep-monthly=K --prune --tag jabali` using values from server_settings)
- `docs/adr/0075-backup-restore-restic.md`
- `docs/BLUEPRINT.md` — add M30 section

**Schema (000084):**

```sql
CREATE TABLE backup_jobs (
  id              CHAR(26) NOT NULL PRIMARY KEY,                 -- ULID
  user_id         CHAR(26) NOT NULL,
  kind            ENUM('backup','restore','download') NOT NULL,
  status          ENUM('queued','running','succeeded','partial','failed','cancelled') NOT NULL DEFAULT 'queued',
  systemd_unit    VARCHAR(128) NOT NULL,                         -- jabali-backup-<id>.service
  snapshot_id     CHAR(64) NOT NULL DEFAULT '',                  -- restic snapshot ID after success
  parent_snapshot CHAR(64) NOT NULL DEFAULT '',                  -- restic --parent for incremental
  bytes_added     BIGINT UNSIGNED NOT NULL DEFAULT 0,            -- new bytes vs parent (from restic JSON output)
  bytes_total     BIGINT UNSIGNED NOT NULL DEFAULT 0,            -- logical total
  manifest_json   JSON DEFAULT NULL,
  warnings_json   JSON DEFAULT NULL,
  error_text      TEXT DEFAULT NULL,
  source_hostname VARCHAR(253) NOT NULL DEFAULT '',
  source_panel_sha CHAR(40) NOT NULL DEFAULT '',
  created_at      DATETIME(6) NOT NULL,
  started_at      DATETIME(6) DEFAULT NULL,
  finished_at     DATETIME(6) DEFAULT NULL,
  KEY idx_user_id_created (user_id, created_at DESC),
  KEY idx_status (status),
  KEY idx_snapshot (snapshot_id)
);
```

**Schema (000085):**

```sql
ALTER TABLE server_settings
  ADD COLUMN backup_keep_daily   INT UNSIGNED NOT NULL DEFAULT 7,
  ADD COLUMN backup_keep_weekly  INT UNSIGNED NOT NULL DEFAULT 4,
  ADD COLUMN backup_keep_monthly INT UNSIGNED NOT NULL DEFAULT 6,
  ADD COLUMN backup_remote_url   VARCHAR(512) NOT NULL DEFAULT '',  -- empty = local repo only
  ADD COLUMN backup_remote_credentials_ref VARCHAR(128) NOT NULL DEFAULT '';  -- pointer to /etc/jabali-panel/restic-remote.env
```

**ADR-0075 covers:**
- Why restic (vs tar.zst, vs borg, vs duplicity, vs rsnapshot): single Go binary, dedup + encryption + remote backends out of the box, mature.
- Why a single shared repo (vs per-user): hosting workloads dedup massively, operators are single-tenant.
- Why systemd-run transient units (M29 carry-over).
- Why we materialize-then-tar on download (vs hand the user a restic snapshot ID): users shouldn't need restic installed to read their backup.
- Why one-restore-per-host gate (concurrency on shared system state).
- Why hard 50 GB ceiling on logical content (operational, not technical).
- Why we DON'T do per-user repos in v1 (deferred to multi-tenant work if it ever lands).
- Why we DON'T expose the restic repo password to users.

**Verification:**
- `migrate up` → `mariadb -e "DESCRIBE backup_jobs"` shows expected cols; `migrate down` cleanly reverses.
- `ls -la /var/lib/jabali-backups` → `drwxr-x--- root jabali`; repo subdir has `config` + `keys/` + `data/`.
- `cat /etc/jabali-panel/restic-repo.password` → 0640 root:jabali, 44 chars (32 random bytes base64).
- `restic --repo /var/lib/jabali-backups/repo --password-file /etc/jabali-panel/restic-repo.password snapshots` → exits 0, prints empty list.
- `systemctl status jabali-backup-retention.timer` → enabled + active, next firing 04:30.

**Exit criteria:**
- Migrations 000084, 000085 land on main.
- ADR-0075 status = `accepted`.
- BLUEPRINT.md has M30 section.
- restic 0.16+ present on host, repo initialized, password file in place.
- Retention timer enabled.

---

### Step 2 (WAVE GATE): restic wrapper + manifest schema + tagging

**Files:**
- `internal/backup/restic.go` — typed wrapper around `restic` CLI (init, backup, snapshots, restore, forget, prune, dump). All commands invoked with `--password-file`, `--json`, structured stderr capture.
- `internal/backup/manifest.go` — Go types for manifest.json
- `internal/backup/tagging.go` — tag constants (`StageHome`, `StageDB`, `StageMailbox`, etc.) + helpers for `--tag user-id=…`, `--tag kind=…`
- `internal/backup/restic_test.go` — wrapper tests (mock subprocess)
- `internal/backup/manifest_test.go` — golden-file tests; schema_version=1
- `docs/runbooks/backup-format.md` — operator-facing schema reference

**Manifest.json shape (schema_version: 1):**

```json
{
  "schema_version": 1,
  "kind": "account_full",
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
  "restic": {
    "snapshot_id": "ab12cd34…",
    "parent_snapshot": "fe98dc76…",
    "bytes_added": 12345678,
    "bytes_total": 234567890
  },
  "stages": [
    { "name": "home",      "status": "ok",      "tag": "stage=home",     "warnings": [] },
    { "name": "databases", "status": "ok",      "tag": "stage=db",       "items": ["shukiv_wp"], "warnings": [] },
    { "name": "mailboxes", "status": "skipped", "tag": "stage=mail",     "warnings": ["stalwart_down"] },
    { "name": "dns",       "status": "ok",      "tag": "stage=dns",      "items": ["example.com"] },
    { "name": "cron",      "status": "ok",      "tag": "stage=cron" },
    { "name": "ssh_keys",  "status": "ok",      "tag": "stage=ssh" },
    { "name": "apps",      "status": "ok",      "tag": "stage=apps" },
    { "name": "php",       "status": "ok",      "tag": "stage=php" }
  ]
}
```

**Snapshot tag convention:**

```
user-id=<ULID>     # required on every backup snapshot
kind=account_full  # backup type
stage=<name>       # one of: manifest, home, db, mail, dns, cron, ssh, apps, php
job-id=<ULID>      # links all stage-snapshots from one backup run
jabali             # blanket tag for retention scoping
```

Each stage = a separate restic snapshot (so retention can prune them as a unit per user). The `manifest` snapshot is one tiny JSON blob holding the cross-stage manifest.

**Restic wrapper invariants:**
- Always pass `--password-file /etc/jabali-panel/restic-repo.password`.
- Always pass `--json` for machine-parseable output.
- Never log password contents; redact `RESTIC_PASSWORD` from any error envelope.
- Capture stderr separately; restic emits human-readable progress on stderr.
- Wrapper struct holds `RepoURL` (for remote-backend support in M30.1).

**Verification:**
- Round-trip test on manifest: encode → decode → field-by-field compare.
- Refuses schema_version=0 with `errors.Is(err, ErrUnsupportedSchema)`.
- Wrapper.Snapshots() against an empty repo → empty slice + nil error.
- Wrapper.Backup() with `--dry-run` against fixture dir → succeeds + reports expected file count.

**Exit criteria:**
- Wrapper + manifest types on main.
- Golden-file test stable (committed `testdata/manifest_v1_golden.json`).
- `docs/runbooks/backup-format.md` documents tags + manifest fields.
- **Wave gate**: this is now the immutable contract. Steps 3-9 build against it.

---

### Step 3 (PARALLEL Wave A): home-tar serializer → restic

**Files:**
- `panel-agent/internal/commands/backup_home.go` — `backup.home`
- `panel-agent/internal/commands/backup_home_test.go`

**Agent command:** `backup.home`

**Params:**
```go
type backupHomeParams struct {
  JobID    string `json:"job_id"`
  UserID   string `json:"user_id"`
  Username string `json:"username"`
}
```

**Behaviour:**
- Pre-pass: `du -sb /home/<u>` → reject with `failed_precondition` if > 50 GiB.
- `restic backup /home/<u> --tag user-id=<UserID> --tag kind=account_full --tag stage=home --tag job-id=<JobID> --tag jabali --exclude-file=/etc/jabali-panel/restic-excludes.list`
- Excludes file (managed by install.sh): `.cache`, `.npm`, `node_modules`, `.composer/cache`, `__pycache__`, `*.tmp`, `wp-content/cache`, `wp-content/uploads/cache`, `*.swp`.
- Returns the restic snapshot ID + parent + bytes_added from `--json` output.

**Verification:**
- 100-MB synthetic home → restic backup → `restic snapshots --tag job-id=<id> --tag stage=home` lists one snapshot. `restic ls <id>` shows expected files.
- Symlink fixture (`/home/<u>/escape -> /etc/shadow`) → archived as symlink, NOT followed.
- 51-GiB synthetic home → command refuses pre-ingest with `failed_precondition`.

---

### Step 4 (PARALLEL Wave A): per-database SQL dumps → restic

**Files:**
- `panel-agent/internal/commands/backup_databases.go` — `backup.databases`
- `panel-agent/internal/commands/backup_databases_test.go`

**Agent command:** `backup.databases`

**Params:**
```go
type backupDatabasesParams struct {
  JobID     string   `json:"job_id"`
  UserID    string   `json:"user_id"`
  Username  string   `json:"username"`
  Databases []string `json:"databases"`  // pre-filtered to user-owned
}
```

**Behaviour:**
- For each db, validate ownership (defense-in-depth check against `database_users.granted_user`).
- Stream each dump directly to restic via `--stdin-from-command`:
  ```
  restic backup --stdin --stdin-filename <db>.sql \
    --tag user-id=<UserID> --tag kind=account_full --tag stage=db --tag job-id=<JobID> --tag jabali --tag db=<db> \
    -- mariadb-dump --single-transaction --skip-lock-tables --routines --triggers --events --hex-blob <db>
  ```
- Each db = one stage snapshot. Repo dedups identical schemas across users → big wins on common WordPress installs.

**Verification:**
- Round-trip: dump → restic → restore → mariadb load → diff `INFORMATION_SCHEMA.TABLES`. Zero diff.
- Foreign db name in input → refused with `permission_denied` agent error before any restic call.

---

### Step 5 (PARALLEL Wave A): mailbox export → restic

**Files:**
- `panel-agent/internal/commands/backup_mailboxes.go` — `backup.mailboxes`
- `panel-agent/internal/commands/backup_mailboxes_test.go`

**Agent command:** `backup.mailboxes`

**Params:**
```go
type backupMailboxesParams struct {
  JobID     string   `json:"job_id"`
  UserID    string   `json:"user_id"`
  Username  string   `json:"username"`
  Mailboxes []string `json:"mailboxes"`  // user@domain list
}
```

**Behaviour:**
- For each mailbox: `stalwart-cli account export <user@domain> --format=mbox --output=/run/jabali-backup/<JobID>/mail/<user@domain>.mbox`
- Then `restic backup /run/jabali-backup/<JobID>/mail/ --tag stage=mail --tag job-id=<JobID> --tag user-id=<UserID> --tag jabali`
- Unlink the staging dir post-restic.
- If `jabali-stalwart.service` is inactive: succeed with `mailbox_export_skipped:stalwart_down` warning, no snapshot created for this stage.

**Verification:**
- 100-message mailbox round-trip via restic → import to test instance → message-id set matches.
- Stalwart down → command exits 0 with skipped status, no panic, no snapshot created.

---

### Step 6 (Wave B): orchestrator + `backup.create` worker

**Files:**
- `panel-agent/internal/commands/backup_create.go` — `backup.create`, `backup.cancel`, `backup.status`
- `panel-agent/internal/commands/backup_internal_worker.go` (or compiled as separate binary `/usr/local/bin/jabali-internal-backup-worker`)
- `panel-api/internal/api/backups.go` — REST stub (returns 501; full impl in Step 8)

**Agent command:** `backup.create`

**Params:**
```go
type backupCreateParams struct {
  JobID    string `json:"job_id"`     // ULID, panel-api-supplied
  UserID   string `json:"user_id"`
  Username string `json:"username"`
  Email    string `json:"email"`
  IsAdmin  bool   `json:"is_admin"`
  Databases []string `json:"databases"`
  Mailboxes []string `json:"mailboxes"`
  Zones     []string `json:"zones"`
  CronJobsYAML  []byte `json:"cron_jobs_yaml"`     // panel-api pre-renders
  SSHKeysYAML   []byte `json:"ssh_keys_yaml"`
  AppsYAML      []byte `json:"apps_yaml"`
  PHPYAML       []byte `json:"php_yaml"`
}
```

**Behaviour:**
- Spawns transient unit: `systemd-run --unit=jabali-backup-<JobID>.service /usr/local/bin/jabali-internal-backup-worker --params=/run/jabali/backup-<JobID>.json`
- Worker:
  1. Creates staging dir `/run/jabali-backup/<JobID>/` (tmpfs).
  2. Calls home / databases / mailboxes / DNS export stages (each spawns `restic backup` with appropriate stage tag).
  3. Writes YAML files (cron / ssh_keys / apps / php) into staging, runs single `restic backup` for the YAML bundle (stage=meta).
  4. Builds `manifest.json` aggregating every stage's restic snapshot ID + warnings.
  5. Writes manifest into staging, runs `restic backup --stdin --stdin-filename manifest.json --tag stage=manifest --tag user-id=<…> --tag job-id=<…> --tag jabali` from manifest bytes.
  6. Unlinks staging dir.
  7. POSTs job summary to panel-api `/internal/backup-job/<id>` (HMAC-authenticated; secret in `/etc/jabali-panel/backup-worker.secret` 0640 root:jabali).
- `backup.status` reads `systemctl is-active <unit>` + journal tail + queries `restic snapshots --tag job-id=<id> --json`.
- `backup.cancel` runs `systemctl stop <unit>`. Any partial snapshots get retention-pruned later.

**Why the worker isn't a normal agent child:** `jabali update` mid-backup would SIGTERM the in-flight backup if the worker were a child of `jabali-agent.service`. Transient unit is independent.

**Verification:**
- 1-GB synthetic account → backup completes < 2 min, manifest snapshot present in restic, all stage snapshots tagged correctly.
- Mid-backup `systemctl restart jabali-agent` → backup completes (worker is independent).
- Cancel mid-backup → unit dead, no manifest snapshot, partial stage snapshots get tagged for retention.

---

### Step 7 (Wave B): `backup.restore` worker

**Files:**
- `panel-agent/internal/commands/backup_restore.go` — `backup.restore`, `backup.restore_status`
- `panel-agent/internal/commands/backup_internal_restore_worker.go`

**Agent command:** `backup.restore`

**Params:**
```go
type backupRestoreParams struct {
  JobID         string `json:"job_id"`
  ManifestSnapshotID string `json:"manifest_snapshot_id"`  // user picks which backup
  TargetUserID  string `json:"target_user_id"`             // create or replace
  Overwrite     bool   `json:"overwrite"`                  // false = error on conflict
}
```

**Behaviour:**
- Spawns transient unit `jabali-restore-<JobID>.service`.
- Worker:
  1. Acquires global flock (`flock /var/lib/jabali-backups/.restore.lock`); refuses if held.
  2. `restic dump <ManifestSnapshotID> manifest.json` → parses manifest, validates schema_version.
  3. Looks up sibling snapshot IDs by `--tag job-id=<manifest.job_id>`.
  4. If target user exists and `!Overwrite` → error.
  5. Creates user (panel-api callback to provision via M20 Kratos flow + Linux user creation if needed).
  6. Stages in order:
     - `restic restore <home-snapshot> --target /home/<u>` (chown -R post-extract)
     - For each db snapshot: `restic dump <id> <db>.sql | mariadb <db>` (after creating db + grants via panel-api callback)
     - `restic restore <dns-snapshot> --target /tmp/restore-dns-<JobID>/`; then per-zone `pdnsutil load-zone <zone> <file>`
     - `restic restore <yaml-snapshot> --target /tmp/restore-yaml-<JobID>/`; panel-api absorbs cron/ssh/apps/php from YAML
     - `restic restore <mail-snapshot> --target /tmp/restore-mail-<JobID>/`; per-mailbox `stalwart-cli account import <user@domain> --format=mbox <file>`
  7. Each stage independently abortable; on stage failure, RECORDS + CONTINUES. Final status = `partial` if any stage failed, `succeeded` if all passed.

**Idempotency rules:**
- Domain already exists with same name → skip (assume same user).
- Database already exists → error unless `Overwrite=true`.
- Cron job duplicate (same name + command + schedule) → skip.

**Verification:**
- Round-trip: backup → restore on a clean panel → `diff -r /home/<u>` empty; `mariadb-dump` of restored db = same as original.
- `Overwrite=false` on existing user → `already_exists` error before touching anything.
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
- `POST /admin/users/:id/backups` — create backup; returns `{job_id, systemd_unit}`
- `GET /admin/users/:id/backups` — list this user's snapshots (via `restic snapshots --tag user-id=<id> --tag stage=manifest --json`)
- `GET /admin/backups` — list ALL manifest snapshots across users (admin overview)
- `GET /admin/backups/:job_id` — single job detail incl. manifest + warnings
- `GET /admin/backups/:job_id/status` — live status (calls agent backup.status)
- `GET /admin/backups/:job_id/download` — materialize-then-tar: `restic restore` to tmpdir, `tar -I zstd -cf -` streams response, unlinks tmpdir on close. Auth-gated.
- `POST /admin/backups/:job_id/cancel` — running job only
- `DELETE /admin/backups/:job_id` — soft delete (DB row marked + retention will prune the snapshots later)
- `POST /admin/backups/restore` — body `{manifest_snapshot_id, target_user_id, overwrite}`; returns `{job_id, systemd_unit}` (kind=restore)

**UI shape:**
- `/jabali-admin/backups` page = single Card with two tabs: `Backups` (list of all manifest snapshots) and `Create Backup` (button opens CreateBackupDrawer with user picker + checkboxes for stages, all checked by default).
- Each row in list: User · Created at · Logical Size · Bytes Added (dedup win) · Status (badge) · Actions (Download / Cancel if running / Delete / Restore-elsewhere).
- Restore drawer: target user picker (existing or "create new from manifest"), Overwrite toggle.
- Live polling via TanStack Query refetchInterval=2s for any job in `running` state.

**Verification:**
- Playwright spec at `panel-ui/tests/e2e/admin-backups.spec.ts`:
  1. Create backup for user `shukivaknin` → assert job appears with status=queued.
  2. Wait up to 60s for status=succeeded.
  3. Click Download → file downloads, magic bytes check `28 b5 2f fd` (zstd).
  4. Restore to a different user-id → wait succeeded → assert /home/<new-user> populated.
- Bundle size delta ≤ 30 KB minified.

---

### Step 9 (Wave C, account-full only): user-shell "Download my backup"

**Files:**
- `panel-ui/src/shells/user/MyProfileBackupCard.tsx`
- `panel-api/internal/api/user_backups.go` — `POST /me/backups`, `GET /me/backups`, `GET /me/backups/:id/download` (RequireAuth, scope = self only)

**Shape:** A card on the user dashboard with:
- "Generate full backup" button → POST /me/backups → drawer shows progress (poll backup.status).
- List of recent self-backups (only the user's own; admin view stays separate).
- Download button per row (materializes via panel-api).
- Notification on completion (M14 hook — new event source `backup_succeeded` / `backup_failed`).

Self-backup uses identical worker. Difference: Auth claim must match `target_user_id`; cross-user GET returns 404 not 403.

**Verification:**
- Playwright user-shell spec: log in as `shukivaknin` → click Generate → wait succeeded → download.
- Cross-user attempt: `alice` GET /me/backups/<bob's job-id> → 404.
- Concurrency: two rapid clicks → first 200, second 409 with running job_id.

**Exit criteria (account-full half of milestone):**
- Steps 1-9 merged to main.
- Playwright suites green (admin + user account-full flows).
- Runbook covers account-full flow.

---

### Step 10 (Wave D, system): system-backup serializer + agent commands

**Files:**
- `panel-agent/internal/commands/backup_system.go` — `system.backup`, `system.backup_status`, `system.backup_cancel`
- `panel-agent/internal/commands/backup_internal_system_worker.go`
- `internal/backup/system_manifest.go` — system-manifest schema (separate from account manifest)
- `internal/backup/system_manifest_test.go` — golden-file

**Agent command:** `system.backup`

**Params:**
```go
type systemBackupParams struct {
  JobID       string `json:"job_id"`
  IncludeAccounts bool `json:"include_accounts"` // when true, fan out to per-user account_full backups under same job-id
}
```

**Stages (every snapshot tagged `kind=system stage=<n> job-id=<JobID> system=<hostname> jabali`):**

1. **`stage=panel_db`** — mariadb-dump of `jabali_panel`, `jabali_kratos`, `jabali_pdns` (each as a separate stdin-piped restic snapshot).
2. **`stage=panel_config`** — `/etc/jabali-panel/` whole tree EXCEPT `restic-repo.password` (a system restore on a different host needs a NEW repo password — operator supplies via CLI). Includes pdns.env, kratos.yml, panel.env, stalwart.env, stalwart-mariadb.password, stalwart-admin.token, backup-worker.secret, restic-remote.env if remote backend in use.
3. **`stage=service_config`** — `/etc/stalwart/`, `/etc/powerdns/`, `/etc/nginx/sites-available/`, `/etc/nginx/sites-enabled/` (links!), `/etc/php/*/fpm/pool.d/`, `/etc/systemd/system/jabali-*.service.d/`, `/etc/systemd/resolved.conf.d/jabali*.conf`, `/etc/cron.d/jabali-*`.
4. **`stage=mail_state`** — `/var/lib/stalwart/` (RocksDB). Big stage; restic dedups well across daily snapshots.
5. **`stage=tls`** — `/etc/letsencrypt/`.
6. **`stage=security`** — `/etc/crowdsec/`, `/etc/ufw/user.rules`, `/etc/ufw/user6.rules`, `/etc/modsecurity/`.
7. **`stage=os_users`** — filtered `/etc/passwd` `/etc/shadow` `/etc/group` `/etc/gshadow` containing only Linux users with uid ≥ 1000 OR primary group `jabali`/`jabali-mail`/`jabali-sockets`/`pdns`. Restore re-adds them via `useradd`/`usermod` rather than overwriting `/etc/passwd` (avoids breaking root + system daemons).
8. **`stage=manifest`** — system_manifest.json with snapshot IDs of every stage above, plus a `linked_account_jobs: [...]` list of account_full job-ids if `IncludeAccounts=true`.

If `IncludeAccounts=true`, the system worker enqueues a `backup.create` for every user (parallel up to N=4 to avoid hammering disks) and waits for completion before sealing the manifest. All these account snapshots share the parent system job-id via tag.

**Verification:**
- Round-trip on a small panel: backup → list snapshots tagged with the job-id → expect 8 stage snapshots + N user manifest snapshots. system_manifest.json validates against schema_version=1.
- Stalwart down → mail_state stage skipped with warning, manifest still written.
- 30-min backup with mid-run `systemctl restart jabali-agent` → completes (transient unit, identical pattern to account_full).

---

### Step 11 (Wave D, system): system-restore worker + CLI

**Files:**
- `panel-agent/internal/commands/backup_system_restore.go` — `system.restore`, `system.restore_status`
- `panel-agent/internal/commands/backup_internal_system_restore_worker.go`
- `panel-api/cmd/server/system_restore_cmd.go` — `jabali system restore` CLI subcommand

**CLI shape (operator-facing):**

```
jabali system restore \
  --remote-url=<restic repo URL or local path> \
  --password-file=<path to restic password file> \
  --snapshot=<system-manifest snapshot ID, or 'latest'> \
  --include-accounts \
  --force
```

`--force` required because restore overwrites a running panel; refuses without it.

**Worker behaviour:**
- Acquires global flock.
- Stops `jabali-panel.service`, `jabali-agent.service` (this last one means the worker MUST be a systemd-run transient unit that survives the agent stop — verified during Step 10 design).
- Restores in this strict order (pre-flight stage failures abort everything; post-stage failures record + continue):
  1. **panel_db** → `mysqladmin drop` then `mysqladmin create` for each panel db, then load the dumps. Refuses to drop if any has unexpected content (pre-flight: `SHOW TABLES` count must match what the source recorded in manifest).
  2. **panel_config** → restore over `/etc/jabali-panel/` (EXCEPT `restic-repo.password` which stays from the new install).
  3. **service_config** → restore the four config trees, then `systemctl daemon-reload`.
  4. **tls** → restore `/etc/letsencrypt/`.
  5. **security** → restore CrowdSec + UFW + ModSec configs.
  6. **mail_state** → stop `jabali-stalwart.service`, restore `/var/lib/stalwart/`, then start it (so restore writes happen with no Stalwart writers contending).
  7. **os_users** → for each entry in the filtered passwd: `useradd -u <uid> -g <gid> -G <supp-groups> -d <home> -s <shell> -p '*' <username>` if absent; `usermod` to align if present. Crypt(3) hashes from shadow are written via `chpasswd -e`.
  8. **accounts** (if `--include-accounts`) → loop over linked_account_jobs, run the existing account-restore worker for each, in series.
- Starts `jabali-agent.service`, then `jabali-panel.service`.
- Verifies via `curl` to panel /api/v1/health.

**Verification:**
- Two-VM test: backup VM-A → restore on VM-B (clean OS + bash <(install.sh)) → log in with original credentials → list domains/users — every entity from VM-A is present, byte-identical config files, same TLS certs.
- `--force` missing on a live host → CLI refuses with explanation.
- Restic password mismatch → fail-fast at flock acquisition stage.

---

### Step 12 (Wave D, system): admin UI for system backups

**Files:**
- `panel-ui/src/shells/admin/backups/SystemBackupsTab.tsx`
- `panel-ui/src/shells/admin/backups/AdminBackupsPage.tsx` — add tab between existing tabs
- `panel-api/internal/api/system_backups.go` — `POST /admin/system/backups`, `GET /admin/system/backups`, `GET /admin/system/backups/:id/status`, `POST /admin/system/backups/:id/cancel`

**UI shape:** new "System Backups" tab on `/jabali-admin/backups`:
- "Create system backup" button → opens drawer with `Include all account backups` checkbox (default on).
- List of system snapshots: created_at, size, status, included accounts count, actions (Cancel if running / Delete).
- **No "Restore" button** in v1 — system restore is CLI-only per the bare-metal model. UI shows a tooltip explaining "to restore a system backup, run `jabali system restore` on a fresh host".
- Shows the most recent successful system snapshot prominently with a "How to restore" expandable that prints the exact CLI command with the snapshot ID pre-filled.

**Verification:**
- Playwright admin test: create system backup with include-accounts → wait completion → assert manifest snapshot listed → assert linked account snapshots count ≥ 1.
- Cancel mid-run → unit dead, partial snapshots get retention-pruned.

---

**Exit criteria for the whole milestone:**
- All 12 steps merged to main.
- `make test` green.
- Playwright suites green (admin account + user account + admin system).
- Two-VM bare-metal recovery test passes (Step 11 verification).
- Runbook at `plans/m30-backup-restore-runbook.md` covers: account flow, system flow, two-VM bare-metal recovery script, how to read manifests, how to point the repo at S3/SFTP, retention timer troubleshooting, restic password loss recovery (= you don't recover; document it).
- ADR-0075 marked `accepted`.

---

## Out of scope (defer to M30.1 / M30.2)

- **Scheduled backups** (cron-style) — M30.1. Daily snapshot + retention is already free with restic; only the scheduler glue is missing.
- **Remote destinations enabled** (S3 / SFTP / B2) — M30.1. Restic supports them natively; just needs Server Settings UI + credential storage at `/etc/jabali-panel/restic-remote.env` + `RESTIC_REPOSITORY` switching.
- **Per-user repos** — multi-tenant work, deferred.
- **Backup verification command** (`jabali backup verify`) — restic has `restic check` built in; surface it in a follow-up.
- **Restore from cPanel/DA/Hestia/WHM tarballs** — that's M15 (migration importers), separate codepath.

---

## Risk register

| Risk | Mitigation |
|---|---|
| Backup runs across `jabali update` and dies | Transient systemd unit, not agent child (Step 6) |
| Two parallel backups for same user race on staging | One-job-per-user-per-kind slot in panel-api (409 second call) |
| Two parallel restores race on nginx reload | Global restore flock (Step 7) |
| Restored home blows past recipient's quota | EDQUOT → 507/`quota_exceeded`; restore stage fails, manifest records warning; restore continues with other stages |
| Mailbox export takes hours on a 50 GB mailbox | Hard 50 GB account cap; UI surfaces warning to use SFTP for whales |
| Restic repo password lost | Repo unrecoverable. Operator gets a one-time post-install reminder to backup `/etc/jabali-panel/restic-repo.password` to safe storage; documented in runbook. |
| Restic repo corruption | `restic check --read-data-subset=10%` in retention timer post-prune; alert on fail. |
| Stalwart down at backup time | Skip mailbox stage with warning; backup completes; same on restore. |
| Backups grow unbounded | Native restic retention (Step 1 timer). |
| Backup repo readable by non-admin | `/var/lib/jabali-backups/repo/` 0750 root:jabali; password file 0600; download endpoint RequireAdmin or self-scope check. |
| Restic version drift between hosts (backup on 0.16, restore on 0.15) | Document minimum version (0.16) in runbook; install.sh ensures 0.16+. |

---

## Implementation order summary

```
Step 1  ──┐
          ├──> Step 2 (gate) ──┬──> Step 3 (home) ──┐
          │                    ├──> Step 4 (dbs)  ──┤
          │                    └──> Step 5 (mail) ──┴──> Step 6 (assemble) ──┬──> Step 8 (REST + admin UI) ──> Step 9 (user UI)
          │                                                                  │
          │                                                                  ├──> Step 7 (restore)
          │                                                                  │
          │                                                                  ├──> Step 10 (system backup)
          │                                                                  │         │
          │                                                                  │         ├──> Step 11 (system restore CLI)
          │                                                                  │         │
          │                                                                  │         └──> Step 12 (system backup admin UI)
          │
          (ADR-0075 + restic install + retention timer land here)
```

Wave D (Steps 10-12) reuses Step 6's orchestrator + worker primitives + Step 2's restic wrapper. System backup with `IncludeAccounts=true` literally fans out to per-user account_full backups under one shared job-id, so the per-user code path is the same. System restore is its own worker because it has a unique pre-flight (stop services) + post-flight (restart + health-check) shape.

---

## Dispatchable starting point

Step 1 + Step 2 are dispatchable today. Steps 3-5 are parallel-dispatchable AFTER Step 2 lands. Steps 6-9 are sequential and must wait for their predecessors.

Total estimated commits: ~35-40 (restic wrapper + tests, three parallel account serializers, two account workers, system worker, system restore worker + CLI, REST handlers, three UI tabs, runbook with two-VM bare-metal recovery script).
