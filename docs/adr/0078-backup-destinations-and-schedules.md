# ADR-0078: Backup destinations + scheduled backups (M30.1)

**Status:** ACCEPTED (2026-04-28).
**Related:** ADR-0075 (M30 restic-backed backup substrate).
**Supersedes:** none.

> **Renumber note (2026-04-28):** drafted as ADR-0077; renumbered to
> 0078 on rebase after `feat(repair): add jabali repair self-heal
> subcommand (ADR-0077)` (8062c87) landed on `origin/main` claiming
> 0077 first.

## Context

M30 (ADR-0075) shipped restic-backed backup/restore against a single
local repo at `/var/lib/jabali-backups/repo/`, manually triggered from
the admin UI. Two scope cuts were deliberately deferred:

1. **Remote destinations.** Local-only is fine for "I forgot to commit"
   but not for DR — a single disk failure loses every snapshot. The
   `server_settings.backup_remote_url` + `backup_remote_credentials_ref`
   columns from M30 were placeholder hooks for this.
2. **Scheduled backups.** Operators were running `cron` against the
   agent socket by hand. Even basic "back up everyone daily" required
   rolling your own systemd timer.

M30.1 closes both gaps: admins manage N restic-native destinations
(local + remotes), and N cron-style schedules per kind / per user.
Scheduled backups always write to local first, then asynchronously
mirror to every remote linked to the schedule via `restic copy`.

## Decisions

### 1. Restic-native backends only

Backend menu = `local | sftp | s3 | b2 | azure | gcs | rest`. No rclone.

- **Why no rclone?** rclone-via-restic adds a second binary on the
  PATH plus a per-user `rclone.conf` configuration surface that
  panel-api would have to template. Restic's native backends already
  cover every scenario where the operator owns the storage. Operators
  who want Google Drive / Dropbox / OneDrive can self-host
  restic-rest-server and point M30.1 at it via the `rest:` backend.
- **Why include `local` as a destination row?** Symmetry with schedules
  (one model: schedule -> N destinations). The copy worker no-ops on
  local destinations (no `restic copy local-to-local`).

### 2. Async copy via `restic copy` after local success

Local backup completes first → `backup_jobs.status='succeeded'` → the
finalizer enqueues one `backup_copy_jobs` row per linked destination.
A separate copy worker scans the queue every 60s and spawns
`systemd-run --collect --unit=jabali-backup-copy-<id>.service` per row.

- **Why systemd-run, not goroutines?** M29 carry-over: long-running
  jobs must outlive `jabali update` (which restarts panel-api). The
  copy CLI runs inside a transient unit; the worker only spawns +
  bookkeeps.
- **Why async, not inline with the backup?** Backups can take 30+ min;
  copy to a slow SFTP is another 30+ min. Holding the agent socket
  blocked for an hour breaks the 90-min orchestrator timeout. Async
  also lets the backup mark `succeeded` immediately so the UI shows
  the local snapshot is ready to restore — copy progress is a
  separate observable surface.

### 3. Admin-only manage destinations + schedules

Per-user destinations were considered and rejected: each tenant
managing their own S3 buckets multiplies the credential leak surface
without buying isolation that the single-tenant operator model
(admin already has root) doesn't already have.

Per-user schedules were also rejected — the admin sets the cadence.
End users can still trigger ad-hoc backups via the existing M30 user
UI.

### 4. Single shared repo password across destinations (v1)

Every destination uses `/etc/jabali-panel/restic-repo.password` for
both source and target. `restic copy --from-repo … --from-password-file …`
syntax requires both passwords; reusing one file keeps the lifecycle
simple (one secret to back up out-of-band; rotation invalidates
everything).

- **M30.2 hook:** per-destination password files at
  `/etc/jabali-panel/restic-remotes/<dest-id>.password` mirroring the
  env-file pattern, when a real "rotate one remote without losing the
  rest" requirement appears.

### 5. Cron strings, not separate hour/min/dow columns

`backup_schedules.cron_expr` is `VARCHAR(64)`. The UI ships preset
buttons (Daily/Weekly/Monthly = canonical 5-field strings) plus a
Custom radio exposing the raw input. Server-side validation via
`github.com/robfig/cron/v3` (already in deps).

- **Why not split fields?** Operators who pick Daily/Weekly/Monthly
  never see the cron string. Operators who need cron flexibility
  (every 6h, Mon+Thu, etc.) get full power without us building a
  surrogate DSL.

### 6. In-process tickers, not systemd timers

The scheduler tick (60s), finalizer tick (30s), and copy worker tick
(60s) all run as goroutines in panel-api alongside the existing
reconciler / event-source loops. The retention timer (M30 daily
04:30) stays as a CLI + systemd timer because of its slow cadence
and operator-debug surface.

- **Why goroutines for these three?** Cobra cold-start at 60s cadence
  costs more than the goroutine itself. Missed ticks during panel
  restart are tolerated — overdue rows stay overdue and the next
  tick picks them up.

### 7. Credentials in env files, not the DB

`backup_destinations.credentials_ref` is a path to a 0600 root:root
env file under `/etc/jabali-panel/restic-remotes/<dest-id>.env` holding
restic backend env vars (AWS_ACCESS_KEY_ID, B2_ACCOUNT_ID, etc.).
The DB never stores secrets; GET responses return only the file
pointer plus the list of declared keys (no values).

- **Why not Kratos secret store / Vault?** Same DB-as-truth /
  filesystem-as-truth pattern as the M16 OIDC plugin tokens, kratos-db
  password, and Stalwart admin token: panel-api writes 0600 root:root
  files at install time and references them by path.
- **Backup of the env files:** part of the M30 system_backup `panel_config`
  stage (already covered by ADR-0075 stage list).

## Consequences

### Operational
- New apt deps: none (all restic backends ship in the upstream binary
  via Go statically).
- New disk paths:
  - `/etc/jabali-panel/restic-remotes/` — root:root 0700, holds
    `<dest-id>.env` files at 0600 root:root each.
- New goroutines in panel-api: scheduler (60s), finalizer (30s), copy
  worker (60s). Each is a small loop with no memory growth; missed
  ticks tolerated.
- Existing M30 retention timer (04:30 daily) is now **per-schedule**:
  iterates every enabled `backup_schedules` row whose
  `keep_{daily,weekly,monthly}` is non-NULL, runs
  `restic forget --tag schedule-id=<id> --keep-*`, then a single
  `restic prune` at the end. Snapshots are tagged with
  `schedule-id=<id>` at backup time. Manual (non-scheduled) backups
  carry no `schedule-id` tag and are NEVER auto-pruned.
  Server-wide `server_settings.backup_keep_*` columns still exist for
  back-compat but the retention CLI no longer reads them.
  Per-destination retention = M30.2.

### Schema
- Migration 000086: `backup_destinations`.
- Migration 000087: `backup_schedules`.
- Migration 000088: `backup_schedule_destinations` (M:N join).
- Migration 000089: `backup_jobs.schedule_id` column (FK-soft, NULL =
  manual trigger).
- Migration 000090: `backup_copy_jobs` (async queue + per-destination
  status).
- Migration 000094: `backup_schedules.include_system_backup` opt-in.
- Migration 000095: `backup_schedule_users` (M:N) — multi-user fan-out
  per schedule. Empty = every non-admin user; non-empty = those users.
- Migration 000096: `server_settings.backup_max_concurrent_jobs`
  (default 2). Caps the in-process dispatcher.
- Migration 000103: `backup_jobs.run_id CHAR(26) NULL` + index. The
  scheduler mints one ULID per tick fan-out; every fan-out row from
  that tick shares the same `run_id` so the admin UI can roll them
  up under one parent row. Manual API path leaves it NULL.

### Concurrency

`server_settings.backup_max_concurrent_jobs` (default 2) caps how
many backup_jobs the in-process dispatcher will keep in
status=running at once. The scheduler `tickEnqueue` (60s) only
inserts queued rows — `tickDispatch` (10s) reads the cap, counts
running, and dispatches up to `cap - running` rows per tick.

### Security
- Creds on disk, not in DB. GET /admin/backup-destinations returns the
  list of env-var KEYS only (no values), so leak via API never exposes
  secrets even if an admin's session token is captured.
- `restic copy` runs in transient systemd-run units with the same
  hardening as the M30 retention timer (PrivateTmp, ProtectSystem=strict,
  ProtectHome=read-only, ReadWritePaths=/var/lib/jabali-backups,
  RESTIC_CACHE_DIR redirected under ReadWritePaths).
- Admin-only REST endpoints; no /me equivalents — destinations and
  schedules are operator concerns, not tenant concerns.

### Forward-compat
- Per-destination password rotation (M30.2): add a `password_ref`
  column and switch `restic copy --to-password-file` to per-destination.
- Per-destination retention (M30.2): the per-schedule keep_* override
  fields are already present in `backup_schedules` for forward-compat;
  v1 only honors the server-default values.
- Bandwidth limits per destination (future): restic doesn't expose
  this; would require traffic shaping outside restic (tc / cgroup
  IO weight per transient unit).

## Notes

- v1 implementation deliberately minimal: scheduler / finalizer / copy
  worker are independent goroutines, no shared state, no leader
  election. A future multi-host control plane (M40+) would need
  leader election before scaling these tickers across replicas.
- The `backup_copy_jobs` table records bytes_copied as an UNSIGNED
  BIGINT, but v1 always writes 0 — restic's `copy` doesn't yet emit
  a JSON summary the wrapper can parse. This is observability debt,
  not correctness debt; surfaced as TODO in the runbook.
- "Run now" on a schedule advances `next_run_at` to NOW and lets the
  next 60s tick fire it. We don't bypass the agent's per-(kind,user)
  concurrency lock — concurrent runs return 409 Conflict the same
  way the manual button does.
