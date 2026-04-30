# ADR-0080: Per-destination backup model (M30.2)

**Status:** ACCEPTED (2026-04-30).
**Supersedes:** ADR-0078 (M30.1 source-then-mirror).

## Context

ADR-0078 shipped a "local-first, copy-async" pipeline: every backup
landed in `/var/lib/jabali-backups/repo` then `restic copy` mirrored
the snapshot to each linked destination via a queue + transient-unit
worker. Two operational pain points surfaced on the first live VPS
deployment:

1. **Operators don't want a forced local copy.** A single-destination
   schedule pointed at SFTP still wrote (and retained) snapshots on
   the panel host's disk. For a small VPS that's a 50% disk-space tax
   on every backup. Operators picking a remote destination expect the
   backup to go ONLY there.
2. **The copy worker spawn path was broken in production.** panel-api
   runs as the `jabali` user; Polkit's `manage-units` action denies
   that uid. `systemd-run` returned `Access denied` on every spawn —
   the local snapshot succeeded but never mirrored. We patched it by
   routing the spawn through panel-agent (root), but the underlying
   complexity (queue + transient unit + per-row retry/backoff +
   separate `jabali backup copy run` CLI) was load-bearing only
   because of the local-first assumption.

The user feedback was direct: *"if i want local i would select local.
of course pure remote or pure local."*

## Decision

**Each backup writes directly to ONE destination. Schedules with N
destinations enqueue N independent backup_jobs (one per destination
per user).** No source repo, no mirror, no copy worker.

`backup_jobs.destination_id` is now a first-class field. The agent's
`backup.create` / `system.backup` accept `repo_url` + `credentials_ref`
+ `extra_options` + `destination_kind` + optional `sftp` block, and
the wrapper threads these into every restic invocation in the
orchestrator (home / db / mailboxes / metadata / manifest).

A schedule with 0 destinations is a no-op (logged + next_run_at
advanced). The UI requires ≥1 destination at create/edit time.

## Consequences

### Operational
- **Disk on panel host shrinks dramatically** when no destination is
  local. The previous always-local behavior is recoverable by adding
  a `local` destination to the schedule.
- **Agent socket holds longer** when the only destination is a slow
  remote. Backup duration = `restic backup` over SFTP/S3 wall-clock,
  no longer "fast local then async copy". The orchestrator still has
  a 90-min ceiling; very large home dirs over slow links must use a
  faster backend or accept that ceiling.
- **N destinations = N restic operations reading the source files N
  times.** No dedup across destinations. This is the explicit
  trade-off vs Option A (one local + N copies). For multi-destination
  fan-out where dedup matters, future M30.3 may reintroduce a
  one-destination-as-source pattern with explicit operator opt-in.

### Schema
- Migration 000104: `backup_jobs.destination_id CHAR(26) NULL` +
  index. NULL on legacy pre-M30.2 rows; required on every new row.
- Migration 000105: `DROP TABLE backup_copy_jobs`. The model + repo
  + worker package + `backup_copy_spawn` agent command + `jabali
  backup copy run` CLI are deleted.

### Retention
`jabali backup retention apply` now iterates **(schedule, destination)
pairs**: `restic --repo <dest.url> forget --tag schedule-id=<sched.id>
--keep-*` per pair, then a single `restic prune --repo <dest.url>` per
destination (deduped at the end). Manual backups (ScheduleID NULL)
remain never-pruned.

### Self-heal
The agent runs `ssh ... mkdir -p <path>` (SFTP only) + `restic init`
at the start of every backup if `restic snapshots` reports the repo
doesn't exist. Same self-heal as the Test button. Operators can hit
"Run now" against a brand-new SFTP destination; the first backup
creates the remote directory.

### Removed code
- `panel-api/internal/backupcopyworker/` (package).
- `panel-api/internal/repository/backup_copy_job_repository.go`.
- `panel-api/internal/models/backup_copy_job.go`.
- `panel-api/cmd/server/backup_copy_cmd.go`.
- `panel-agent/internal/commands/backup_copy_spawn.go`.
- `backup_copy_jobs` table + index + foreign keys.
- Finalizer copy enqueue path.
- `/admin/backups/:job_id/copy-jobs` REST endpoint.

### UI
- Schedule edit drawer requires ≥1 destination (validation rule).
- "Create backup" drawer adds Destination select; falls back to the
  single enabled destination if exactly one exists.
- Backups table shows the Destination column on child rows.

## Notes

- ADR-0078 is **superseded** but kept in the tree for historical
  context. Anyone reading old git blame should land here for the
  current model.
- The `local` destination kind is preserved for operators who want
  the previous always-local behavior; nothing structural is lost.
- Per-destination passwords (M30.2 forward-compat hook in 0078) are
  still future work; the single shared `/etc/jabali-panel/restic-
  repo.password` is reused for every destination.
