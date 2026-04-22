# ADR-0047 — M6.3: pdns-recursor for local self-resolution

Status: **ACCEPTED**
Date: 2026-04-22
Milestone: M6.3 — pdns-recursor local self-resolution

## Context

The panel runs pdns-server (authoritative) on the public IP `:53`, serving
its own self-zone (bootstrapped by `install.sh:bootstrap_pdns_self_zone`)
plus every user-created domain. External resolvers queried via
NS delegation reach pdns-server just fine.

**Problem**: nothing on the panel host queries pdns-server for its own zones.
systemd-resolved (seeded by `install.sh:install_base_packages` ~L591) forwards
queries to whatever upstream the admin configured via the panel UI "DNS
Resolvers" page (`/etc/systemd/resolved.conf.d/jabali.conf`), typically public
`1.1.1.1 9.9.9.9`. A public resolver has no data about `mail.<our-domain>`
until NS delegation has propagated — which on a fresh domain is seconds to
hours depending on the parent registrar's TTL.

Symptoms accumulated across M6:

- **Bulwark → Stalwart `/.well-known/jmap`**: on an install host freshly
  enabled for email, Bulwark resolves `mail.<domain>` via the host's
  upstream DNS → public gets `NXDOMAIN` or stale data → JMAP session
  verify fails → user sees "webmail login failed (status 502)". M6.2
  shipped `NODE_TLS_REJECT_UNAUTHORIZED=0` as a cert-trust workaround,
  but the DNS side of the same problem remained.
- **ACME HTTP-01 pre-flight**: certbot plugins that probe the target
  name before requesting a challenge can hit the same delegation gap
  on first issuance.
- **Operator convenience**: `/etc/hosts` entries pointing panel domains
  at `127.0.0.1` have been the workaround. Doesn't scale, doesn't reconcile,
  drifts, and every fresh install reaches the same dead end.

The panel **is** the authoritative source for its own zones. Locally, the
host should prefer the authoritative data over public recursion.

## Decision

Install and run **pdns-recursor** on loopback `:53` (both v4+v6). Move
pdns-server's loopback binding to `:5300` so recursor owns the stub path.
Point systemd-resolved at `127.0.0.1` via a new drop-in
`/etc/systemd/resolved.conf.d/zz-jabali-recursor.conf` (alphabetically after
the panel-UI-managed `jabali.conf`).

Wire per-zone forwarders into the recursor via `/etc/powerdns/recursor.forwards`
(reconciler-owned); the recursor forwards authoritative zones to
`127.0.0.1:5300` and recurses everything else to public upstream via
`forward-zones-recurse=.=1.1.1.1;9.9.9.9`.

### 1. Split-port binding on pdns-server

Change `/etc/powerdns/pdns.d/01-jabali-mysql.conf`:

```
# before
local-address=127.0.0.1, ${JABALI_SRV_IPV4}, ::1
# after
local-address=${JABALI_SRV_IPV4}:53, 127.0.0.1:5300, [::1]:5300
```

Every entry carries an explicit port. pdns-server defaults `local-port` to
`53` when unspecified, but a future port flip would silently break only part
of the binds — listing explicitly closes that hole. Syntax pinned to
pdns-server 4.9+ (Debian 13 default).

Public-IP authoritative queries from the open internet are unchanged. The
recursor forwards into `127.0.0.1:5300` for the loopback authoritative path.

### 2. pdns-recursor on loopback :53

`/etc/powerdns/recursor.conf` (managed by install.sh):

```
local-address=127.0.0.1, ::1
local-port=53
allow-from=127.0.0.0/8, ::1/128
forward-zones-file=/etc/powerdns/recursor.forwards
forward-zones-recurse=.=1.1.1.1;9.9.9.9
dnssec=off
threads=2
max-cache-entries=50000
quiet=yes
loglevel=4
setuid=pdns
setgid=pdns
```

### 3. DNS amplification defense

`allow-from=127.0.0.0/8, ::1/128` — explicit, loopback-only. Debian's
package default is `127.0.0.0/8, 10.0.0.0/8, 172.16.0.0/12, 192.168.0.0/16`;
we narrow it deliberately because LXC bridge interfaces live in the RFC1918
ranges and the default would expose an open resolver to every co-tenant
container on the host.

`install.sh` hard-fails if `local-address` in the rendered `recursor.conf`
resolves to anything non-loopback, and hard-fails if `allow-from` contains
anything other than loopback CIDRs. Real external-source verification is
operator-driven from off-host (runbook) — in-host source-spoofing via
`dig -b <public-ip>` is a misleading probe because the kernel rewrites the
source address to loopback when the destination is loopback.

### 4. systemd-resolved drop-in at alphabetical-last

`/etc/systemd/resolved.conf.d/zz-jabali-recursor.conf`:

```
[Resolve]
DNS=
DNS=127.0.0.1
FallbackDNS=1.1.1.1 9.9.9.9
DNSSEC=no
```

Per `man systemd-resolved.conf`: "Setting this variable to an empty list (as
in `DNS=`) resets the list of servers to the empty list, all prior assignments
will be cleared." The `zz-` prefix sorts after the panel-UI-managed
`jabali.conf` so the reset applies globally; `DNS=127.0.0.1` is the resulting
authoritative setting.

**Gate**: install.sh asserts `resolvectl status` shows `DNS Servers:` with
`127.0.0.1` only — if the merge semantics aren't what we read from the man
page, install fails loudly on first run rather than silently routing queries
to the wrong resolver. Fallback path documented in `plans/m6.3-pdns-recursor.md`
Step 2 (consolidate `jabali.conf` into `zz-jabali-recursor.conf`; panel UI
edits `FallbackDNS=` instead of `DNS=`).

### 5. Startup ordering

`After=pdns.service` on `pdns-recursor.service` (recursor can't forward into
a dead authoritative); `After=pdns-recursor.service` on
`systemd-resolved.service` (stub can't query a dead recursor). Both via
`/etc/systemd/system/<unit>.service.d/10-jabali-after.conf` drop-ins.

### 6. Atomic `recursor.forwards` reload

Every edit flows through the agent (panel-api → `panel-agent` RPC):

1. Agent writes `recursor.forwards.tmp`.
2. Validator pass — strict regex per line
   (`^[a-z0-9][a-z0-9.-]*[a-z0-9]?=<addr>(:port)?$`) + semantic checks:
   reject trailing-dot zones, reject forwarder `127.0.0.1:53` / `[::1]:53`
   (self-loop), reject port 0 or >65535.
3. Backup: `os.Link(forwards, .bak)` or rename if `.bak` exists.
4. Atomic rename `.tmp` → `forwards`.
5. `rec_control reload-zones` (5s timeout).
6. Post-probe: `dig SOA <added-zone> @127.0.0.1` with 2s timeout.
7. On probe failure: restore `.bak`, reload again, return error.

This is the only path that writes `recursor.forwards`. The panel-api never
touches `rec_control`; the reconciler converges via the agent command.

### 7. Self-loop guard

Validator rejects `<zone>=127.0.0.1:53` and `<zone>=[::1]:53`. Port `5300`
is the only legal forwarder target. A zone accidentally forwarded to the
recursor's own bind port would loop until thread exhaustion.

### 8. No DNSSEC validation on the recursor

`DNSSEC=no`. systemd-resolved validates DNSSEC upstream already; doubling
up burns CPU per query with no security benefit for a single-host panel.

## Alternatives considered

- **dnsmasq**: good at DHCP/TFTP, weaker recursor. We already have pdns
  ecosystem tooling (`rec_control`) and don't want a second upstream to
  monitor; zero net benefit.
- **unbound**: solid recursor, but doesn't integrate with pdns's zone-aware
  forwarder file layout the way `recursor.forwards` does. Adds another
  daemon to operate, monitor, and keep patched. One less dependency wins.
- **systemd-resolved only, `DNSStubListener=no`**: impossible — resolved
  isn't a recursor that can forward per-zone, and disabling the stub
  breaks every app that reads `/etc/resolv.conf`.
- **Permanent `/etc/hosts` overrides**: doesn't scale past a handful of
  domains; every domain.create would need hosts patching; hosts file is
  not reconciled against the panel DB; drifts silently.
- **App-side resolver override** (Bulwark / Stalwart / Kratos talk direct
  to pdns-server on a loopback bypass): requires app code changes per-app,
  doesn't fix ACME HTTP-01 pre-flight or any future consumer. Not general.

## Consequences

- New package dependency: `pdns-recursor` (Debian 13 main; already in
  the install_base_packages apt batch after M6.3 step 2).
- `/etc/powerdns/recursor.forwards` becomes reconciler-owned state. On
  disk is the source of truth; no mirror in the panel DB. The backfill
  CLI (`jabali pdns backfill`) converges from panel DB → recursor.forwards
  idempotently.
- Loopback `:5300` is reserved — anything else that tries to bind it fails.
  Documented in the runbook.
- `rec_control` is the interface for live reload. Agent-only — panel-api
  never invokes it directly. Preserves the "panel-api runs no privileged
  local commands" invariant.
- `pdns-recursor` apt upgrades own `/etc/powerdns/recursor.conf`; a future
  Debian upgrade could drop our settings. Install.sh is idempotent — a
  re-run restores the managed config. Runbook flags this.
- Systemd-resolved restarts briefly drop DNS. install.sh sequences daemon
  starts so the stub never points at a dead recursor.

### Security posture

- `allow-from=127.0.0.0/8, ::1/128` keeps the recursor strictly local.
- install.sh hard-fails on non-loopback CIDRs in `allow-from` or
  non-loopback binds in `local-address`.
- Runbook includes an operator-driven amplification probe (`dig @<public-ip>
  . NS` from off-host → expect REFUSED).
- No change to pdns-server's public-IP surface (still authoritative-only
  on `:53`; no recursion).

### Failure modes

- Recursor dies → local resolution breaks (panel can't resolve its own
  zones). Systemd-resolved keeps the admin's `FallbackDNS` (`1.1.1.1 9.9.9.9`)
  so public names still resolve via the stub; the fallback is for
  non-authoritative only. Runbook includes the one-command fallback:
  edit `zz-jabali-recursor.conf` → set `DNS=1.1.1.1 9.9.9.9` → restart
  resolved.
- Self-zone absent from `recursor.forwards` → panel can't resolve its own
  hostname. `jabali pdns backfill --yes` repopulates; reconciler converges
  within 60s on next tick anyway.
- `/etc/hosts` overrides from pre-M6.3 hosts take precedence over the
  recursor (glibc NSS order). Runbook covers cleanup.

## References

- `plans/m6.3-pdns-recursor.md` — 7-step construction blueprint
  (architect-reviewed, CRITICAL+HIGH findings folded).
- `plans/m6.3-pdns-recursor-runbook.md` — operator runbook (shipped in
  Step 7).
- pdns-recursor config reference:
  https://doc.powerdns.com/recursor/settings.html
- systemd-resolved drop-in semantics: `man systemd-resolved.conf`.
- Post-incident notes: 2026-04-22 VM network break from an orphan
  `.network` drop-in under `/etc/systemd/network/`; the resolved drop-in
  for M6.3 lives under `resolved.conf.d/` and carries no `[Match]` block
  — bounded risk, explicitly flagged in the plan's known-pitfalls.
