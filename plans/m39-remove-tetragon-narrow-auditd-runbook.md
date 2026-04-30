# M39 — Tetragon-removed + narrow auditd runbook

Companion to plans/m39-remove-tetragon-narrow-auditd.md and
ADR-0085. Operator day-2 ops for the auditd narrow exec audit.

## What it is, in one sentence

`/etc/audit/rules.d/jabali-exec.rules` ships 11 narrow `execve`
rules tagged `jabali_susp_exec` (and `jabali_susp_exec_phpcli` for
the PHP CLI specifically), filtered to real users via
`auid>=1000`. ausearch is the read path; the panel surfaces the
last 100 events as a read-only "Exec audit" card on the admin
Malware tab.

## Files / units / commands

| Where | What |
|---|---|
| `/etc/audit/rules.d/jabali-exec.rules` | Generated rule file (root:root 0640). install.sh is the only writer; idempotent re-render on `jabali update`. |
| `auditd.service` | Debian package unit; jabali doesn't ship a custom unit. |
| `/var/log/audit/audit.log` | Storage. Rotated by `audisp-rotate`. |
| `auditctl -l` | List loaded rules. |
| `ausearch -k jabali_susp_exec --start recent` | Recent events. |
| `ausearch -k jabali_susp_exec -ua <auid>` | Filter by login user. |
| `aureport -x --summary` | Top-N executable summary. |

## Daily checks

```bash
auditctl -l | grep jabali_susp_exec | wc -l
# Expected: 11

systemctl is-active auditd
# Expected: active

du -sh /var/log/audit/
# Expected: < 50 MB on a typical host (5×8 MB rotation default)

ausearch -k jabali_susp_exec --start today --summary 2>/dev/null | head -20
```

## Investigating an incident

1. Operator gets a M14 alert (e.g. malware quarantine event for user X).
2. Visit `/jabali-admin/security?tab=malware` → `Exec audit` sub-tab.
3. Filter by username — does user X have a recent `bash` / `curl` /
   `wget` row that doesn't match a known cron schedule?
4. Pivot via SSH:
   ```bash
   ausearch -k jabali_susp_exec -ua $(id -u <username>) --start "1 hour ago"
   ```
5. Cross-reference with maldet quarantine + nft drop counter (M34) +
   nginx access log to build the timeline.

## Tuning the rule list

To add a binary (e.g. `/usr/bin/ruby`):

1. Edit `install.sh` `install_audit_exec()` — add one line to the
   heredoc.
2. `git commit && jabali update` (or copy to host + `augenrules --load`).
3. Verify: `auditctl -l | grep ruby`.

To exclude a binary (rare — the closed-set is small enough that
removal is usually wrong):

1. Comment out the line in install.sh.
2. `jabali update` — re-render replaces the file, dropped rule is
   gone after `augenrules --load`.

## Filtering noisy users

A user with a legitimate shell-heavy workload (developer with SFTP
access running build scripts) can produce hundreds of rows/day. Two
options:

1. **Add a per-auid exclusion** above the suspicious rules:
   ```
   -a always,exit -F arch=b64 -S execve -F path=/bin/bash -F auid=2001 -F exit=0 -F never
   ```
   (auditd evaluates rules top-to-bottom; never-match short-circuits.)

2. **Move the user to SSH-via-bubblewrap (M13)** — the bubblewrap
   wrapper carries its own auid, audit events get filed under the
   sandbox uid, easier to filter.

## Log rotation cap

auditd default: 5 files × 8 MB = ~40 MB.

Edit `/etc/auditd/auditd.conf` if more is needed:
```
max_log_file = 64       # MB per file
num_logs = 10
max_log_file_action = ROTATE
```
`systemctl restart auditd` after change.

## Rollback

If a rule turns out to be too noisy and a fix isn't immediately
available:

```bash
# Disable just the audit rules without touching auditd:
mv /etc/audit/rules.d/jabali-exec.rules{,.disabled}
augenrules --load
```

`jabali update` will re-render the file on next run; rename to
`.disabled` again or fix the rule list in install.sh.

To remove auditd entirely (last resort):
```bash
systemctl disable --now auditd
mv /etc/audit/rules.d/jabali-exec.rules{,.disabled}
```
The "Exec audit" card will show empty + a connection-failed banner;
maldet + nft remain functional.

## Tetragon legacy cleanup

`jabali update` runs `cleanup_tetragon_legacy()` from install.sh on
every host. Manual probe to confirm:

```bash
systemctl status tetragon || true
# Expected: Unit tetragon.service could not be found.

ls /opt/tetragon /etc/tetragon /var/log/tetragon 2>&1 | head
# Expected: each path returns "No such file or directory"

ls /usr/local/bin/tetragon* /usr/local/bin/jabali-tetragon-relay 2>&1
# Expected: each path returns "No such file or directory"
```

If any path persists after `jabali update`, run the install pipeline
again — `cleanup_tetragon_legacy()` is idempotent.

## What's NOT covered

- **Real-time alerting** — forensic-only by design. An M14
  `exec.audit.burst` event source is sketched as Step 7 of the M39
  plan but deferred until operator demand surfaces.
- **AppArmor daemon confinement** — separate blueprint (M40).
- **AIDE FIM for system files** — separate blueprint (M42).
- **Off-host audit log shipping** — `audispd-plugins` ships
  `au-remote` but is not configured by jabali. Operator can wire
  to a syslog server or SIEM independently.

## Cross-references

- ADR-0072 (malware stack) — amended 2026-04-30 to record Tetragon
  removal.
- ADR-0085 — narrow auditd decision.
- M34 (per-user egress firewall) — companion network-layer audit
  via nft counters.
- M33 runbook — LMD inotify file-change tier (still active).
