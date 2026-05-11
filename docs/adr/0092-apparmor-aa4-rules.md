# ADR-0092 — AppArmor 4.x profile authoring patterns (M40.1 empirical findings)

**Status:** Accepted — 2026-05-09
**Amends:** ADR-0086 (M40 AppArmor profiles — reverse-link added)
**Related:** ADR-0085 (narrow auditd exec audit), ADR-0084 (per-user egress firewall)

## Context

M40 shipped five AppArmor profiles for jabali daemons. Post-ship smoke on
Debian 13 (kernel 6.12, AppArmor 4.0.1) revealed two structural failures:

1. **Unix-socket mediation regressed.** Every jabali daemon hit `EACCES` on
   any `connect()` to a Unix socket — MySQL (`/var/run/mysqld/mysqld.sock`),
   Kratos admin, agent UDS — even with `network unix stream,` rules, explicit
   path grants, and `abstractions/mysql` included. Disabling the profile
   immediately lifted the block.

2. **Three of five profiles never auto-attached.** Profile declarations of
   the form `profile <name> flags=(complain) { … }` (no path attach) produce
   a named profile that is loaded into the kernel but never attached to a
   process: `/proc/<pid>/attr/current` shows `unconfined`. Only
   `jabali-agent`, which had an explicit path (`/usr/local/bin/jabali-agent
   flags=(complain) { … }`), mediated anything — and that is the profile
   that broke `dns.zone.upsert` via EACCES on the gmysql socket.

M40.1 rebuilt all five profiles from scratch against AA 4.x rules and
verified each with `aa-exec -p <profile> -- <binary>` smoke runs. The
findings below are empirical observations from that test bench
(`tools/aa-smoke/`), not the AA 4.x documentation (which is sparse on
per-rule inheritance between minor versions).

## Decision

The following rules and patterns are required for jabali AppArmor profiles
targeting Debian 13 / AA 4.x. Profiles that omit any of these silently
fail on real workloads.

### 1. Binary-path attach is required in the profile header

```
/usr/local/bin/<daemon-name> flags=(complain) {
  …
}
```

A named profile (`profile <name> flags=(complain) { … }`) without a path
prefix is loaded into the kernel but never auto-attached to a new process
execve. The `aa-exec -p <name>` test bench verifies attach; `aa-status`
listing a profile is **not** sufficient evidence that it mediates anything.

### 2. Unix-socket mediation requires explicit type= rules

AA 4.x on Debian 13 returns `EACCES` for every `connect()` on a stream or
datagram Unix socket unless the profile explicitly lists:

```
unix (create, bind, listen, accept, connect, send, receive) type=stream,
unix (create, bind, connect, send, receive) type=dgram,
```

The following are **not** sufficient substitutes:

- `network unix,` or `network unix stream,` — these grant the `AF_UNIX`
  socket(2) call but not connect/send/receive operations on the socket fd.
- Path rules (`/var/run/mysqld/mysqld.sock rw,`) — these control file-system
  access to the socket inode, not the socket operations themselves. Required
  in addition to the `unix` rules, not instead of.
- `#include <abstractions/mysql>` — includes path rules for the MySQL socket
  but not the unix operation rules. Fails on AA 4.x for the same reason.
- `unix (connect, receive, send) type=stream,` — incomplete; missing
  `create` causes socket(2) to EACCES before connect even runs. Missing
  `bind`/`listen`/`accept` causes daemons that create server sockets (e.g.
  Kratos, the panel's Unix listener) to fail.

### 3. `unix peer=(label=unconfined)` for kernel→user callbacks

Daemons that receive connections from unconfined processes (nginx, PHP-FPM
workers, external tooling) require:

```
unix peer=(label=unconfined),
```

Without this rule, the kernel's peer-credential check denies the inbound
`accept()` even if the daemon's own socket creation is permitted.

### 4. `peer=(addr=<path>)` is known-bad syntax for named sockets

`addr=` in a `unix peer=` rule is for Linux abstract namespace sockets
(those whose name starts with `\0`). For filesystem-path sockets, the
address is a path rule, not a peer= attribute. Using `peer=(addr=/path/to.sock)`
either silently matches nothing or produces a parser warning. Do not use it.

### 5. Profile file naming (Debian convention)

Profile files must be named after the binary path with `/` → `.`:

```
/etc/apparmor.d/usr.local.bin.jabali-panel
```

A profile named `jabali-panel` stored in a file named `jabali-panel` will
not be auto-loaded by `apparmor_parser -r /etc/apparmor.d/` on Debian 13
because the file does not follow the Debian auto-load naming convention.

## Verified working unix rule block (all five M40.1 profiles)

```
unix (create, bind, listen, accept, connect, send, receive) type=stream,
unix (create, bind, connect, send, receive) type=dgram,
unix peer=(label=unconfined),
```

This block, combined with path rules for each socket file (`rw,`), produced
zero `apparmor="DENIED"` lines across a 48-hour smoke on mx.jabali-panel.com
(WordPress install + login + cron + mail send/receive + reconciler ticks).

## Consequences

### Positive

- All five jabali daemon profiles (jabali-panel, jabali-agent, jabali-bulwark,
  jabali-kratos, stalwart-mail) attach correctly and produce zero FP denials
  in complain mode on production workloads.
- The `tools/aa-smoke/` test bench (`make aa-smoke`) catches regressions on
  kernel or AA version bumps without requiring a full smoke on a live VM.
- The failure modes are documented, so future profile authors don't rediscover
  them by watching production break.

### Negative

- The `unix` rules are broad (all operations, both stream and dgram). A
  future hardening pass could narrow to per-daemon per-peer rules once the
  socket inventory stabilises. The broad block is the safe default for
  complain-mode soak.

### Risks

- **AA version drift.** These rules are empirically verified on AA 4.0.1
  (Debian 13, kernel 6.12). A future AA minor that tightens `peer=` semantics
  or changes how `type=` is evaluated would require a re-smoke. Mitigation:
  `make aa-smoke` is a CI target; run it on any kernel or AA package bump.

## Implementation

Shipped in:
- `a618bb85 fix(apparmor)`: all 5 profiles rewritten with AA 4.x unix rules
- `f78f378a fix(apparmor)`: added `unix peer=(label=unconfined)` to all 5
  profiles after kernel→user accept() failures observed in smoke

Profile sources: `install/apparmor/usr.local.bin.jabali-*`
Test bench: `tools/aa-smoke/`
