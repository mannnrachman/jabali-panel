# M42 — AIDE file integrity monitoring (system binaries + configs)

**Status:** drafted 2026-04-30 · branch `m42/aide-fim-system-integrity`

**ADR target:** new **0087** — AIDE for system-file integrity (light mode, daily check).

**Companion to:** M33 (LMD scans user docroots), M39 (auditd narrow exec audit), M40 (AppArmor daemon confinement).

## Why

LMD (M33) covers the user-docroot tier — webshells dropped into `/home/<user>/domains/<d>/public_html/`. **It does NOT watch system binaries or configs.** A successful post-exploit step where attacker tampers with `/etc/passwd`, `/etc/shadow`, sshd config, panel binaries, cron files, or `/usr/local/bin/jabali-*` is invisible to LMD.

**AIDE is the missing FIM layer.** Mature (1999-vintage tripwire successor, in Debian main), light, single binary, declarative include/exclude, daily check via timer.

**Watch targets (system-file scope, NOT user files):**
- `/bin /sbin /usr/bin /usr/sbin /usr/local/bin /usr/local/sbin` — system + jabali binaries.
- `/lib /lib64 /usr/lib` — shared libs.
- `/etc` — system configs (with carve-outs).
- `/boot` — kernel + initrd + bootloader.
- `/root` — root home (script tampering surface).

**Excluded (because we modify them, or they rotate):**
- `/etc/jabali/` — panel writes config here.
- `/etc/letsencrypt/live/`, `/etc/letsencrypt/archive/` — cert renewal changes checksums.
- `/etc/nftables.d/jabali-*` — M34 reconciler writes.
- `/etc/audit/rules.d/jabali-*` — M39 reconciler writes.
- `/etc/nginx/sites-{available,enabled}/jabali-*` — reconciler writes per-domain vhosts.
- `/etc/php/*/fpm/pool.d/jabali-*` — M9 pool manager writes.
- `/etc/systemd/system/jabali-*` + `/etc/systemd/system/user-*.slice.d/` — M18 + reconciler.
- `/var/lib/jabali-*`, `/var/log/`, `/run/`, `/proc/`, `/sys/`, `/tmp/`, `/home/`.

**Daily check via systemd timer.** Diff goes to M14 event source `aide.tamper.detected` (one event per check run, low cardinality, high signal).

**Re-baseline on `jabali update`.** When panel binaries update (`jabali-panel-api`, `jabali-agent`, `jabali-bulwark`), their checksums change — AIDE would scream. `jabali update` post-step re-initialises the AIDE DB *only* for `/usr/local/bin/jabali-*` paths.

## Verification — what "done" means

1. `dpkg -l aide` returns installed.
2. `/var/lib/aide/aide.db` exists, root:root 0600, age < 30 days.
3. `systemctl is-active jabali-aide-check.timer` = active.
4. Manual probe: `aide --check` returns "AIDE found NO differences between database and filesystem" on a clean host.
5. Synthetic tamper test: `echo malicious >> /usr/local/sbin/test-tamper-target` (a deliberate marker file), wait one timer tick, observe M14 event `aide.tamper.detected` fires with file count + diff summary in `events_log`.
6. WordPress install + DNS zone create + user create over 24 hours produces ZERO false-positive AIDE diffs (confirms exclude list correctness).
7. `jabali update` (with a panel binary actually changing) produces ZERO AIDE alerts (confirms re-baseline hook).

## Caveman wave map

| Wave | Steps | Parallel? | Ship-ready exit |
|---|---|---|---|
| A | 1 → 2 | sequential | install.sh `install_aide()` + canonical /etc/aide/aide.conf + initial DB build |
| B | 3 ‖ 4 | parallel after A | systemd timer + agent check command + UI status surface |
| C | 5 | sequential after B | M14 event source + jabali update re-baseline hook + live-VM smoke |
| D | 6 | sequential after C | ADR-0087 + runbook + memory |

---

## Step 1 — install.sh `install_aide()` + canonical aide.conf

**Branch:** `m42/aide-fim-system-integrity` (root branch)

**Brief:** AIDE is in Debian 13 main. Install + author a single-source-of-truth `/etc/aide/aide.conf` covering the watch + exclude list. Initial DB build is one-time (idempotent guard — only re-init if missing OR if `--force`).

**Tasks:**
1. Add `install_aide()` to install.sh (called from main install pipeline AFTER `install_audit_exec` from M39):
   - `apt install -y aide aide-common` (only if missing).
   - Render `/etc/aide/aide.conf` with the canonical config:
     ```
     # Jabali — system-file integrity. Excludes paths the panel writes to.
     database=file:/var/lib/aide/aide.db
     database_out=file:/var/lib/aide/aide.db.new
     gzip_dbout=yes
     report_url=file:/var/log/aide/aide.report.log
     report_url=stdout

     # Strong default rule: hash + meta but skip atime (mtime+ctime catch tamper).
     RULE = p+i+n+u+g+s+m+c+sha256

     # WATCH:
     /bin            RULE
     /sbin           RULE
     /usr/bin        RULE
     /usr/sbin       RULE
     /usr/local/bin  RULE
     /usr/local/sbin RULE
     /lib            RULE
     /lib64          RULE
     /usr/lib        RULE
     /etc            RULE
     /boot           RULE
     /root           RULE

     # EXCLUDE:
     !/etc/jabali
     !/etc/letsencrypt/live
     !/etc/letsencrypt/archive
     !/etc/nftables.d/jabali-.*
     !/etc/audit/rules.d/jabali-.*
     !/etc/nginx/sites-available/jabali-.*
     !/etc/nginx/sites-enabled/jabali-.*
     !/etc/php/.*/fpm/pool.d/jabali-.*
     !/etc/systemd/system/jabali-.*
     !/etc/systemd/system/user-.*\.slice\.d
     !/etc/cron.d/jabali-.*
     !/var
     !/run
     !/proc
     !/sys
     !/tmp
     !/home
     !/dev
     !/mnt
     !/media
     !/lost\+found
     !/etc/mtab
     !/etc/resolv.conf
     !/etc/adjtime
     !/etc/machine-id
     !/etc/ssh/ssh_host_.*_key.*
     ```
   - **Initial DB build:** if `/var/lib/aide/aide.db` does not exist, run `aide --init && mv /var/lib/aide/aide.db.new /var/lib/aide/aide.db`. Build takes ~2-5 minutes on a typical host. Run async with a marker (`/var/lib/aide/.init-in-progress`) so the install pipeline doesn't block.
   - **Sentinel:** `/var/lib/aide/.jabali-installed` records the install timestamp. install.sh skips re-init if sentinel exists; operator forces re-init via `jabali aide rebuild` (Step 5 CLI).
2. **Permissions:** `/var/lib/aide/aide.db` root:root 0600 (single-file DB; if compromised, attacker can rebuild it with stale-but-clean checksums — AIDE's standard limitation).

**Verification:**
```bash
ssh root@192.168.100.150 'dpkg -l aide | grep -i installed'
ssh root@192.168.100.150 'ls -la /var/lib/aide/aide.db'
ssh root@192.168.100.150 'wc -l /etc/aide/aide.conf'
ssh root@192.168.100.150 'aide --check 2>&1 | tail -5'
# Expected: "AIDE found NO differences between database and filesystem"
```

**Exit:** AIDE installed, canonical config in place, initial DB built, clean check.

---

## Step 2 — Tighten the exclude list via dry-run

**Branch:** continues `m42/aide-fim-system-integrity`.

**Brief:** Step 1 ships an exclude list based on memory of where the panel writes. This step verifies on the testbed by running `aide --check` after a full panel workload (user create, WP install, mail flow, scheduled scan, jabali update) and adjusting excludes for any FP that surfaces. NOT an open-ended sweep — bounded to one 24-hour soak.

**Tasks:**
1. Run a representative workload on 192.168.100.150:
   - Create a hosting user via panel.
   - Install WordPress.
   - Run a backup.
   - Send a test mail.
   - Wait for a scheduled malware scan.
   - Run `jabali update`.
2. Run `aide --check` after each step; record FPs.
3. Adjust `/etc/aide/aide.conf` exclude list for any path that legitimately changed. Common suspects:
   - `/etc/letsencrypt/csr/`, `/etc/letsencrypt/keys/`, `/etc/letsencrypt/renewal/`, `/etc/letsencrypt/accounts/`.
   - `/etc/cron.daily/apt-compat`, `/etc/cron.d/anacron` (cron-related rotation).
   - `/etc/group`, `/etc/passwd`, `/etc/shadow`, `/etc/gshadow` — when user-create runs, these change. Decide: include (high-signal alert on every user-create) vs exclude. **Default include** — operator wants to know when these change, even if it's a panel-driven user-create. Document in runbook so the M14 alert is expected.
   - `/etc/aliases`, `/etc/aliases.db` — Stalwart writes these on M6.x mail-domain setup.
4. Re-render canonical config based on findings; `aide --update` to rebuild DB without losing the previous.

**Verification:**
```bash
# After 24-hour soak:
ssh root@192.168.100.150 'aide --check 2>&1 | grep -E "added|changed|removed" | wc -l'
# Expected: 0 (or a small list the operator deliberately accepted, like /etc/passwd on user-create)
```

**Exit:** Exclude list tuned to zero spurious FPs over a 24-hour realistic workload.

---

## Step 3 — systemd timer + agent `security.aide.{check,status}` commands

**Branch:** continues `m42/aide-fim-system-integrity` (parallel-safe with Step 4).

**Brief:** Daily check via systemd timer; agent commands surface the latest report to panel-api. NOT real-time inotify (too noisy + too high overhead for a system-file watcher; daily is the correct cadence for FIM).

**Tasks:**
1. **`install/systemd/jabali-aide-check.service` + `.timer`:**
   - Service: `ExecStart=/usr/bin/aide --check`. Output captured to `/var/log/aide/aide.report.log` (rotated by logrotate, keep 30 days).
   - Hardened: `ProtectSystem=strict ReadWritePaths=/var/log/aide /var/lib/aide ProtectHome=true PrivateTmp=true CapabilityBoundingSet=`.
   - Timer: `OnCalendar=daily 04:30 UTC` (between maldet scan-daily 03:00 and quarantine purge 04:00 — non-overlapping).
   - 15-minute jitter via `RandomizedDelaySec=900` to spread fleet load.
2. **panel-agent `internal/commands/security_aide.go`:**
   - `security.aide.status` → reads `/var/log/aide/aide.report.log` (latest report) + `/var/lib/aide/.jabali-installed` (DB age). Returns `{db_age_seconds, last_check_ts, summary: {added, changed, removed}, sample: [{path, change_type}]}` (sample capped at 50).
   - `security.aide.check` (admin only, manual trigger) → invokes `aide --check`, returns immediately with a job_id (long-running). `security.aide.check_status` polled by panel-api to read result.
3. **panel-api `internal/api/security_malware.go`:** add to existing security/malware tab — new endpoint group `/api/v1/admin/security/aide/{status,check}`.

**Verification:**
```bash
ssh root@192.168.100.150 'systemctl status jabali-aide-check.timer'
# Expected: active (waiting), next trigger ~tomorrow 04:30 UTC
ssh root@192.168.100.150 'systemctl start jabali-aide-check.service && journalctl -u jabali-aide-check --since "1 minute ago"'
# Expected: aide --check ran, report written
```

**Exit:** Timer active, manual run produces report, agent commands return live data.

---

## Step 4 — UI: AIDE status card on Security tab

**Branch:** continues `m42/aide-fim-system-integrity` (parallel-safe with Step 3).

**Brief:** Read-only operator view + manual-recheck button. AIDE is one of those tools operators want to verify, not configure.

**Tasks:**
1. **panel-ui:** new `AideCard` component on the admin Security tab (or sub-tab next to ExecAudit from M39). AntD layout:
   - Stat row: DB age (humanized), Last check (relative time), Summary {added, changed, removed} (badges).
   - Table (collapsible): sample of changed paths (path, change type, file mtime).
   - Actions: "Run check now" button (POST `/admin/security/aide/check`), "Re-baseline" button (POST `/admin/security/aide/rebuild` — destructive, behind confirm modal).
2. **Hook:** `useAide.ts` with `useAideStatus()` (60s poll), `useRunAideCheck()`, `useRebuildAide()`.

**Verification:**
```bash
cd panel-ui && npm run build
# Live: panel UI shows DB age, last-check timestamp, zero changes on a clean host
```

**Exit:** Operator sees AIDE state without SSH; can trigger check + re-baseline from the UI.

---

## Step 5 — M14 `aide.tamper.detected` event source + jabali update re-baseline hook + live-VM smoke

**Branch:** continues `m42/aide-fim-system-integrity`.

**Tasks:**
1. **panel-api `internal/eventsources/aide.go`:**
   - Tick: 5 minutes (lighter than typical — AIDE only reports daily so polling more often is wasted).
   - Reads `security.aide.status`. Fires `aide.tamper.detected` envelope when the latest check has `summary.added + summary.changed + summary.removed > 0`.
   - Per-host cooldown 24 hours (one alert per check run; operator acks via the UI, next day's check resets).
   - Severity tier: `critical` if any of `/etc/passwd /etc/shadow /etc/sudoers /usr/local/bin/jabali-* /root/.ssh/authorized_keys` changed; `high` otherwise.
2. **`jabali update` re-baseline hook:** post-update step calls `jabali aide rebuild --paths /usr/local/bin/jabali-*` (re-init only the changed binaries, not the whole DB). New CLI subcommand:
   - `jabali aide rebuild [--paths <glob>] [--full]` — `--paths` does a partial DB update (uses `aide --update` filtered); `--full` does a complete `aide --init`.
3. **Live-VM smoke on 192.168.100.150:**
   - Pre-state: AIDE installed, DB initialised, timer active, zero diffs.
   - Synthetic tamper test: `echo "$(date)" >> /usr/local/sbin/test-tamper-target` (a deliberate marker file the operator created to avoid corrupting real binaries).
   - Wait for next timer tick (or trigger manually via UI).
   - Observe AideCard shows 1 added/changed file; M14 dispatches `aide.tamper.detected` with severity=high; configured channels (Slack/email/etc.) receive the alert.
   - Re-baseline test: run `jabali update`. Confirm panel binary checksums change in `/usr/local/bin/`. Confirm post-update AIDE check shows ZERO diffs (re-baseline hook covered them).
   - Critical-tier test: tamper a real-but-recoverable file like `/root/.bashrc` (operator backed up first). Confirm M14 fires with severity=critical.

**Verification:**
```bash
ssh root@192.168.100.150 'jabali update && systemctl start jabali-aide-check && journalctl -u jabali-aide-check --since "1 minute ago" | grep "NO differences"'
# Expected: re-baseline successful; clean check
```

**Exit:** M14 event fires on real tamper; re-baseline absorbs jabali updates without false positives.

---

## Step 6 — ADR-0087 + runbook + memory + BLUEPRINT

**Branch:** continues `m42/aide-fim-system-integrity`.

**Tasks:**
1. **New `docs/adr/0087-aide-system-fim.md`** (Status: Accepted). Sections:
   - Context — LMD covers user docroots; system binaries + configs are unmonitored gap; AIDE is mature, light, in Debian main.
   - Decision — AIDE with daily timer, exclude list scoped to panel-writeable paths, M14 event source, re-baseline hook on `jabali update`.
   - Alternatives — Tripwire (commercial; AIDE is the open successor), OSSEC (broader scope; M27 already does CrowdSec), Samhain (heavier, daemon model overkill for daily-check use case), inotify-only (high overhead, doesn't catch offline tamper).
   - Consequences (positive: closes system-file gap; negative: DB-on-same-host limitation — sophisticated attacker rebuilds AIDE DB after tamper; mitigated partially by 0600 perms, fully only by off-host DB shipping which is phase 2).
   - Operator action: `jabali update` runs `install_aide()` idempotently.
2. **New `plans/m42-aide-fim-runbook.md`:**
   - What it is in one sentence.
   - Files / units / commands.
   - Daily checks: `systemctl status jabali-aide-check.timer`; `du -sh /var/lib/aide/`; `tail /var/log/aide/aide.report.log`.
   - Investigating a tamper alert: how to read the report, how to identify the changed file, how to triage legit vs hostile.
   - Adding a new path to the exclude list (when a vendor package starts writing somewhere that wasn't excluded).
   - Re-baseline procedure (after a deliberate change — kernel bump, manual config edit).
   - Off-host DB shipping (phase 2 deferral): document the standard tripwire/AIDE approach (sign DB, ship to S3, verify before each check) and explain why we deferred (operational complexity for marginal gain on most threat models).
3. **`docs/BLUEPRINT.md`** — add M42 row to changelog.
4. **`docs/adr/README.md`** — add ADR-0087.
5. **Memory updates:** new entry `project_m42_aide_shipped.md`.

**Exit:** Documentation complete, runbook published, memory updated.

---

## Notes

- **DB-on-same-host limitation is real but accepted.** A root-level attacker can `aide --init` after their tamper and rotate the DB. Mitigations: 0600 perms, daily off-host backup of aide.db (M30 backup already covers `/var/lib/`), severity-critical M14 alert on `/var/lib/aide/aide.db` itself if it changes between checks. Off-host DB shipping is phase 2.
- **Don't watch `/home/`** — that's LMD's job. Overlap = noise.
- **Don't watch `/var/log/`** — log rotation guarantees changes daily; checksums useless.
- **Daily cadence is correct** — system-file tamper is rare, alerts must be actionable. More-frequent checks burn CPU + I/O without surfacing more incidents.
- **No agents per `feedback_never_agents`** — execute every step inline.
- **Migration coordination with M39 / M40:** none needed — M42 adds zero schema. Single touch on `eventsources/` and `api/security_malware.go`; cleanly stacks onto either.
