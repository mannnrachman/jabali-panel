# ADR-0094: Migration importers (cPanel / DA / Hestia / WHM / IMAP)

**Status:** Proposed
**Date:** 2026-05-09
**Supersedes:** —

## Context

Operators evaluating jabali2 today cannot land prospects who already
run cPanel, DirectAdmin, HestiaCP, or WHM. The hosting market expects
one-click migration from any competing panel; without it every sales
conversation hits the same wall. The old jabali-panel issue tracker
recorded 4+ explicit user requests for this exact feature
(#2 HestiaCP, #14 IMAP Sync, #60 cPanel transfer, #71 WHM UAPI).

This ADR pins the architectural decisions that the M35 9-step
blueprint at `plans/m35-migration-importers.md` builds on. Steps 1-9
implement; this ADR captures the *why* so future readers don't have
to reverse-engineer the constraints from the code.

## Decision

### 1. Read-only on the source. Always.

Every importer talks to its source panel through read calls only:
cPanel UAPI list-mode, `whmapi1`, DA's `da admin user.show`, Hestia's
`v-list-*`, WHM's `pkgacct` (which writes a tarball but mutates no
panel state). We never run a command that creates or edits accounts
on the source side. Operators retain a clean rollback story: if our
import goes sideways, the source is untouched.

### 2. Single-user transfer is the unit.

"Migrate 200 cPanel accounts" is 200 independent runs of the importer.
Each run owns one (source-host, source-user, source-kind) tuple,
recorded in `migration_jobs`. The UNIQUE on that tuple prevents two
parallel attempts from racing on the same files. Operators wanting a
bulk move script `for u in users; do jabali migrate ...; done` —
multi-account batch UX is deliberately out of scope for v1.

### 3. Four-pipeline-stage pattern per importer.

`analyze → fix-perms → validate → restore`. analyze produces a
report (no host changes). fix-perms shimmies file ownership/perms
before importing. validate dry-runs the import (would-create lists,
conflicts, quota projections). restore is the only mutation step.
Stages are recorded in `migration_stages` so resume after a mid-run
failure picks up where the previous attempt died.

Rejected alternative: monolithic "do it all" command. Rejected because
the most common failure mode (we discovered this in cPanel 124.x
rehearsal) is a permission bug on a single home subdir that takes 30
seconds to diagnose. Without stages we'd have to re-pull the entire
account every retry.

### 4. systemd-run transient unit per job.

Same pattern as M29 + M30. A 30-minute migration that dies on
`jabali update` reboot is unacceptable. The transient unit has its
own cgroup so we can kill one job without touching the panel.

### 5. No PostgreSQL data import in v1.

cPanel + WHM tarballs may include PG dumps. We record them in the
manifest with `skipped: postgres_unsupported` and let M37 PostgreSQL
parity import them later. cPanel has very few PG users in practice;
acceptable cost. ADR-0091 (PostgreSQL parity phase 1) captures the
follow-up.

### 6. Mailbox import is JMAP push, not RocksDB hand-write.

Source panel's Maildir or mbox is parsed, then re-injected via
`stalwart-cli` JMAP import OR direct IMAP APPEND when the source is
network-reachable. Writing Stalwart's RocksDB by hand would lock us
to a specific Stalwart minor version; JMAP is stable across versions
and is what the operator-facing API speaks anyway.

### 7. Migrated accounts are normal users.

The `users` row a migration creates is byte-identical to one a human
would create through the admin UI. No `imported_user` flag, no
shadow table. The migration record (source, status, errors) lives in
`migration_jobs` and links via `target_user_id`. Subsequent edits go
through the normal admin UI; the migration record becomes a
historical breadcrumb.

### 8. Resumable from any failed stage.

`migration_stages` records `(job_id, stage_name, state, started_at,
ended_at, error, bytes_processed)`. Resume scans for state in
`{'pending','failed'}` ordered by created_at and re-dispatches.
Already-done stages no-op. The state machine in
`internal/migrate/stage.go` (Step 2 wave gate) pins legal transitions.

### 9. One migration at a time per natural key.

Two parallel attempts to migrate the same source user race on the
same files. The UNIQUE on `(source_host, source_user, source_kind)`
in `migration_jobs` is the row-level enforcement; the admin REST
returns a 409 conflict on duplicate.

## Consequences

### Positive

- Every importer follows the same shape — one new source kind = one
  new package under `internal/migrate/<kind>/`, no schema changes,
  no admin-UI changes.
- Operator-facing failure messages are stage-scoped ("fix_perms
  failed on /home/user/.cpanel" beats "migration failed").
- Test surface is bounded: per-importer Docker fixtures (Step 9)
  cover the full pipeline against a frozen source-panel version.

### Negative

- Resume after a mid-stage crash needs care: every importer must be
  idempotent on its own re-runs. We pay this cost up front in code
  review (Step 2 wave gate enforces) rather than in production
  surprises.
- IMAP-sync mode is mail-only by design — operators wanting full
  account moves from arbitrary IMAP sources still need a cPanel /
  DA / Hestia / WHM source. Acceptable v1 scope.

### Tracked risks

- Source-panel API drift: cPanel UAPI has broken between major
  versions before. Mitigated by the runbook's per-source-version
  compatibility table (Step 9).
- Per-source credential exposure: stored at
  `/etc/jabali-panel/migration-secrets/<job-id>.env` (root:jabali
  0640). **Wipe shipped 2026-05-09 in `89b0da31`:**
  `jabali-migration-secrets-reap.timer` runs `jabali migrate
  reap-secrets` daily 04:30 UTC + 15-min jitter; the cobra
  subcommand walks `migration_jobs WHERE state IN
  ('done','failed','cancelled')` + `os.Remove`s the matching
  `.env` file. Operator can also invoke on demand. Service is
  hardened with `ProtectSystem=strict` +
  `ReadWritePaths=/etc/jabali-panel/migration-secrets`.

## References

- Blueprint: `plans/m35-migration-importers.md`
- Schema: migrations 000120–000122
- Foundation Step 1: this ADR + DB schema + dirs
- Wave gate Step 2: `internal/migrate/discover.go` interface contract
