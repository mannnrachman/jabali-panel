# ADR-0075 — Backup & Restore: restic-backed, single shared repo

**Status:** accepted
**Date:** 2026-04-28
**Supersedes:** none
**Superseded by:** none

> **Numbering note:** drafted as ADR-0074. Renumbered to 0075 after rebasing onto `origin/main` 2026-04-28, where the M31 lazy-service-alert-suppression ADR had taken 0074 (see commit 183ae18 resolving an earlier 0067 collision).

## Context

Jabali shipped through M33 without a first-class backup/restore story.
Operators can shell-script `mariadb-dump` + `tar` themselves; that
covers DB and `/home`, but a real cPanel/DA-parity workflow needs the
full bundle (mailboxes, DNS zones, cron, SSH keys, app installs, PHP
config) plus a system-level snapshot that lets us recover the entire
panel onto a clean OS.

A multi-month parked tar.zst plan would have hand-rolled:

- chunked file streaming with per-chunk checksums,
- retention/pruning (delete-before-N-days reaper),
- encryption (gpg or `openssl enc`),
- remote destination support (S3/SFTP later),
- dedup (we'd never have shipped this — too hard).

Every one of those is a feature `restic` already has, written in Go, in
production use for a decade, single static binary that ships on Debian
13. Reusing it removes ~6 months of foundation work and lets M30 focus
on the Jabali-specific glue: per-user serializers, manifest format,
admin UI, system-restore CLI.

## Decision

**M30 uses [restic](https://restic.net/) as the backup substrate.**
Bundle bytes are restic snapshots; metadata + workflow lives in
`backup_jobs` (migration 000084). Operators never touch restic
directly — every flow goes through panel-api / panel-agent.

### Repository topology

- **Single shared repo** at `/var/lib/jabali-backups/repo/`
  (`root:root 0700`). Restic dedups across users (every WordPress
  install shares the core), so per-user repos would burn disk for
  no isolation gain — operators are single-tenant; admin already
  has root.
- **Repo + retention timer both run as root.** The agent writes
  packs as root (it has to traverse `/home/<user>/*`), and restic
  default-creates 0600 files. Running the retention timer as root
  too keeps every restic invocation in one identity and avoids the
  chown chase that earlier jabali-group attempts required (root
  packs locked the jabali user out). Hardening on the timer unit
  (PrivateTmp, ProtectSystem=strict, ReadWritePaths,
  ProtectHome=read-only with `RESTIC_CACHE_DIR` redirected under
  ReadWritePaths) keeps blast radius narrow without needing a
  second user.
- **Server-managed password** at `/etc/jabali-panel/restic-repo.password`
  (root:root 0600). Generated once at install time
  (`openssl rand -base64 32`). Users never see the restic password;
  downloads materialize the snapshot first, then re-tar with zstd.

### Snapshot tagging is the partition

Every snapshot carries:

```
kind=<account_backup|system_backup>
job-id=<ULID>
stage=<home|db|mail|dns|cron|ssh|apps|php|manifest|panel_db|panel_config|service_config|mail_state|tls|security|os_users>
jabali                     # blanket retention scope
user-id=<ULID>             # account_* only
system=<hostname>          # system_* only
```

Each stage = a separate restic snapshot. The `manifest` stage is one
tiny JSON blob linking all stage snapshots from one job. UI list and
retention scope filter on these tags server-side.

### Long-running ops run as systemd-run transient units

Backups can take 30+ minutes on accounts with mailboxes; restores
modify shared system state (nginx, PowerDNS, MariaDB DDL). Running
either as a child of `jabali-agent.service` would die on every
`jabali update` (which restarts the agent). Same M29 carry-over:
worker spawned via `systemd-run --unit=jabali-backup-<job-id>.service`
and survives agent/panel restarts.

### Concurrency gates

- One backup at a time **per (kind, user-id)**. Different users back
  up in parallel; same user gets 409 with running `job_id`.
- One restore at a time **globally** (flock at
  `/var/lib/jabali-backups/.restore.lock`). Two parallel restores
  would race nginx reload + PowerDNS NOTIFY + MariaDB DDL.

### Hard 50 GB account ceiling (logical content)

Pre-pass `du -sb /home/<u>` plus mailbox sizes; refuse above with a
pointer at SFTP+rsync. Operational not technical — backups above 50 GB
take long enough that a streaming sync is the right tool.

### Manifest is schema-versioned

`schema_version: 1` in `manifest.json`, stored as the first object in
the snapshot. Future M30.1 bumps schema; restore refuses unknown
versions.

### System restore is CLI-only in v1

UI for system restore would let an operator wipe their running panel
mid-day with one click. Bare-metal recovery flow:

1. Fresh OS install
2. `bash <(curl install.sh)`
3. `jabali system restore --remote-url=… --snapshot=…`

Live in-place system restore is hidden behind `--force` — operator
who ran that flag knew what they were doing.

## Alternatives considered

### Hand-rolled tar.zst (the original plan)

- **Pros:** zero new dependencies; full control over format.
- **Cons:** every restic feature (dedup, encryption, retention, remote
  backends) becomes our problem. Ship date for parity with restic:
  ~6 months. Maintenance: ours forever.
- **Verdict:** rejected. Reinventing a battle-tested tool is the
  opposite of what M30 needs.

### borg

- **Pros:** dedup + encryption; mature.
- **Cons:** Python, not a single binary; remote backends are a separate
  daemon (borg serve) — restic's S3/B2/SFTP support is built in.
  Restic also has better Go integration for the wrapper.
- **Verdict:** rejected.

### duplicity / duply

- **Pros:** S3 + GPG encryption out of the box.
- **Cons:** chain-of-incrementals model — losing one full snapshot
  invalidates every later increment. Restic's content-addressed model
  doesn't have this fragility.
- **Verdict:** rejected.

### rsnapshot

- **Pros:** simplest tool (just rsync + hardlinks).
- **Cons:** no encryption, no remote support without per-host setup,
  no deduplication beyond identical-files.
- **Verdict:** rejected.

### Per-user restic repos

- **Pros:** strict isolation; theoretical multi-tenant story.
- **Cons:** kills dedup — the biggest reason to pick restic. Operators
  are single-tenant in v1; admin already has root, so per-user repo
  passwords add no real isolation. Multi-tenant work, if it ever
  happens, can layer per-user repos on top.
- **Verdict:** rejected for v1; revisit if multi-tenant lands.

### Hand the user a restic snapshot ID for download

- **Pros:** zero copy on the server.
- **Cons:** users would need restic installed locally + the repo
  password (which we explicitly never expose). Anti-portability.
- **Verdict:** rejected. Materialize-then-tar gives users a portable
  tar.zst they can extract anywhere with stock tools.

## Consequences

### Operational

- New apt dependency: `restic` (Debian 13 ships 0.16; install.sh pins
  the floor at 0.16).
- New disk path: `/var/lib/jabali-backups/repo/`. Operator should
  monitor disk usage; native restic retention prunes daily but a
  large account_full backup still bursts the dataset.
- New password file: `/etc/jabali-panel/restic-repo.password`.
  Operators must back this up to safe storage out-of-band; losing it
  means losing every snapshot in the repo. Documented in the M30
  runbook (Step 1 + Step 2).
- New systemd timer: `jabali-backup-retention.timer` (daily 04:30)
  runs `jabali backup retention apply` which calls
  `restic forget --keep-daily/--keep-weekly/--keep-monthly --prune`
  with values from `server_settings`.

### Schema

- Migration 000084: `backup_jobs` table (workflow rows; bundle bytes
  live in restic).
- Migration 000085: `server_settings` retention knobs +
  `backup_remote_url` / `backup_remote_credentials_ref` (M30.1 hooks).

### Security

- Repo dir is root:root 0700; only root can read.
- Password file 0600 root:root; only root can read.
- Worker → panel-api status callback uses an HMAC at
  `/etc/jabali-panel/backup-worker.secret` (Step 6) — same M14
  defense-in-depth pattern (see ADR-0056).
- Restic's AES-256-GCM repo encryption means a stolen disk image
  without the password is recoverable only in theory.

### Forward-compat

- `manifest_json.schema_version` lets us extend the manifest in
  M30.1+ without breaking older restores; restore refuses unknown
  versions rather than guessing.
- `backup_remote_url` empty in v1; M30.1 enables S3/SFTP/B2 by
  populating it + `backup_remote_credentials_ref` (pointer at
  `/etc/jabali-panel/restic-remote.env`).

## Notes

- Step 1 (this commit) lays foundation only: schema + dirs + restic
  install + retention timer + this ADR + BLUEPRINT update.
- Step 2 is the wave gate: it pins repo location (already pinned
  here), password lifecycle (pinned here), manifest schema, snapshot
  tagging convention. Steps 3-12 build against Step 2's contract.
- Future work tracked under `plans/m30-backup-restore.md`. Out of
  scope for v1: scheduled backups (M30.1), remote destinations
  enabled (M30.1), per-user repos (multi-tenant), `restic check`
  command surfacing (follow-up), cPanel/DA tarball restore (M15).
