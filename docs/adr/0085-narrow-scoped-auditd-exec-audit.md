# ADR-0085 — Narrow-scoped auditd as L3 forensic exec audit (M39)

**Status:** Accepted
**Date:** 2026-04-30
**Supersedes:** the L3 forensic audit role of the M33 Tetragon stack
(see ADR-0072 amendment 2026-04-30).
**Related:** ADR-0072 (malware detection stack), ADR-0084 (per-user
PHP-FPM egress firewall — kernel-layer enforcement at network plane).

## Context

After M33, the malware stack covered the user-docroot tier (LMD
inotify + YARA-X + signature-base) and the network plane (M34 nft +
cgroup v2). The remaining tier — "what binary did user X exec, when?"
— was supposed to be filled by Tetragon. In practice, Tetragon was a
poor fit for shared PHP hosting (see ADR-0072 amendment for the long
list); the L3 audit promise was never delivered (the relay-shim that
was meant to ingest Tetragon's JSON log was deferred indefinitely).

We need a replacement that:
- Runs on every Debian 13 host without BTF gymnastics.
- Tags events per real user (not per kernel thread / daemon).
- Has a high signal-to-noise ratio on a workload that legitimately
  spawns ImageMagick, Composer, wp-cli, Let's Encrypt probes etc.
- Is read-only forensic — operator triages from the panel UI.

## Decision

**auditd**, narrow-scoped to **suspicious-binary execve only**, with
a single tag key for ausearch pivots.

### Rule shape

`/etc/audit/rules.d/jabali-exec.rules` ships 11 rules — one per
suspicious binary — each with:

- `arch=b64` (Debian 13 64-bit only).
- `-S execve` syscall match.
- `-F path=<binary>` exact-path match (no wildcards; suspicious-binary
  list is closed-set, see below).
- `-F auid>=1000` to exclude system daemons.
- `-F auid!=4294967295` to exclude pre-PAM kernel threads.
- `-k jabali_susp_exec` (or `jabali_susp_exec_phpcli` for /usr/bin/php)
  — single key for `ausearch -k` / `aureport -k` pivots.

### Closed-set binary list

`/bin/{bash,sh,dash}` shells, `/usr/bin/{wget,curl,nc,ncat,socat}`
post-exploit network tools, `/usr/bin/{python3,perl}` interpreter
post-exploit toolkits, `/usr/bin/php` CLI (separate tag —
distinguishes legit cron from webshell-spawned PHP).

### NOT auditing blanket execve

`-a always,exit -S execve` without path filters is the standard
"audit everything" mistake. WordPress ImageMagick alone produces
thousands of execve events per minute. The closed-set + auid filter
is what makes the rule survivable on a busy LAMP host.

### Storage + read path

auditd's audit.log is the storage. Two agent commands shell out to
`ausearch -k jabali_susp_exec --raw`:

- `security.audit.recent` — most recent N events (default 100).
- `security.audit.by_user` — same, filtered by `-ua <auid>`.

panel-api forwards `/admin/security/malware/audit/recent` to the
recent command. panel-ui surfaces an "Exec audit" sub-tab on the
Malware tab — read-only AntD Table, 60s auto-poll, no notifications.

### NOT real-time alerting in v1

Operators read the ExecAudit card during incident response, not at
03:00. An M14 burst-source (`exec.audit.burst`) is sketched in the
plan as Step 7 but explicitly deferred until operator demand surfaces.

## Alternatives considered

### Blanket `-S execve`

Rejected — log volume on a busy LAMP host overwhelms the operator and
fills disk. Tetragon's default policies failed for the same reason.

### Tetragon (status quo before M39)

Removed — see ADR-0072 amendment for the full reasoning. Short
version: k8s-shaped, BTF-fragmented, default policies are noise
cannons, relay-shim deferred indefinitely.

### bpftrace one-shots

Useful as a complementary operator tool for ad-hoc forensic
spelunking, but doesn't surface in the panel UI and requires the
operator to know what to look for in advance. Keep available for
power users; not the default path.

### AppArmor

Out of scope for M39. AppArmor's job is enforcement (deny path/cap)
not audit (record-then-pivot). Daemon-confinement AppArmor profiles
are M40 (separate blueprint). User-PHP AppArmor was rejected on the
same FP-cliff grounds that killed Snuffleupagus and Tetragon defaults.

### sysmon / falco

Heavier daemon model. falco would duplicate maldet's inotify watch
+ Tetragon's eBPF surface. We already pay for both — adding a third
event-stream integrator pulls the malware stack back toward the
"installed-but-unused" failure mode that doomed Tetragon.

## Consequences

### Positive

- **Mature tooling.** auditd has been in mainline Linux since 2003;
  ausearch/aureport are operator-friendly and work without a panel.
- **No BTF dependency.** Runs on every Debian 13 cloud image.
- **Per-user via auid.** `auid` (loginuid) is set at PAM login and
  inherited by every child process; PHP-FPM child execs carry the
  pool's auid, not the daemon's. Answers "what did user X exec?"
  cleanly.
- **Closed-set list is fast to evolve.** Adding a binary is one line
  in `/etc/audit/rules.d/jabali-exec.rules` + `augenrules --load`.
  No CRD, no policy compiler, no eBPF program rebuild.

### Negative

- **Loses syscall-class breadth.** Tetragon could (in theory) catch
  weird ptrace / setns / chmod-x-on-docroot patterns. We only catch
  exec of named binaries. Accepted because we never used the breadth
  in practice — the relay-shim was deferred and the events were
  unread.
- **No real-time alerting.** Forensic-only by design; operator
  triages from the UI. M14 burst-source is deferred.
- **Path-based, not content-based.** A renamed `cp /bin/bash /tmp/x &&
  /tmp/x` evades the rule. Mitigation: maldet's inotify already
  watches user docroots for files getting +x bits. Layered defence.

### Risks accepted

- **audit.log fills disk on a runaway loop.** auditd's default
  `audisp-rotate` keeps 5 × 8 MB; operator can lift via
  `/etc/audit/auditd.conf`. Documented in runbook.
- **ausearch on a busy host can take seconds.** Agent command is
  bounded to 15s context; on a degraded host the UI shows a timeout
  and the operator falls back to direct SSH.

## Implementation

- `install.sh install_audit_exec()` writes
  `/etc/audit/rules.d/jabali-exec.rules`, calls `augenrules --load`,
  enables auditd. Idempotent — only re-renders + reloads if checksum
  changed.
- panel-agent `internal/commands/security_audit.go`:
  - `mwAuditRecentHandler` (security.audit.recent).
  - `mwAuditByUserHandler` (security.audit.by_user).
- panel-api `/api/v1/admin/security/malware/audit/recent`.
- panel-ui `ExecAuditCard` on the Malware sub-tabs.

### Live verification

End-to-end on 192.168.100.150 (target after M39 ship):
- `auditctl -l | grep jabali_susp_exec | wc -l` = 11.
- Real-user shell spawn produces a `key="jabali_susp_exec"` line
  with `auid=<user-uid>`.
- WordPress wp-cron over 5 minutes produces 0 rows (no FP).
- Webshell-style spawn (PHP invoking curl from a docroot) produces
  1 row tagged with the user's auid.
