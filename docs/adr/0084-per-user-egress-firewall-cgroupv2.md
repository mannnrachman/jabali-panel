# ADR-0084 — Per-user PHP-FPM egress firewall via nftables + cgroupv2 socket match (M34)

**Status:** PROPOSED
**Date:** 2026-04-29
**Supersedes/extends:** ADR-0054 (UFW over iptables), ADR-0068 (per-user cgroup v2 slice metrics)

## Context

After M33 amendment 3 (ClamAV removed; maldet 2.0 native HEX/MD5/SHA-256 +
yara-x as the on-disk scanner), one residual gap stands: **PHP runtime
exfil**. A webshell that lands inside a user's docroot — past every
filesystem-based scanner — can still phone home or pull a second-stage
payload at execution time via PHP network APIs (fsockopen, curl, or any
of the dozens of stream wrappers PHP exposes).

Two PHP-runtime hardening paths were evaluated to close this gap:

- **A. Snuffleupagus (PHP extension).** Inline runtime guard, popular
  with hosting providers, blocks dangerous functions and patterns at
  PHP-VM level. **Operator FP intolerance:** the panel operator runs
  diverse user code (WordPress + Composer + custom apps) and rejected
  this in M9 because the false-positive rate forces repeated
  per-tenant allowlisting. Bypassable via PHP-extension exec or
  LD_PRELOAD before the extension boots.
- **B. Suhosin.** Dead upstream; last release predates PHP 8.0.
  Rejected on supportability.
- **C. eBPF cgroup_skb program.** Stronger than Snuffleupagus
  (kernel-layer, no PHP-extension bypass), but adds a long-lived BPF
  program to the install surface and a tooling dependency
  (`bpftool` / `libbpf` / Go bindings). More moving parts than the
  alternative below.
- **D. nftables `socket cgroupv2 level 2 "user-<u>.slice"` match.**
  Native nftables matcher (kernel ≥ 5.7, Debian 13 ships ≥ 6.1 — already
  required). Per-user cgroup v2 slices already exist (M18 + ADR-0068).
  Zero new daemons, zero new compiled artifacts; one extra reconciler
  pass that renders `/etc/nftables.d/jabali-per-user-egress.nft`.

**D wins.** ROI is highest, blast radius is lowest, install surface
unchanged.

## Decision

1. **One nftables `inet` table `jabali_per_user`** owned by the panel.
   `nft -f` reload only — never `systemctl restart nftables` (would
   flush other tables we do not own, e.g. CrowdSec blocklists).
   Coexists with the existing UFW + CrowdSec tables (ADR-0054).

2. **vmap dispatch by cgroup path** for O(1) per-user routing. One
   `map cgroup_to_chain { type cgroupsv2 : verdict }` keyed on the
   level-2 slice path (`user-<u>.slice`). At 1000 users this is
   constant-time, vs O(N) linear matches in the naive form.

3. **Three policy states** per Linux user, stored as an ENUM in
   `user_egress_policies.state`:
   - `off` — no chain emitted; the slice falls through to the default
     accept policy. Used for break-glass.
   - `learning` — would-drops are logged (rate-limited 5/min) and
     counter-bumped, but accepted. The Step 8 timer auto-flips
     `learning` rows older than 7 days to `enforced`.
   - `enforced` (default) — would-drops are counter-bumped + dropped
     at the kernel packet layer.

4. **Default allowlist** (must include or stuff breaks):
   - **Loopback4:** 127.0.0.0/8 + 10.0.0.0/8 + 172.16.0.0/12 +
     192.168.0.0/16 — covers MariaDB / Redis / php-fpm sockets that
     drift between unix and 127.0.0.1 DSN configs.
   - **Loopback6:** ::1/128 + fc00::/7 + fe80::/10 — IPv6 parity for
     the same drift class.
   - **TCP 53/80/443/587/465/25** — DNS + WordPress auto-update +
     Composer + apt + SMTP submission via mail(). Removing :443 would
     silently break wp-cron updates → operator support load.
   - **UDP 53** — DNS resolver fallback.
   Operators who want to harden defaults (drop :25/:465/:587 on a
   no-mail-from-PHP host) edit them in Server Settings → Security
   (M34 Step 6); changes take effect within one reconciler tick (~60 s).

5. **Per-user extras** in `user_egress_policies.allowed_extra` (JSON
   array). Admin sets directly; user submits requests via
   `/me/egress/request` and admin approves via
   `/admin/egress-requests/:id/approve`.

6. **DB-as-truth** (ADR-0002). Two new tables:
   - `user_egress_policies` (one row per user; state, allowed_extra,
     denormalised drop_count_24h, learning_started_at).
   - `user_egress_requests` (queue, status pending/approved/denied).
   The reconciler is the only writer to `/etc/nftables.d/`.

7. **Per-user drop counter** (`counter user_<u>_drops`) exposed via
   `nft -j list counters` and read into
   `user_egress_policies.drop_count_24h` every reconciler tick. M14
   event source `egress_drop_burst` fires when a single user crosses
   `server_settings.egress_burst_threshold` (default 50) drops/minute.

8. **LEARNING auto-flip** for upgrading hosts. Existing hosts
   (`/etc/jabali/installed` already present at install time) start in
   LEARNING for 7 days, anchored on
   `user_egress_policies.learning_started_at`. A daily systemd timer
   (`jabali-per-user-egress-flip.timer`) runs the
   `jabali per-user-egress flip-mature` CLI. Operator pin via
   `/etc/jabali/per-user-egress.mode = learning` halts the auto-flip.
   New installs default to ENFORCED with the default allowlist.

## Consequences

**Wins**
- Webshell-phone-home becomes a kernel-level drop, not a userspace
  filter. PHP-extension bypass paths (LD_PRELOAD, in-process payload
  obfuscation) cannot evade a packet-layer drop.
- Zero install surface increase: nftables, cgroup v2 slices, and the
  reconciler pattern are all already on the host.
- Operator FP rate is approximately zero on a stock LAMP workload —
  the default allowlist covers MariaDB / Redis / DNS / HTTP/S / SMTP
  submission.
- Per-user signal (`egress_drop_burst`) gives operators a
  high-confidence "this user is infected" alert without per-user
  payload inspection.

**Costs**
- One additional reconciler pass and one nft reload per tick (~60 s
  cadence).
- Operators must learn the LEARNING / ENFORCED model; the runbook
  (Step 8) covers the common workflow.
- IPv6 outbound is allowed today only via the loopback6 default + any
  per-user extras; full IPv6 egress policy parity is a phase 2
  blueprint.

**Trade-offs explicitly accepted**
- Per-user, not per-pool. Some users run multiple PHP-FPM pools at
  different trust levels; pool-level scoping is deferred until concrete
  operator demand. Slice-level granularity matches the existing M18
  topology and is what the cgroup match natively keys on.
- No payload inspection. That is CrowdSec AppSec's job at the HTTP
  layer (M27). nftables drops the SYN; it does not inspect bytes.

## Reservations / phase 2

- **open_basedir + disable_functions PHP-FPM hardening** — separate
  blueprint, simpler scope, can run in parallel with M34.
- **Tetragon companion policy** for L3 audit — Step 9 (deferred). Belt
  and braces for the rare kernel-bug or mis-slice-attribution case.
- **IPv6 full coverage** — beyond loopback6.
- **Per-pool granularity** — slice-level scoping per ADR-0068 amendment.
- **Egress rate-limiting (vs binary drop)** — nftables `limit rate`
  could throttle suspicious egress; phase 3.

## Live evidence

(filled in at Step 8 acceptance — placeholder until VM smoke runs)
