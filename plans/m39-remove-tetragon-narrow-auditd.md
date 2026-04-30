# M39 — Remove Tetragon, replace with narrow-scoped auditd

**Status:** drafted 2026-04-30 · branch `m39/remove-tetragon-narrow-auditd`

**ADR targets:**
- Amend [0072](../docs/adr/0072-malware-detection-stack.md) — record Tetragon removal.
- New **0085** — Narrow-scoped auditd as L3 forensic substitute.

**Supersedes:** M33 Step 9 (Tetragon eBPF runtime detection); M34 Step 9 (Tetragon companion policy, deferred — closed as deleted).

## Why

Tetragon was added in M33 for "L3 forensic audit — belt and braces for the rare kernel-bug or mis-slice-attribution case." Six months on, the verdict:

- **Built for k8s/cloud-native, not bare-metal LAMP.** Cilium's audience is per-pod workload identity; we have per-user-slice on a single tenant-per-host model. Different axis.
- **Default 4 TracingPolicies (`exec-from-tmp` / `chmod-x-docroot` / `curl-bash` / `suspicious-syscalls`) are noise cannons** on shared hosting. WordPress ImageMagick, Composer tempfiles, wp-cli auto-update, plugin uploads, Let's Encrypt webroot probes — all hit at least one default policy daily. Signal-to-noise approaches zero.
- **BTF gate fragmentation.** install_tetragon ships with skip+sentinel for no-BTF kernels; M33 effectively ships in two operating modes neither symmetrically tested.
- **Tetragon-relay log shim deferred indefinitely.** Events sit in `/var/log/tetragon/tetragon.log`; nobody ingests them. Installed-but-unused = worst combo (cost without benefit).
- **CRD/YAML policy model is wrong shape for hosting.** N users × M policies = YAML explosion or matchBinaries explosion. nft (M34) chose data-driven; Tetragon insists on policy-as-code.

**Replacement:** auditd, narrow-scoped to suspicious binary execve only (NOT blanket execve). High signal, low noise, no BTF, decades of mature tooling. Per-user via `auid` (loginuid) — answers "what did user X exec?" cleanly.

## Verification — what "done" means

1. `systemctl status tetragon` returns `Unit tetragon.service could not be found.` on a fresh install AND on a host that previously had M33 Tetragon installed (after `jabali update`).
2. `ls /etc/tetragon/ /opt/tetragon/ /var/log/tetragon/` returns ENOENT.
3. `auditctl -l | grep jabali_susp_exec` lists the 11 narrow rules.
4. Running a shell as a hosting user produces an `audit.log` line with `key="jabali_susp_exec"` AND `auid=<user-uid>` AND `comm="bash"`.
5. `ausearch -k jabali_susp_exec --start recent | aureport -x --summary` prints a top-N binary table without panel-api help.
6. WordPress `wp-cron` running on a real domain produces ZERO `jabali_susp_exec` rows over a 5-minute observation window (no FP on legit PHP cron).
7. Webshell forensic test (PHP webshell that calls into a curl subprocess from user docroot, hit via nginx) produces an audit row tagged `jabali_susp_exec` AND `auid=<user>`.
8. Migration that drops `tetragon_policy_state` table runs clean on a host that had M33 Tetragon installed.

## Caveman wave map

| Wave | Steps | Parallel? | Ship-ready exit |
|---|---|---|---|
| A | 1 → 2 | sequential | install.sh + agent + panel-api scrubbed of Tetragon; drop migration written |
| B | 3 ‖ 4 | parallel after A | UI scrub + auditd installer + narrow rule file |
| C | 5 | sequential after B | agent.security.audit.* commands + read-only "Exec audit" UI card |
| D | 6 | sequential after C | jabali update cleanup hook + ADR amendments + runbook + live-VM smoke |
| E | 7 | deferred | M14 `exec.audit.burst` event source (only if operator demand surfaces) |

---

## Step 1 — Scrub Tetragon from install.sh + panel-agent + panel-api

**Branch:** `m39/remove-tetragon-narrow-auditd` (root branch; subsequent steps stack)

**Brief:** Tetragon footprint is wide. This step removes the install path and the agent/API code that drove it; NO behavior is added. Migration to drop `tetragon_policy_state` is in this step (forward-only).

**Tasks:**

1. **install.sh:**
   - Delete `install_tetragon()` function.
   - Remove `install_tetragon` call from `install_malware_stack()`.
   - Remove `bpftool` install block (was Tetragon BTF probe). Add comment that bpftool is no longer required.
   - Remove `TETRAGON_VERSION` env var declaration.
   - Remove tetragon paths from any cleanup/uninstall pass.

2. **panel-agent:**
   - `internal/commands/security_malware.go`: delete `mwTetragonSetEnabledHandler`, `mwListTetragonPoliciesHandler`, `mwToggleTetragonPolicyHandler`. Remove `tetragonPolicyDir` const + `tetragonPolicyNameRE`. Drop `TetragonAvailable` + `TetragonReason` from `mwStatusResponse` and the population logic.
   - `cmd/jabali-tetragon-relay/`: delete the entire directory (binary entrypoint shipped but never wired).
   - Drop `ScannerTetragon = "tetragon"` const from response shapes; `ev.Source` enum reduces to `maldet | clamav | yara`.
   - `internal/commands/backup_system.go`: remove Tetragon paths from skip-paths list.

3. **panel-api:**
   - `internal/api/security_malware.go`: drop `listTetragonPolicies` + `toggleTetragonPolicy` handlers + their route registrations.
   - `internal/api/security_malware.go` `putSettings`: drop the `TetragonEnabled` diff-and-call branch.
   - `internal/models/malware.go`: delete `TetragonPolicyState` struct + `TableName()`. Delete `ScannerTetragon` const. Remove `TetragonEnabled` field from `MalwareSettings`.
   - `internal/repository/tetragon_policy_state_repository.go`: delete file.
   - `internal/repository/malware_settings_repository.go`: drop any `TetragonEnabled` read/write.
   - `internal/eventsources/malware.go`: drop the `ScannerTetragon → severity=critical` branch. Default severity logic stays for maldet/yara.
   - `internal/app/app.go`: remove `TetragonPolicies repository.TetragonPolicyStateRepository` from `Deps`.
   - `cmd/server/serve.go`: remove `deps.TetragonPolicies` initialisation.

4. **Migration `000NNN`** (next free at merge time — coordinate via memory `feedback_merge_audit_migrations` since main moves fast):
   - `up.sql`: `DROP TABLE IF EXISTS tetragon_policy_state;` AND `ALTER TABLE malware_settings DROP COLUMN IF EXISTS tetragon_enabled;`. Defensive `IF EXISTS` against re-applies.
   - `down.sql`: re-create both with M33 column types (forward-only convention says down is best-effort).

5. **panel-ui:**
   - `src/shells/admin/security/AdminSecurityMalware.tsx`: drop `useTetragonPolicies` + `useToggleTetragonPolicy` imports; remove the Tetragon Tile from Overview; remove `tetragon` color entry; remove `tetragon` source filter option; remove `Tetragon eBPF runtime detection` Settings switch; delete `TetragonCard` component entirely. Reduce sub-tab list from 7 to 6 (Overview / Quarantine / Events / ManualScan / YARA / Settings).
   - `src/hooks/useSecurityMalware.ts`: delete `useTetragonPolicies`, `useToggleTetragonPolicy`. Drop `tetragon_available` / `tetragon_reason` / `TetragonEnabled` from response/payload types.

**Verification:**
```bash
git checkout -b m39/remove-tetragon-narrow-auditd
# After edits:
grep -ri "tetragon\|Tetragon" install.sh panel-agent/ panel-api/ panel-ui/src/ 2>/dev/null
# Expected: zero matches OR only matches in plans/, docs/adr/0072 (amendment text), docs/BLUEPRINT.md (changelog)
cd panel-api && go build ./... && go test ./...
cd ../panel-agent && go build ./... && go test ./...
cd ../panel-ui && npm run build
```

**Exit:** All packages build, all tests green, repo grep for "tetragon" returns only doc/plan files.

**Rollback:** `git revert` the commit. Drop migration is forward-only; restore via the `down.sql` + redeploy.

---

## Step 2 — Drop bpftool + Tetragon paths + jabali update cleanup hook

**Branch:** continues `m39/remove-tetragon-narrow-auditd`.

**Brief:** Step 1 deleted the function but install.sh has scattered checks for BTF / tetragon-disabled sentinel / bpftool. Sweep them. Add cleanup hook so existing M33 hosts converge cleanly on `jabali update`.

**Tasks:**
1. Remove BTF probe blocks (`/sys/kernel/btf/vmlinux` checks) from install.sh — only Tetragon used them.
2. Remove `/etc/jabali/tetragon-disabled` sentinel writes (no longer meaningful).
3. **`jabali update` cleanup hook:** when install.sh runs on a host that previously had Tetragon, mask + stop + disable + delete units; remove `/opt/tetragon/`, `/etc/tetragon/`, `/var/log/tetragon/`, `/usr/local/bin/tetragon*`, `/etc/jabali/tetragon-disabled`. Function `cleanup_tetragon_legacy()` called from `install_malware_stack()` BEFORE the rest of the malware stack provisioning. Idempotent (safe to run twice; safe on a fresh host where nothing exists).

**Verification:**
```bash
ssh root@192.168.100.150 'systemctl is-active tetragon || true; ls /opt/tetragon /etc/tetragon /var/log/tetragon 2>&1'
# Run jabali update, then re-run the same probe
ssh root@192.168.100.150 'jabali update'
ssh root@192.168.100.150 'systemctl status tetragon || true'
# Expected: Unit tetragon.service could not be found.
```

**Exit:** Live host that previously had M33 Tetragon now has zero Tetragon footprint after `jabali update`.

---

## Step 3 — install.sh `install_audit_exec()` + jabali-exec.rules

**Branch:** continues `m39/remove-tetragon-narrow-auditd` (parallel-safe with Step 4).

**Brief:** auditd is in Debian main. Single rule file scoped to suspicious-binary execve — NOT blanket `-S execve`. Per-user via `auid>=1000` filter.

**Tasks:**
1. Add `install_audit_exec()` function to install.sh, called from `install_malware_stack()` AFTER the maldet/yara block:
   - `apt install -y auditd audispd-plugins` (auditd not assumed already present — verify, only install if missing).
   - Render `/etc/audit/rules.d/jabali-exec.rules` (root:root 0640) with the 11 narrow rules:

     ```
     # Jabali — narrow-scoped suspicious-binary execve audit.
     # Tagged 'jabali_susp_exec' for ausearch -k pivots.
     # auid>=1000 = real users only (excludes daemon services).
     # auid!=4294967295 = exclude pre-PAM kernel threads.

     -a always,exit -F arch=b64 -S execve -F path=/bin/bash         -F auid>=1000 -F auid!=4294967295 -k jabali_susp_exec
     -a always,exit -F arch=b64 -S execve -F path=/bin/sh           -F auid>=1000 -F auid!=4294967295 -k jabali_susp_exec
     -a always,exit -F arch=b64 -S execve -F path=/bin/dash         -F auid>=1000 -F auid!=4294967295 -k jabali_susp_exec
     -a always,exit -F arch=b64 -S execve -F path=/usr/bin/wget     -F auid>=1000 -F auid!=4294967295 -k jabali_susp_exec
     -a always,exit -F arch=b64 -S execve -F path=/usr/bin/curl     -F auid>=1000 -F auid!=4294967295 -k jabali_susp_exec
     -a always,exit -F arch=b64 -S execve -F path=/usr/bin/nc       -F auid>=1000 -F auid!=4294967295 -k jabali_susp_exec
     -a always,exit -F arch=b64 -S execve -F path=/usr/bin/ncat     -F auid>=1000 -F auid!=4294967295 -k jabali_susp_exec
     -a always,exit -F arch=b64 -S execve -F path=/usr/bin/socat    -F auid>=1000 -F auid!=4294967295 -k jabali_susp_exec
     -a always,exit -F arch=b64 -S execve -F path=/usr/bin/python3  -F auid>=1000 -F auid!=4294967295 -k jabali_susp_exec
     -a always,exit -F arch=b64 -S execve -F path=/usr/bin/perl     -F auid>=1000 -F auid!=4294967295 -k jabali_susp_exec
     -a always,exit -F arch=b64 -S execve -F path=/usr/bin/php      -F auid>=1000 -F auid!=4294967295 -k jabali_susp_exec_phpcli
     ```
   - `augenrules --load` (idempotent reload).
   - `systemctl enable --now auditd`.
   - On `jabali update`, idempotent re-render only if checksum changed.
2. **Log rotation:** keep auditd's default `audisp-rotate` (not journald) — `/var/log/audit/audit.log.*`. Document size cap in runbook.
3. **No syscall-32 rules** — Debian 13 64-bit only per project convention.

**Verification:**
```bash
ssh root@192.168.100.150 'apt list --installed 2>/dev/null | grep auditd'
ssh root@192.168.100.150 'auditctl -l | grep jabali_susp_exec | wc -l'
# Expected: 11
ssh root@192.168.100.150 'sudo -u testuser -- bash -c "echo hi"'
ssh root@192.168.100.150 'ausearch -k jabali_susp_exec --start recent | tail -20'
# Expected: lines with comm="bash" auid=<testuser-uid>
# Negative test:
ssh root@192.168.100.150 'apt-get update'
ssh root@192.168.100.150 'ausearch -k jabali_susp_exec --start recent | grep apt-get | wc -l'
# Expected: 0 (auid=0 filtered out)
```

**Exit:** auditd running, 11 rules loaded, real-user execve produces tagged log entries, system-user execve produces zero.

**Rollback:** `rm /etc/audit/rules.d/jabali-exec.rules && augenrules --load`.

---

## Step 4 — UI scrub verification

**Branch:** continues `m39/remove-tetragon-narrow-auditd` (parallel-safe with Step 3).

**Brief:** Step 1 deleted Tetragon UI surfaces inline. This step is the verification + visual touch-up: ensure 6 sub-tabs render cleanly, no orphan keys, no broken links.

**Tasks:**
1. `npm run build && npm run test` — confirm Step 1 changes compile + unit tests stay green.
2. Visual smoke (`npm run dev`): navigate to `/jabali-admin/security?tab=malware`, verify 6 sub-tab cards render in order Overview / Quarantine / Events / ManualScan / YARA / Settings. No "Tetragon" string anywhere.
3. Settings tab: confirm `tetragon_enabled` toggle is gone; `realtime_enabled` (LMD monitor opt-in) stays.
4. Events tab Source filter: confirm dropdown shows `maldet | clamav | yara` (3 options, not 4).

**Verification:**
```bash
cd panel-ui && npm run build
grep -ri "tetragon\|Tetragon" src/
# Expected: zero matches
```

**Exit:** UI compiles green, no Tetragon string anywhere in src/, 6-card layout renders.

---

## Step 5 — agent `security.audit.{recent,by_user}` + UI "Exec audit" card

**Branch:** continues `m39/remove-tetragon-narrow-auditd`.

**Brief:** Two thin agent commands that shell out to `ausearch` and parse the result. No new DB table — auditd's log file IS the storage. New "Exec audit" sub-tab card on Malware tab.

**Tasks:**
1. **panel-agent `internal/commands/security_audit.go`:**
   - `security.audit.recent` — params `{limit?:int=100}` → invokes `ausearch -k jabali_susp_exec --raw --start recent`, parses N most recent rows (key fields: timestamp, auid, comm, exe, ppid, uid, pid). Returns `{events: [{ts, auid, username, comm, exe, ppid}]}`. Username resolved via `getent passwd <auid>` once per unique auid in the batch (cache-friendly).
   - `security.audit.by_user` — params `{user_id:string, limit?:int=100, since?:rfc3339}` → caller passes username (resolved panel-api side from user_id), agent filters with `ausearch -k jabali_susp_exec --raw --start <since>` then awks by auid. Returns same envelope.
   - Both commands time-bounded (15s context); ausearch can be slow on large logs.
2. **panel-api `internal/api/security_malware.go`:**
   - `GET /api/v1/admin/security/malware/audit/recent?limit=100` → forwards to `security.audit.recent`.
   - `GET /api/v1/admin/users/:id/audit?limit=100` → resolves user → username → calls `security.audit.by_user`.
3. **panel-ui:**
   - New `ExecAuditCard` component on the Malware tab (between Events and ManualScan). Reads `/admin/security/malware/audit/recent`. AntD Table: Time | User | Binary | Parent | exe-path. Refresh button + 60s auto-poll.
   - Per-user view: link from Events row → `/jabali-admin/users/:id` → new "Exec audit" tab fetches `/admin/users/:id/audit`.
4. Add Sub-tab card to layout, total cards now **7** again (Overview / Quarantine / Events / ManualScan / YARA / **ExecAudit** / Settings).

**Verification:**
```bash
cd panel-api && go build ./... && go test ./...
cd ../panel-agent && go build ./... && go test ./...
cd ../panel-ui && npm run build
# Live VM:
ssh root@192.168.100.150 'sudo -u testuser -- /usr/bin/curl -s http://example.com >/dev/null'
# Visit panel: /jabali-admin/security?tab=malware → ExecAudit card shows the curl row
```

**Exit:** ExecAudit card renders rows from a live host; per-user audit accessible from user detail page.

---

## Step 6 — ADR amendments + runbook + live-VM smoke + memory updates

**Branch:** continues `m39/remove-tetragon-narrow-auditd`.

**Tasks:**

1. **Amend `docs/adr/0072-malware-detection-stack.md`** — append "Amendment 2026-04-XX — Remove Tetragon, replace with narrow-scoped auditd." Document why removed (per Why section above), what replaces it (Step 3 rule file + Step 5 agent commands), operator action (`jabali update` runs `cleanup_tetragon_legacy()`, no manual SSH).
2. **New `docs/adr/0085-narrow-scoped-auditd-exec-audit.md`** (Status: Accepted). Sections:
   - Context — Tetragon removal + need for L3 forensic audit replacement.
   - Decision — narrow auditd rule (11 binaries, auid≥1000 filter, single key).
   - Alternatives considered — full `-S execve` (rejected, noise), Tetragon (removed, see ADR-0072 amendment), AppArmor (out of scope; defense-in-depth at daemon layer is M40), bpftrace ad-hoc (operator-only, doesn't surface in UI).
   - Consequences (positive: signal-rich, mature tool, no BTF; negative: lose syscall-class breadth that Tetragon had — accepted because we never used it).
   - Operator action: `jabali update`.
3. **New `plans/m39-remove-tetragon-narrow-auditd-runbook.md`:**
   - What it is in one sentence.
   - Files / units / commands table.
   - Daily checks: `auditctl -l | grep jabali_susp_exec | wc -l` should = 11; `du -sh /var/log/audit/`; `ausearch -k jabali_susp_exec --summary`.
   - Troubleshooting noisy auditd: how to add a binary to the exclusion list; how to filter for one user; how to extend rotation.
   - Rollback (auditd disable + rule file remove, only if a user pushes back).
4. **Update `docs/BLUEPRINT.md`** — amend M33 row noting Tetragon removed in M39; add M39 row to changelog table.
5. **Update `docs/adr/README.md`** index — mark ADR-0072 as "Accepted (amended 2026-04-XX — Tetragon removed)"; add ADR-0085.
6. **Live-VM smoke on 192.168.100.150:**
   - Pre-state: confirm Tetragon was installed (`systemctl status tetragon`).
   - Run `jabali update`.
   - Post-state: Tetragon gone (Step 2 verification block); auditd up + 11 rules loaded; ExecAudit card renders rows.
   - Webshell test: plant a PHP file in a docroot that invokes `curl http://192.0.2.1/sink` via PHP's process API; hit via nginx; observe row in ExecAudit card with the user's auid.
   - Negative test: WordPress wp-cron over 5 minutes produces zero `jabali_susp_exec` rows.
7. Update memory: replace `feedback_xxx` if any reference Tetragon as part of malware stack.

**Exit:** ADRs amended, runbook published, BLUEPRINT updated, live VM clean, smoke pass.

---

## Step 7 — DEFERRED — M14 `exec.audit.burst` event source

Only ship if operator demand surfaces (e.g., "I want a Slack ping when a user spawns >5 suspicious binaries in a minute"). Same pattern as M34 `egress.drop.burst`: 60s tick, threshold from `server_settings.audit_burst_threshold` (default 5/min), per-user 15-min cooldown. Skipped from initial release to avoid alert fatigue — operators read the ExecAudit card during incident response, not at 03:00.

---

## Notes

- **ClamAV state (per ADR-0072 amendment 2):** clamscan binary kept (on-demand only, daemons masked); freshclam timer kept. M39 does NOT touch ClamAV — only Tetragon.
- **YARA stays.** Custom rules + php.yar webshell heuristics are operator-loved.
- **maldet / LMD stays.** Inotify watches are doing real work.
- **Migration coordination:** Step 1's drop migration claims the next free number AFTER all in-flight branches land on main. Coordinate via memory `feedback_merge_audit_migrations`.
- **No agents per `feedback_never_agents`** — execute every step inline.
