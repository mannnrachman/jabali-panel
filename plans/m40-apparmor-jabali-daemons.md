# M40 — AppArmor profiles for Jabali daemons

**Status:** drafted 2026-04-30 · branch `m40/apparmor-jabali-daemons`

**ADR target:** new **0086** — AppArmor for jabali-owned daemons + system services.

**Companion to:** M39 (auditd narrow exec audit), M34 (per-user nft egress).

## Why

**AppArmor is mature, kernel-built-in (Debian 13 default), declarative, and path-based.** It enforces at the LSM hook layer — file open, exec, capability checks, network. We have a real defense-in-depth gap at the daemon layer: a panel-api or panel-agent RCE today reads `/etc/passwd`, `/home/*/wp-config.php`, `/etc/letsencrypt/live/*/privkey.pem`, dumps the MariaDB socket, exec's `nc` to phone home. AppArmor profiles confine each daemon to a minimal allowlist of paths + capabilities + network family.

**Scope: jabali-owned daemons + critical system services.** NOT user-facing PHP-FPM (same FP cliff that killed Snuffleupagus and Tetragon defaults — WordPress + Composer + custom apps execute too many things).

**Profile targets (priority order):**
1. `panel-api` (`/usr/local/bin/jabali-panel-api`) — runs as root, talks to MariaDB+Redis+agent socket, reads `/etc/jabali/`. Compromise = full panel takeover.
2. `panel-agent` (`/usr/local/bin/jabali-agent`) — runs as root, exec'd shell-out to nft/nginx/maldet/etc. Wide privilege.
3. `jabali-bulwark` (Node.js fronting service) — public-facing, untrusted input.
4. `mariadb` — operator might prefer `apt-get install mariadb-server-core` default profile.
5. `stalwart-mail` — mail surface, internet-facing on :25/:465/:587.
6. `redis-server` — confined data dir + sockets.
7. `pdns-server` + `pdns-recursor` — DNS surface, internet-facing on :53.
8. `kratos` (admin + public) — auth surface.
9. `nginx` — Debian default profile is conservative; consider override only if needed.

**Not targets:** `php-fpm` (FP risk), `bash` / `cron` (used everywhere), user binaries.

**Why AppArmor over SELinux:** path-based vs label-based. Operator-readable profiles. Debian-native (Ubuntu/Debian lean AA; RHEL leans SE). Profile-per-binary, not whole-system relabel.

## Verification — what "done" means

1. `aa-status` shows all 8+ jabali-targeted profiles loaded in `enforce` mode (or `complain` during the 7-day soak per Step 4).
2. `aa-status --json | jq '.profiles | length'` ≥ 8.
3. `cat /sys/kernel/security/apparmor/profiles | grep jabali` lists all jabali-prefixed profiles.
4. Synthetic violation test: `aa-exec -p jabali-panel-api /usr/local/bin/jabali-panel-api -- cat /etc/shadow` denied by AppArmor (audit log: `apparmor="DENIED"`).
5. Real workload smoke: panel-api boot, login, create user, install WordPress — zero `apparmor="DENIED"` lines for jabali-* profiles in `journalctl -k`.
6. Per-daemon profile sizes are reviewable (each ≤ 80 lines), no `audit deny` rules (use `deny` not `audit deny` to avoid log noise).
7. `jabali update` re-applies profiles; `aa-enforce /etc/apparmor.d/usr.local.bin.jabali-panel-api` is idempotent.

## Caveman wave map

| Wave | Steps | Parallel? | Ship-ready exit |
|---|---|---|---|
| A | 1 → 2 | sequential | install.sh `install_apparmor()` + tooling + jabali daemon profiles in COMPLAIN mode |
| B | 3 ‖ 4 | parallel after A | system-daemon profiles (mariadb/stalwart/redis/pdns/kratos) + UI status surface |
| C | 5 | sequential after B | 7-day complain-soak → flip to enforce → live-VM smoke |
| D | 6 | sequential after C | ADR-0086 + runbook + jabali update cleanup hook |

---

## Step 1 — install.sh `install_apparmor()` + base tooling

**Branch:** `m40/apparmor-jabali-daemons` (root branch)

**Brief:** AppArmor is in Debian 13 main, default-installed on most images. This step verifies install + adds operator tooling (`apparmor-utils` for `aa-status` / `aa-genprof` / `aa-logprof`) + ensures the LSM is enabled at boot.

**Tasks:**
1. Add `install_apparmor()` to install.sh (called from main install pipeline AFTER `install_malware_stack`):
   - `apt install -y apparmor apparmor-utils apparmor-profiles` (only if missing).
   - Verify `/sys/kernel/security/apparmor/` exists; if not, `_warn` and skip with sentinel `/etc/jabali/apparmor-disabled` (kernel without AA support — rare on Debian 13 but possible on minimal cloud images).
   - Confirm `aa-status` returns success.
2. **GRUB check:** AppArmor LSM must be active. Probe `grep -q apparmor /sys/kernel/security/lsm`. If not, append `apparmor=1 security=apparmor` to `GRUB_CMDLINE_LINUX_DEFAULT` in `/etc/default/grub` (idempotent — grep first), regenerate via `update-grub`, log a `_warn` that operator must reboot for AA to activate. Mark sentinel `/etc/jabali/apparmor-grub-pending`.
3. **Directory layout:** profiles ship under `install/apparmor/usr.local.bin.jabali-*` (mirroring AA's filename convention `path.with.dots`). install.sh copies them to `/etc/apparmor.d/` then `apparmor_parser -r -W` each.

**Verification:**
```bash
ssh root@192.168.100.150 'aa-status --enabled && echo OK'
ssh root@192.168.100.150 'aa-status --json | jq .profiles[]?.name | head'
ssh root@192.168.100.150 'grep apparmor /sys/kernel/security/lsm'
```

**Exit:** AppArmor active, tooling installed, no jabali profiles loaded yet (Step 2 ships those).

---

## Step 2 — jabali daemon profiles (panel-api, panel-agent, bulwark) in COMPLAIN

**Branch:** continues `m40/apparmor-jabali-daemons`.

**Brief:** Author the three jabali-owned daemon profiles from scratch. Start in COMPLAIN mode (logs but does not deny) so a 7-day soak surfaces missed paths before enforcement. Profile content is hand-authored from observed behavior, NOT `aa-genprof` output (genprof produces overly permissive profiles).

**Tasks:**
1. **`install/apparmor/usr.local.bin.jabali-panel-api`** — profile for panel-api binary. Allowed:
   - r `/etc/jabali/` (config + secrets).
   - rw `/var/lib/jabali-panel/` (state).
   - r `/etc/letsencrypt/live/*/fullchain.pem` (panel TLS).
   - rw `/run/jabali-panel.sock` (HTTP socket).
   - rw `/run/jabali-agent.sock` (agent IPC).
   - rw `/var/run/mysqld/mysqld.sock` (MariaDB unix socket).
   - rw `/run/redis/jabali.sock` (Redis socket).
   - rw `/var/log/jabali-panel/*` (logs).
   - cap `net_bind_service` (port 8443).
   - network inet stream (TCP) — outbound for OIDC/notifications/webhooks.
   - Standard libs (r `/lib/**` `/usr/lib/**` `/etc/ssl/**`).
   - Deny: `/etc/shadow`, `/home/**`, `/root/**`, `/etc/sudoers*`, `/var/log/audit/**`.
2. **`install/apparmor/usr.local.bin.jabali-agent`** — wider but still bounded:
   - All of panel-api's allowlist except outbound TCP (agent doesn't need it).
   - rw `/etc/nftables.d/jabali-*` (M34 reconciler writes).
   - rw `/etc/nginx/sites-available/jabali-*` + `/etc/nginx/sites-enabled/jabali-*`.
   - rw `/etc/audit/rules.d/jabali-*` (M39 reconciler writes).
   - rw `/etc/php/*/fpm/pool.d/jabali-*` (M9 pool manager).
   - rw `/etc/systemd/system/jabali-*` + `/etc/systemd/system/user-*.slice.d/`.
   - r `/proc/*/cgroup` (M18 slice probing).
   - cap `chown` `dac_override` `fowner` `fsetid` `kill` `net_admin` `setgid` `setuid` `sys_admin` (broad — agent is privileged orchestrator, rationale documented inline).
   - exec ix `/usr/sbin/nft` `/usr/sbin/nginx` `/bin/systemctl` `/usr/sbin/maldet` `/usr/local/bin/clamscan` `/usr/bin/yara` etc. (named exec list — NOT bare exec wildcard).
3. **`install/apparmor/usr.local.bin.jabali-bulwark`** — Node.js public-facing:
   - r `/var/lib/jabali-bulwark/` (state).
   - rw `/run/jabali-bulwark.sock`.
   - cap `net_bind_service`.
   - network inet stream.
   - Deny `/home/**`, `/etc/jabali/` (no need to read panel secrets).
4. **install.sh `apply_apparmor_profiles()`:**
   - Copy profiles from `install/apparmor/` to `/etc/apparmor.d/`.
   - `apparmor_parser -r` each.
   - **Default mode:** complain (`aa-complain /etc/apparmor.d/usr.local.bin.jabali-panel-api` etc.) on FRESH installs.
   - On UPGRADE (existing host), keep current mode — operator's enforce/complain choice persists.
   - First-install marker: `/etc/jabali/.apparmor-installed` (ts of first install).

**Verification:**
```bash
ssh root@192.168.100.150 'aa-status | grep jabali-panel-api'
# Expected: "in complain mode"
ssh root@192.168.100.150 'systemctl restart jabali-panel jabali-panel-agent jabali-bulwark'
ssh root@192.168.100.150 'journalctl -u jabali-panel --since "2 minutes ago" | grep -i apparmor || echo no_apparmor_lines'
# Expected: real workload runs; complain-mode logs show what would have been denied
ssh root@192.168.100.150 'ausearch -m AVC --start recent | grep "profile=\"jabali-" | head'
```

**Exit:** Three jabali daemon profiles loaded in complain mode. Real workload runs (panel responds, login works, user-create works). Complain-mode logs show denied-but-allowed paths the operator will review.

---

## Step 3 — System daemon profiles (mariadb / stalwart / redis / pdns / kratos)

**Branch:** continues `m40/apparmor-jabali-daemons` (parallel-safe with Step 4).

**Brief:** Confine the system services jabali depends on. Most ship vendor-supplied AA profiles in their .deb packages — we install them or override.

**Tasks:**
1. **MariaDB:** `apparmor-profiles-extra` ships `usr.sbin.mysqld` — install + complain. Override only if it conflicts with our M25 unix-socket-only setup.
2. **Stalwart:** no upstream AA profile. Author `install/apparmor/usr.local.bin.stalwart-mail`:
   - rw `/var/lib/stalwart-mail/`, `/etc/stalwart-mail/`.
   - r `/etc/letsencrypt/live/*/fullchain.pem`.
   - cap `net_bind_service` (25/465/587).
   - network inet stream.
3. **Redis:** Debian's `redis-server` package ships `/etc/apparmor.d/usr.bin.redis-server` — verify, set complain.
4. **PowerDNS authoritative + recursor:** upstream packages ship profiles (`usr.sbin.pdns_server`, `usr.sbin.pdns_recursor`) — verify, set complain. Override if our M6.3 split-port setup needs adjustments.
5. **Kratos admin + public:** no upstream profile. Author `install/apparmor/usr.local.bin.kratos`:
   - rw `/var/lib/kratos/`, r `/etc/jabali/kratos.yaml`.
   - rw `/run/kratos-admin.sock`, `/run/kratos-public.sock` (per M25 unix socket model).
   - rw `/var/run/mysqld/mysqld.sock`.
6. **install.sh `apply_apparmor_system_profiles()`:** orchestrates the load + complain-mode for all of the above. Idempotent, safe to re-run.

**Verification:**
```bash
ssh root@192.168.100.150 'aa-status'
# Expect 8+ profiles loaded, all in complain mode initially
```

**Exit:** All system-service profiles loaded; nothing in enforce yet; real workload runs.

---

## Step 4 — UI: AppArmor status surface in admin Security tab

**Branch:** continues `m40/apparmor-jabali-daemons` (parallel-safe with Step 3).

**Brief:** Read-only operator view: which profiles loaded, mode (complain/enforce), recent denials. NOT a profile editor — that's expert-only via SSH.

**Tasks:**
1. **panel-agent `internal/commands/security_apparmor.go`:**
   - `security.apparmor.status` → parses `aa-status --json` to return `{enabled, profiles: [{name, mode}], recent_denials: [{ts, profile, op, name}]}`.
   - `security.apparmor.set_mode` (admin only) → `aa-enforce` / `aa-complain` for a single named profile. Whitelisted set: only `jabali-*` and known system services we ship profiles for.
2. **panel-api:** `/api/v1/admin/security/apparmor/{status,profiles/:name/mode}`.
3. **panel-ui:** new `AppArmorCard` component on the admin Security tab (or as a sub-tab). AntD Table: Profile | Mode (badge) | Recent Denials. Mode toggle behind a confirm modal — flipping enforce on a profile that has unresolved complain-mode denials shows them in the modal first.

**Verification:**
```bash
cd panel-api && go build ./... && go test ./...
cd ../panel-ui && npm run build
# Live: panel UI shows all 8+ profiles, mode toggleable, denials visible
```

**Exit:** Admin can see profile inventory + recent denials + flip mode without SSH.

---

## Step 5 — 7-day complain-soak → flip to enforce + live-VM smoke

**Branch:** continues `m40/apparmor-jabali-daemons`.

**Brief:** Complain mode burns in for 7 days on the testbed. Operator reviews denials via the AppArmor card, adjusts profiles for FPs, then flips one profile at a time to enforce.

**Tasks:**
1. **Daily timer `jabali-apparmor-soak-report.timer`** (daily 04:00 UTC) → emails admin the count of `apparmor="ALLOWED"` (complain-mode denials that would have been denied) per profile per user. Operator reviews, adjusts profiles.
2. **Auto-flip CLI** (NOT a timer — operator must approve): `jabali apparmor flip-mature [--soak-days N] [--profile name] [--dry-run]`. Lists profiles with zero denials over the soak window; if `--profile` matches one, flip to enforce. Default soak 7 days.
3. **Live VM 192.168.100.150:**
   - Day 0: profiles loaded in complain mode (Steps 2+3).
   - Days 1-7: WordPress sites running, daily backup, mail flow, panel updates, scheduled scans — every code path the daemons exercise.
   - Day 7: review denials. For zero-denial profiles, run `jabali apparmor flip-mature --profile jabali-bulwark` (start with the smallest-surface daemon).
   - Day 8: flip jabali-panel-api.
   - Day 9: flip jabali-agent.
   - Day 10: flip system-service profiles (mariadb / stalwart / redis / pdns / kratos).
4. **Soak report archiving:** keep last 30 days at `/var/log/jabali/apparmor-soak/` for postmortem if a flip causes issues.

**Verification:**
```bash
ssh root@192.168.100.150 'aa-status' # all profiles in enforce
ssh root@192.168.100.150 'systemctl status jabali-panel jabali-panel-agent jabali-bulwark mariadb stalwart-mail redis-server pdns-server pdns-recursor kratos'
# All active (running)
# Synthetic deny test:
ssh root@192.168.100.150 'aa-exec -p jabali-panel-api -- cat /etc/shadow'
# Expected: Permission denied
```

**Exit:** All 8+ profiles in enforce mode; full workload runs without service disruption; synthetic violation denied.

---

## Step 6 — ADR-0086 + runbook + jabali update cleanup hook + memory

**Branch:** continues `m40/apparmor-jabali-daemons`.

**Tasks:**
1. **New `docs/adr/0086-apparmor-jabali-daemons.md`** (Status: Accepted). Sections:
   - Context — daemon defense-in-depth gap; AppArmor available in Debian 13; rejected php-fpm scope (FP cliff).
   - Decision — author profiles for jabali-owned daemons + critical system services; complain mode for 7-day soak then operator flips to enforce.
   - Alternatives — SELinux (label-based, ramp cost), bubblewrap per-daemon (over-engineering for long-lived daemons), seccomp filters (orthogonal — could pair with AA later), no MAC (status quo, accepted compromise risk).
   - Consequences (positive: defense in depth on a panel RCE; negative: profile maintenance every time a daemon learns a new path; rare AA tooling regressions on unusual cloud kernels).
2. **New `plans/m40-apparmor-jabali-daemons-runbook.md`:**
   - Files / units / commands.
   - Daily checks: `aa-status | grep -c jabali-`; `ausearch -m AVC --start today | grep "profile=\"jabali-" | wc -l`.
   - Adding a new path to a profile (when a daemon update needs a new file/cap): edit `/etc/apparmor.d/...`, `apparmor_parser -r`, observe complain mode for 24h, flip back to enforce.
   - Troubleshooting "service won't start after enforce flip": revert with `aa-complain`, fix profile, re-flip.
   - Rollback levels: per-profile complain (1 cmd), all-jabali complain (one CLI), AA disable entirely (`aa-teardown`, last resort).
3. **`docs/BLUEPRINT.md`** — add M40 row to changelog.
4. **`docs/adr/README.md`** — add ADR-0086 to index.
5. **install.sh `cleanup_apparmor_legacy()`:** safety net — if a previous M40 install left stale profiles, re-apply current canonical profiles. Idempotent.
6. **Memory updates:** new entry `project_m40_apparmor_shipped.md`.

**Exit:** Documentation complete, runbook published, idempotent install path proven on a re-run, memory updated.

---

## Notes

- **Profile authoring discipline:** every new path or cap added to a jabali profile must have a one-line comment explaining why. AppArmor profiles drift toward "world-readable" if reviewers don't push back.
- **Don't aa-genprof.** It produces overly permissive profiles by observing one workload run; real-world coverage misses paths and the operator ends up enforcing a profile that breaks a quarterly batch job. Author profiles by reading the daemon's source / strace, not by recording behavior.
- **Don't profile php-fpm in M40.** Operator FP intolerance is on record (M9 Snuffleupagus, M33 Tetragon defaults). PHP workload is too dynamic.
- **No agents per `feedback_never_agents`** — execute every step inline.
