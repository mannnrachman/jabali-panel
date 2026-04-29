# M35 — Migration importers (cPanel / DirectAdmin / HestiaCP / WHM / IMAP)

**Goal.** Operator points jabali2 at an existing cPanel/DirectAdmin/HestiaCP/WHM
host (or a single tarball produced by one of those panels) and lands a
working user account on the jabali host: home tree, mailboxes, MariaDB
databases, DNS zones, cron, applications, mail forwarders/autoresponders.
A separate IMAP-sync mode handles mail-only migrations from any
IMAP-speaking provider (Google Workspace, Plesk, Microsoft 365, etc.).

This is the strongest signal in the old jabali-panel issue tracker
(#2 HestiaCP, #14 IMAP Sync, #60 cPanel transfer, #71 WHM UAPI). The work
was originally scoped as M15 in `docs/BLUEPRINT.md` but the M15 number
was reused (and shipped) for DNSSEC. M35 takes a fresh number to avoid
review-time confusion.

Branch: `m35/migration-importers`. Default mode: branch + ff-merge into
`main` after every step.

ADR target: **0085** (next free above ADR-0084 per-user-egress-firewall;
prior reservation of 0079 collided with M33.2 mail-yara-async-scanner).

Migration high-water-mark on main: 000102 (post-M34). M35 takes
000103..000105. If a parallel milestone lands a migration before M35
ships, the wave-1 step renumbers to the next free contiguous range.

**MariaDB FK collation requirement:** every CREATE TABLE Step 1 introduces
that has a FOREIGN KEY back to `users(id)` MUST declare both
`DEFAULT CHARSET=utf8mb4` AND `COLLATE=utf8mb4_unicode_ci`, AND ULID
columns referencing users.id MUST be `CHAR(26)` (not `VARCHAR(26)`).
M34 shipped without these and crashed every fresh install with
errno 150 "Foreign key constraint is incorrectly formed" until
b336d856 + 10569464. Reference patterns: 000037_create_cron_jobs,
000095_create_backup_schedule_users.

## Why now

- 4+ explicit user requests in the old issue tracker.
- Hosting market expectation: every competing panel ships migration
  (cPanel/WHM has its own; Plesk has Plesk Migrator; HestiaCP has
  v-restore-user; CloudPanel has cloud-migration). jabali2 today has
  nothing — operators have to copy files by hand and rebuild every
  account.
- Without migration we cannot land prospects who already run another
  panel. Every sales conversation hits the same wall.

## Constraints + invariants

- **Source-side discovery is read-only.** We never run mutating commands
  on a source panel. cPanel: SSH + tar + UAPI read calls. DirectAdmin:
  SSH + `da admin user.list` + `da admin user.show`. HestiaCP:
  `v-list-user` + `v-list-user-package`. WHM: UAPI + `pkgacct` (which
  produces a tarball — read-only).
- **Single-user transfer is the unit.** "Migrate 200 cPanel accounts"
  = run the importer 200 times. Each run is independent, idempotent,
  resumable; one failure does not block the next.
- **Four-pipeline pattern per source panel.** analyze → fix-perms →
  validate → restore. analyze produces a report (no host changes);
  fix-perms shimmies file ownership/perms before importing; validate
  dry-runs the import (would-create lists, conflicts, quota
  projections); restore is the actual mutation.
- **No PostgreSQL in v1.** cPanel + WHM tarballs may include PG dumps;
  we record them in the manifest with `skipped: postgres_unsupported`
  and let M37 PostgreSQL parity import them later. cPanel has very
  few PG users; this is acceptable cost.
- **Mailbox import is JMAP push, not file copy.** We do not write
  Stalwart's RocksDB by hand. Source panel's Maildir or mbox is
  parsed, then re-injected via `stalwart-cli` JMAP import OR direct
  IMAP APPEND when the source is reachable over network.
- **Reuse existing models.** A migrated account is a normal user from
  jabali2's POV. No "imported_user" flag, no separate table. The
  migration record (source, status, errors) lives in
  `migration_jobs` and links to the created `users` row by user_id.
- **Resumable.** Every stage records status to `migration_stages`
  (pending/running/done/failed). Rerun continues from the failed
  stage; already-done stages no-op.
- **systemd-run transient unit per job.** Same pattern as M29 + M30.
  A long-running 30-minute migration that dies on `jabali update`
  reboot is unacceptable.
- **One migration at a time per (source-host, source-username).** Two
  parallel attempts to migrate the same source user race on the same
  files. DB rows enforce.

## Wave gate (Step 2 = source-discovery contract + manifest schema)

Step 1 = foundation (DB tables + ADR + dirs + UI shell). Step 2 = wave
gate: pin the discovery API contract every importer implements + the
manifest.json schema each transfer produces. Step 2 lands and is
reviewed before Steps 3-9 dispatch.

Wave A (3, 4): cPanel + DirectAdmin importers in parallel — independent
discovery + restore code paths.

Wave B (5, 6): HestiaCP + WHM-pkgacct importers in parallel — different
source format from cPanel/DA.

Wave C (7): IMAP sync importer (mail-only fallback for any source).

Wave D (8, 9): admin UI + runbook + E2E.

## Steps

### Step 1: foundation — DB schema, dirs, ADR-0085

**Files:**
- `panel-api/internal/db/migrations/000103_create_migration_jobs.{up,down}.sql`
- `panel-api/internal/db/migrations/000104_create_migration_stages.{up,down}.sql`
- `panel-api/internal/db/migrations/000105_server_settings_migrations.{up,down}.sql`
- `panel-api/internal/models/migration_job.go`
- `panel-api/internal/repository/migration_job_repository.go` (+ tests)
- `install.sh`: provision `/var/lib/jabali-migrations/` (root:jabali 0750)
  + the staging tmpfs directory `/run/jabali-migrations/<job-id>/`
- `panel-api/cmd/server/migrate_run_cmd.go` — cobra subcommand the
  transient unit invokes
- `docs/adr/0085-migration-importers.md`

Each CREATE TABLE that adds a FOREIGN KEY (target_user_id → users.id, or
the migration_stages.job_id → migration_jobs.id self-FK) MUST end with:
`ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;`
ULID columns referencing users.id MUST be `CHAR(26)` (not VARCHAR(26)).
This is a hard precondition: skipping it crashes every fresh install
with errno 150 (M34 scar; ref b336d856 + 10569464).

`migration_jobs` shape:
```sql
CREATE TABLE migration_jobs (
  id              CHAR(26) NOT NULL,            -- ULID, doubles as job id
  source_kind     ENUM('cpanel','directadmin','hestiacp','whm_pkgacct','imap_only') NOT NULL,
  source_host     VARCHAR(255) NOT NULL,
  source_user     VARCHAR(64) NOT NULL,
  target_user_id  CHAR(26) NULL,                -- NULL until restore stage runs
  state           ENUM('pending','analyzing','fix_perms','validating',
                       'restoring','done','failed','cancelled') NOT NULL,
  started_at      DATETIME(0) NOT NULL,
  ended_at        DATETIME(0) NULL,
  manifest_json   LONGTEXT NULL,                 -- final manifest after restore
  last_error      TEXT NULL,
  PRIMARY KEY (id),
  UNIQUE KEY uq_migration_source (source_host, source_user, source_kind),
  KEY idx_migration_state (state)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
```

`migration_stages` records the per-stage state for resumability:
`(job_id, stage_name, state, started_at, ended_at, error, bytes_processed)`.

server_settings additions (default off; operator enables migration UI
when their team is ready):
- `migrations_enabled` (default false)
- `migrations_concurrent_per_source` (default 2)
- `migrations_imap_concurrent_folders` (default 4)

**Verify:** migration up + down on a throwaway DB; install.sh fresh +
idempotent (re-run = no-op); `/var/lib/jabali-migrations/` exists with
correct owner/mode.

### Step 2: WAVE GATE — source-discovery contract + manifest schema

**Files:**
- `internal/migrate/discover.go` — interface every importer implements
- `internal/migrate/manifest.go` — manifest.json schema (schema_version=1)
- `internal/migrate/stage.go` — stage definitions + transition rules

Discovery contract (every source kind implements):
```go
type Discoverer interface {
    Connect(ctx, host, user, secret) (Session, error)
    ListAccounts(s Session) ([]AccountSummary, error)
    DescribeAccount(s Session, accountID string) (*AccountManifest, error)
    Close(s Session) error
}

type AccountManifest struct {
    SchemaVersion int
    Source struct{ Kind, Host, User string }
    Sizes struct{ Home, DBs, Mail, Logs int64 }
    Domains      []DomainSpec
    Mailboxes    []MailboxSpec
    Databases    []DatabaseSpec
    DNSZones     []DNSZoneSpec
    Cron         []CronSpec
    SSH          []SSHKeySpec
    Apps         []AppSpec      // detected WordPress/Joomla/Drupal installs
    Warnings     []Warning
}
```

Stage transitions:
- `pending → analyzing` on first invocation
- `analyzing → fix_perms` after manifest produced
- `fix_perms → validating` after perms pass
- `validating → restoring` after dry-run reports zero blocking conflicts
- `restoring → done` on success; `restoring → failed` on any unrecoverable error

**Wave gate decision: dispatcher reviews this step.** Specifically: the
Discoverer interface signature, the AccountManifest field set, the
manifest.json on-disk schema, the stage state machine. Steps 3-9 build
on all four.

### Step 3 (Wave A): cPanel importer

**Files:** `internal/migrate/cpanel/{discover,restore,fixperms,validate}.go`

cPanel specifics:
- Discovery: `whmapi1 listaccts user=<u>` + `cpapi2 user.list_users` +
  per-user UAPI `Email::list_pops`, `Mysql::list_dbs`, `DNSSEC::*`,
  `Cron::list_cron`, `SSH::list_keys`.
- Backup: invoke `pkgacct <user>` on the source host, scp the
  resulting `cpmove-<user>.tar.gz` into `/run/jabali-migrations/<job>/`.
  Avoid pkgacct's "compressed" mode (older cPanel ships gzip not
  zstd; tarball can be 2x larger but shorter pull time).
- Restore parsing: tarball's `cp/<user>` dir contains everything in
  named subdirs. Walk → write into jabali2's models.

### Step 4 (Wave A): DirectAdmin importer

**Files:** `internal/migrate/directadmin/...`

DA specifics:
- Discovery: `da-internal/sock` + `da_get_user.sh <user>`.
- Backup: `da admin tools.system_backup_user <user>` produces a tarball
  similar to DA's `user_backup_*.tar.gz`. Pull via SSH.
- DNS lives in `/var/named/<domain>.db` (BIND zone files). Parse +
  upsert into PowerDNS via existing pdns API.

### Step 5 (Wave B): HestiaCP importer

**Files:** `internal/migrate/hestiacp/...`

Hestia specifics:
- Discovery: `v-list-user <u> json` + `v-list-user-domains` +
  `v-list-databases`.
- Backup: `v-backup-user <user>` produces a `.tar` under
  `/backup/`. Pull via SSH.
- Tar internals: `web/<domain>/`, `mail/<domain>/<user>/Maildir`,
  `db/<dbname>.sql`. Easier to parse than cPanel because Hestia uses
  predictable subdirs.

### Step 6 (Wave B): WHM-pkgacct importer (file-only, no live source)

**Files:** `internal/migrate/whm/...`

Use case: operator gets a `cpmove-<user>.tar.gz` produced by another
person's WHM and uploads it to jabali2 directly. No SSH to the source.

- Upload via existing M30 backup-upload path or new
  `/admin/migrations/upload`.
- Reuses Step 3's tarball parser (cPanel restore is shared code).

### Step 7 (Wave C): IMAP sync importer (mail-only)

**Files:** `internal/migrate/imapsync/...`

Use case: source is Plesk / Microsoft 365 / Google Workspace / arbitrary
IMAP. Operator only needs mail moved.

- Wrap `imapsync` (Perl, well-maintained, MIT) as an agent command.
- For each source mailbox: `imapsync --host1 ... --user1 ... --host2
  127.0.0.1 --user2 ... --useheader Message-ID`.
- Concurrent folder workers: `migrations_imap_concurrent_folders`
  setting from Step 1.
- Resumable via imapsync's own `--cache` machinery + the migration
  stages table.

### Step 8 (Wave D): admin UI

**Files:**
- `panel-ui/src/shells/admin/migrations/MigrationsPage.tsx`
- `panel-ui/src/shells/admin/migrations/StartMigrationDrawer.tsx`
- `panel-ui/src/shells/admin/migrations/MigrationLogsPanel.tsx`

UI flow:
1. New Migration Drawer: choose source kind → connect form (host +
   credentials OR upload tarball) → list-accounts step → pick user(s)
   → review manifest summary → start.
2. Migrations table: filterable by source_kind / state, per-row
   "View logs", "Cancel", "Resume from failed stage".
3. Per-job detail page: stage timeline, byte counters, warnings list,
   manifest download.

### Step 9 (Wave D): runbook + E2E + memory entry

`plans/m35-migration-importers-runbook.md` covers:
- credential handling (per-source secrets in /etc/jabali-panel/migration-secrets/)
- recovery (which manifest fields survive restore failure; how to
  resume from each stage)
- known-source-version compatibility table (we test against cPanel
  126.x, DA 1.66.x, Hestia 1.9.x, plus pkgacct format from WHM 11.x)
- IMAP-sync edge cases (Gmail throttling, Microsoft 365 modern auth)

E2E: a Docker-based source fixture per panel kind (cpanel-source-fixture,
da-source-fixture, hestia-source-fixture). The fixtures pre-seed one
account each; test runs the full pipeline against a fresh jabali2
install and asserts target state.

## Out of scope

- PostgreSQL data import — depends on M37 PostgreSQL parity. cPanel/WHM
  tarballs containing PG dumps record `skipped: postgres_unsupported`
  in the manifest; resume is possible after M37 ships.
- Plesk migration — operator can use Plesk's own export then run our
  cPanel importer on the resulting tar (Plesk supports a cPanel-format
  export). Native Plesk source = future M35.x.
- Multi-account batch import UX — v1 is single-account. Operators
  scripting "for u in users; do jabali migrate ..." is acceptable for
  the first release.
- DNS TTL coordination during cutover — operator's responsibility to
  lower TTL before migration day; the importer does NOT auto-edit
  source DNS.
- Reverse migration (jabali2 → cPanel) — unidirectional in v1.
