# ADR-0086 — AppArmor profiles for Jabali daemons (M40)

**Status:** Accepted, all profiles parked pending re-author (Amendment 2026-05-09)
**Date:** 2026-04-30
**Related:** ADR-0072 (malware stack), ADR-0084 (per-user egress
firewall), ADR-0085 (narrow auditd exec audit).

## Amendment 2026-05-09 — all profiles parked, awaiting M40.1

Live audit on mx via `aa-exec -p <profile> -- /test_connect <socket>`
exposed two structural problems:

1. **AA 4.x unix-socket mediation rejects the rules as written.** Every
   jabali daemon profile fails the same EACCES on a Unix-socket
   `connect()` to `/var/run/mysqld/mysqld.sock`, even with
   `abstractions/mysql` included AND explicit `unix (connect, receive,
   send) type=stream,` AND both `/run/mysqld/mysqld.sock rw` +
   `/var/run/mysqld/mysqld.sock rw` path rules. Disabling the profile
   lifts the block. AA 4.x on Debian 13 wants rules we haven't yet
   figured out — needs a per-rule test bench.

2. **Three of four non-agent profiles never auto-attached.** The
   declarations were `profile <name> flags=(complain) {` with no path
   attach, so `/proc/<pid>/attr/current` showed `unconfined` for live
   panel-api/kratos/stalwart processes. Only the `jabali-agent` profile
   (which had a path attach) actually mediated anything — and that's
   the one that broke `dns.zone.upsert` by EACCES'ing the gmysql
   socket dial.

**Current state:**

- All 5 jabali AA profile files renamed to `*.disabled` in
  `install/apparmor/`. The auto-loader skips them.
- `cleanup_apparmor_legacy()` aa-disables + removes the live
  `/etc/apparmor.d/usr.local.bin.jabali-*` files on every install +
  `jabali update` tick, then restarts each daemon so any leftover
  EACCES self-heals.
- `jabali apparmor flip-mature` CLI + admin Security AppArmor card
  remain shipped + useful for the system-daemon profiles
  (mariadb/redis/pdns/pdns-recursor) that come from
  `apparmor-profiles-extra` and DO work.
- ADR-0086's design decisions still stand for **M40.1**: re-author
  rules with AA-4.x-correct unix mediation and verify each profile
  via `aa-exec -p` smoke runs against the daemon's full code-path
  inventory (login, create user, install WordPress, mail
  send/receive, reconciler tick) before any profile gets re-enabled.

The "defense-in-depth on a panel RCE" goal is unchanged. The
implementation gets a redo.

## Context

After M39 removed Tetragon and added narrow auditd, jabali had two
defensive tiers:

1. Network plane — M34 nft + cgroup v2 drops outbound SYNs from
   user PHP-FPM slices to non-allowlisted destinations.
2. Forensic exec audit — M39 auditd records suspicious-binary execve
   per user.

The remaining gap: **a panel-api or panel-agent RCE has the entire
filesystem available**. `/etc/passwd`, `/home/*/wp-config.php`,
`/etc/letsencrypt/live/*/privkey.pem`, MariaDB unix socket, every
operator-stored secret. M14 alerts fire after the fact; the RCE is
already done.

We need a Mandatory Access Control layer that confines our own
daemons to the file/socket/cap surface they actually need.

## Decision

**AppArmor**, path-based MAC, in mainline kernel + Debian 13 default,
profiles authored per binary. **Scope: jabali-owned daemons + critical
system services jabali depends on. NOT user-facing PHP-FPM** (operator
FP intolerance documented in M9 Snuffleupagus rejection + M33
Tetragon-default rejection).

Profiles ship under `install/apparmor/usr.local.bin.jabali-*` (Debian
filename convention: dots replace slashes). install.sh
`install_apparmor()` copies them to `/etc/apparmor.d/` then
`apparmor_parser -r`.

**Default mode on first install: complain** (audit-only, no enforcement).
Operator burns in for 7 days, reviews any AVC denials in `journalctl
-k`, then flips per-profile to enforce via:

```
jabali apparmor flip-mature [--profile <name>] [--dry-run]
```

or per-profile via the panel UI Security → AppArmor tab.

**On upgrade** (existing host with `/etc/jabali/.apparmor-installed`
present), the apply pass preserves the operator's current
complain/enforce state — re-renders the profile content but does NOT
flip mode.

### Profiles shipped in M40

| Profile | Binary | Notes |
|---|---|---|
| `jabali-panel`   | `/usr/local/bin/jabali-panel`        | HTTP API, talks to MariaDB+Redis+agent socket. Tight: r `/etc/jabali/`, rw `/var/lib/jabali-panel/`, sockets, `cap net_bind_service`. Hard-deny `/etc/shadow`, `/home/**`, `/root/**`. |
| `jabali-agent`   | `/usr/local/bin/jabali-agent`        | Privileged orchestrator. Wider cap set + named-exec list (~50 entries — nft, nginx, systemctl, maldet, restic, etc.). All exec entries `ix` (inherit profile) so children stay confined. |
| `jabali-bulwark` | `/usr/local/bin/jabali-bulwark`      | Public-facing Node.js daemon. Tight: state dir + sockets + outbound TCP. **Hard-deny `/etc/jabali/`** — bulwark must NOT read panel secrets. |
| `stalwart-mail`  | `/usr/local/bin/stalwart`            | Mail daemon listening on 25/465/587. r `/etc/stalwart/`, rw `/var/lib/stalwart-mail/`, sockets, `cap net_bind_service`. |
| `jabali-kratos`  | `/usr/local/bin/kratos`              | Identity service. r `/etc/jabali/kratos*`, rw `/var/lib/kratos/`, two unix sockets. |

### NOT shipped (deferred or out of scope)

- **mariadb / redis / pdns** — vendor packages ship AppArmor profiles
  in `apparmor-profiles-extra`; install.sh leaves them alone (no
  override). Operator can `aa-enforce /etc/apparmor.d/usr.sbin.mysqld`
  manually if desired.
- **php-fpm** — operator FP intolerance. PHP workload spans WordPress
  ImageMagick + Composer + custom apps; an enforce-mode profile would
  break the same legit paths Snuffleupagus did.

## Alternatives considered

### SELinux

Label-based (vs path-based). Steeper operator ramp; Debian/Ubuntu lean
AppArmor by default; whole-system relabel is disruptive. Not worth
the cost when AppArmor covers the same threat surface for our use.

### Bubblewrap per-daemon

Already used for SSH shell sandboxing (M13). Wrong shape for long-
lived daemons — bubblewrap is per-process namespace creation, not
LSM enforcement. Daemon restart cycles + systemd unit dependencies
get complicated.

### seccomp filters

Orthogonal — could pair with AppArmor later for syscall narrowing.
Out of scope for M40; AppArmor's file/cap/network coverage is the
high-signal layer.

### No MAC (status quo)

Accepted compromise risk: a panel-api RCE = full host takeover.
Documented as the gap that motivated M40.

## Consequences

### Positive

- **Defence in depth on a panel RCE.** Reading `/etc/shadow`,
  exec-ing `/usr/bin/nc` for reverse shell, scraping `/home/*`
  wp-config.php files — all denied for jabali daemons.
- **No new daemon, no BTF, no third-party repo.** AppArmor is in
  Debian 13 main; tooling is `aa-status` / `aa-enforce` /
  `aa-complain` from `apparmor-utils`.
- **Profile-per-binary.** Each profile reviewable in isolation
  (~30-130 lines). Diff-friendly for code review.
- **Complain-mode soak.** First-install default + per-profile flip
  CLI removes the FP cliff that killed Snuffleupagus + Tetragon
  defaults. Operator never gets a "panel broke after upgrade"
  surprise.

### Negative

- **Profile maintenance.** Every time a daemon learns a new
  path/cap/socket, the profile needs an edit + reload. Mitigation:
  complain mode is the cushion — production can stay there
  indefinitely if maintenance is light.
- **Path-based defeats by attacker who can rename binaries.** A
  jabali-agent RCE that hardlinks `/usr/bin/nc` to a path the agent
  profile permits (e.g. `/usr/local/bin/jabali-foo`) escapes. M39
  auditd narrow exec catches the renamed binary's execve via auid;
  layered defence.
- **AppArmor LSM-only kernels are missing on rare cloud images.**
  install.sh detects via `/sys/kernel/security/apparmor` + LSM list,
  falls back to a sentinel + warning; operator reboots to activate.

### Risks accepted

- **Fail-closed enforce flip can break a daemon.** Mitigation: 7-day
  complain soak + UI confirm modal that names the risk explicitly +
  per-profile flip (not all-at-once) + the complain-mode AVC log
  provides the missing-path delta to fix.
- **First-install on a host with no AppArmor LSM activated.** install
  edits GRUB to add `apparmor=1 security=apparmor`, sets a sentinel
  `/etc/jabali/.apparmor-grub-pending`, warns the operator. M40 is
  installed but not active until reboot.

## Implementation

- `install.sh install_apparmor()` — apt installs `apparmor` +
  `apparmor-utils` if missing, probes LSM, edits GRUB if needed,
  calls `apply_apparmor_profiles()`. Idempotent.
- `apply_apparmor_profiles()` — copies + parses + sets mode per
  profile. First install = complain; upgrade = preserve current.
- `panel-agent internal/commands/security_apparmor.go` —
  `security.apparmor.{status,set_mode}`. Set-mode allowlist is
  hard-coded to the M40 profile names; arbitrary input rejected.
- `panel-api /api/v1/admin/security/apparmor/{status,profiles/:name/mode}`.
- `panel-ui` Security → AppArmor sub-tab — read-only profile list
  + per-profile flip behind a confirm modal (which lists the risk
  before enforce).
- `jabali apparmor flip-mature [--profile X] [--dry-run]` — operator
  CLI. Lists complain-mode jabali profiles, flips matching ones to
  enforce.
- `jabali apparmor status` — quick CLI list of profile + mode.

### Live verification (target after merge)

On 192.168.100.150:
1. `aa-status | grep jabali-` lists 4+ profiles in complain mode on
   first install, preserved mode on upgrade.
2. Synthetic deny: `aa-exec -p jabali-panel -- cat /etc/shadow`
   produces `apparmor="DENIED"` in `journalctl -k`.
3. WordPress install + login + create user + run scheduled scan
   over 24h produces ZERO `apparmor="DENIED"` lines for jabali-*
   profiles (FP-free baseline before flip-mature).
4. `jabali apparmor flip-mature --profile jabali-bulwark` flips
   bulwark to enforce; subsequent panel actions unaffected.
