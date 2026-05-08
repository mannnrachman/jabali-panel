# Plan: real per-user systemd slices

**Objective.** Give every panel user a real cgroup boundary —
`jabali.slice → jabali-user.slice → jabali-user-<user>.slice` — so that
FPM workers, crons, and shell sessions all live under the user's own
slice. Lets us enforce per-user `MemoryHigh`, `CPUQuota`, `TasksMax`,
and gives a clean "kill slice, kill everything" abort switch.

Core deltas from today:
- FPM moves from one **system-wide** `php<v>-fpm.service` to **one master per user** (`jabali-fpm@<user>.service`), pinned to the user's pool config and placed in the user's slice.
- Per-user slice unit is created by the agent on `user.create`, removed on `user.delete`.
- Per-user `user@<uid>.service` is re-parented into `jabali-user-<u>.slice` so login shells + systemd-user timers land in the slice.
- Global `php<v>-fpm.service` is masked to prevent package upgrades re-enabling it.

**Mode.** Direct (Gitea remote, no `gh`). One commit per step on `main`.

**Sequencing.** 1 → 2 → 3 → 4 → 5 → 6 strictly sequential (each builds runtime state the next depends on). Steps 7 and 8 can run in parallel after 6. Step 9 is closeout.

**Invariants.** After every step:
- `go build ./...` green.
- `go test ./panel-agent/... ./panel-api/...` green (race flag on).
- `cd panel-ui && npx tsc -b && npx vite build` green.
- On the smoke-test host (a disposable LXC or VM — **not** `192.168.100.150` except during step 6 cutover): a user's vhost serves PHP via the per-user slice and `systemd-cgls /jabali.slice` shows workers + shell under the user's own child slice.
- No user-visible regression: existing bound vhosts keep serving PHP during and after the change (step 6 is the risky cutover — plan a maintenance window).

---

## Shape of what we touch

- `panel-agent/internal/commands/` — new: `user_slice_ensure.go`, `user_slice_remove.go`; modified: `user_create.go`, `user_delete.go`, `php_pool_apply.go`, `php_pool_remove.go`.
- `install.sh` — new `install_jabali_slices` step; new `/etc/systemd/system/jabali.slice`, `jabali-user.slice`, `jabali-fpm@.service` template.
- `install/systemd/` — new directory for the slice + template unit source files that `install.sh` copies into place.
- `panel-api/internal/api/` — small additions for the admin versions page to surface slice status (memory/tasks) per user. Optional; could slip to step 9 runbook instead.
- `docs/adr/0025-per-user-systemd-slices.md` — new ADR documenting the design.
- `docs/BLUEPRINT.md` — §4.10 amendment; new §4.11 for slice infra.
- `docs/runbooks/per-user-slices.md` — ops doc (diagnosis, rollback, limits tuning).

---

## Design decisions (committed in step 1, ADR-0025)

1. **Slice hierarchy.** `jabali.slice` (top-level under `-.slice`) → `jabali-user.slice` (container for all tenant slices) → `jabali-user-<user>.slice` (one per panel user). The `-` separator in slice names is significant: systemd interprets `foo-bar.slice` as nested under `foo.slice`.
2. **FPM master per user, not per server.** One systemd service unit per user runs `php-fpm<ver> -y /etc/php/<ver>/fpm/pool.d/<user>.conf -F`, with `Slice=jabali-user-<user>.slice`. Disable + mask the global `php<v>-fpm.service`. Trade: ~10–20 MB RSS per master × N users (acceptable up to hundreds of users; review at 1k).
3. **Service unit shape: templated on user, version baked into drop-in.** `jabali-fpm@<user>.service` is a template (`%i=<user>`) whose ExecStart reads the pinned PHP version from `/etc/jabali-panel/user-phpver/<user>` (a one-line file written by the agent on pool apply). Switching a user's PHP version = rewrite the file + `systemctl restart jabali-fpm@<user>.service`. No systemd reload needed because ExecStart is dynamic via a small shim script.
4. **No FPM multi-version per user (MVP).** Matches ADR-0023 §3 ("one pool per user"). A user has exactly one FPM master at any time. If someday we want multi-version, the template becomes `jabali-fpm-<ver>@<user>.service` — that migration is out of scope here.
5. **Shell + cron capture via `user@<uid>.service` drop-in.** Write `/etc/systemd/system/user-<uid>.slice.d/jabali.conf` that overrides `Slice=jabali-user-<username>.slice`. systemd places `user@<uid>.service` and its spawned processes (logind sessions, `systemd --user`, user crons run via `systemd --user` timers) under the jabali slice. Traditional `cron.service`-launched crontabs are **not** captured (they run under `cron.service`'s cgroup) — acceptable MVP gap; documented in the runbook.
6. **Resource defaults at `jabali-user.slice`.** Start permissive: `MemoryAccounting=yes`, `TasksAccounting=yes`, `CPUAccounting=yes`. No hard caps in MVP — admins tune per-user via pool ini overrides or a later admin-UI feature. Accounting alone gives us `systemd-cgtop` + metrics.
7. **Cutover strategy.** Step 6 is the risky one. Approach: (a) pre-provision per-user slice + service units for all existing users; (b) start them alongside the global `php<v>-fpm.service` (both bind different socket paths — new sockets are `/run/php/php<ver>-fpm-<user>.sock` which we already use, old default pool sockets were disabled at install time per `install.sh`); (c) verify each user's vhost is still served via its own socket (nginx config already targets `php<ver>-fpm-<user>.sock`, unchanged); (d) stop + disable + mask `php<v>-fpm.service`. If any user fails, skip cutover for that user and flag.
8. **Rollback.** `systemctl unmask php<v>-fpm.service && systemctl start php<v>-fpm.service` restores the old master. Per-user units can stay — they don't conflict on socket paths. The per-user sockets survive.

---

## Step 1 — ADR-0025 + blueprint shape

**Parallel:** no (foundation). **Model tier:** strongest (design lock). **Agent:** `adr-architect`. **Complexity:** LOW.

### Context brief

Capture the design above in a MADR-3.0 ADR. This ADR supersedes *nothing* in ADR-0023 (pool-per-user remains); it refines the runtime placement of those pools. Cross-link: ADR-0023 §3 (one pool per user), the phpMyAdmin SSO plan (doesn't interact), and M9 Step 7 reconciler (creates default pool; slice provisioning is a follow-up on the reconciler path per step 5 below).

### Tasks

1. Create `docs/adr/0025-per-user-systemd-slices.md` with the 8 decisions above as subsections (Decision / Alternatives / Consequences).
2. Add entry to `docs/adr/README.md` table.
3. Amend `docs/BLUEPRINT.md` §4.10 (PHP/FPM pools) with a forward-reference: "slice placement tracked in ADR-0025". Do NOT add §4.11 yet — that's step 9.
4. Amend `docs/adr/0023-m9-php-fpm-pool-manager.md` header with a cross-ref note (review L4): "Runtime placement refined by ADR-0025 (per-user systemd slices)." Not a supersession — the pool-per-user decision stands — just a pointer so future readers of ADR-0023 find the slice context.
5. Add park banner removal note: no banners to touch here; SSO plan is already unparked.

### Verification

- `grep -q "ADR-0025" docs/adr/README.md`
- `test -f docs/adr/0025-per-user-systemd-slices.md`
- The 8 decision titles match the list above.

### Exit criteria

- ADR-0025 accepted, README indexed.
- Commit: `docs(adr): ADR-0025 per-user systemd slices`.

---

## Step 2 — Install top-of-hierarchy slice units

**Parallel:** no. **Model tier:** default. **Agent:** `coder`. **Complexity:** LOW.

### Context brief

Two static slice units need to exist on every jabali host: `jabali.slice` and `jabali-user.slice`. These don't depend on any user existing — they're the container that per-user slices attach to. Also install the FPM template service unit here (not per-user — the template is shared).

### Tasks

1. Create `install/systemd/jabali.slice`:
   ```ini
   [Unit]
   Description=Jabali Panel root slice
   Before=slices.target
   [Slice]
   CPUAccounting=yes
   MemoryAccounting=yes
   TasksAccounting=yes
   ```
2. Create `install/systemd/jabali-user.slice` (soft resource defaults; admin-tunable):
   ```ini
   [Unit]
   Description=Jabali hosted users container slice
   Before=slices.target
   [Slice]
   CPUAccounting=yes
   MemoryAccounting=yes
   TasksAccounting=yes
   # Soft caps applied to the AGGREGATE of all hosted users.
   # Individual users get no per-slice caps in MVP — admin tunes via
   # drop-ins at /etc/systemd/system/jabali-user-<user>.slice.d/.
   # Rationale (from review F5): prevents a single runaway user from
   # eating the whole host without blocking legitimate bursts.
   MemoryHigh=80%
   TasksMax=infinity
   ```
3. Create `install/systemd/jabali-fpm@.service` (template, non-instantiable directly):
   ```ini
   [Unit]
   Description=PHP-FPM master for jabali user %i
   After=network.target
   # Slice set dynamically via drop-in per user (step 3 writes it).

   [Service]
   # Type=simple, NOT notify — php-fpm 7.x–8.x does not implement sd_notify
   # (review F1). FPM's `-F` in fpm-exec keeps the master in foreground so
   # systemd can supervise it directly.
   Type=simple
   ExecStartPre=/usr/local/libexec/jabali/fpm-pre-start %i
   ExecStart=/usr/local/libexec/jabali/fpm-exec %i
   ExecReload=/bin/kill -USR2 $MAINPID
   Restart=on-failure
   RestartSec=5s

   [Install]
   WantedBy=multi-user.target
   ```
4. Create `install/systemd/fpm-pre-start` and `install/systemd/fpm-exec` shim scripts:
   - `fpm-pre-start <user>`: validates `/etc/jabali-panel/user-phpver/<user>` exists; ensures pool file `/etc/php/<ver>/fpm/pool.d/<user>.conf` exists; mkdir -p `/run/php` owned `root:root` 0755.
   - `fpm-exec <user>`: reads version, execs `/usr/sbin/php-fpm<ver> --nodaemonize --fpm-config /etc/php/<ver>/fpm/php-fpm.conf --pid /run/php/jabali-fpm-<user>.pid`.
5. Add `install_jabali_slices` step to `install.sh`, invoked from `main()` *before* `install_php` (slices should exist before any FPM lives):
   - Copy the three unit files to `/etc/systemd/system/`.
   - Install the two shims to `/usr/local/libexec/jabali/` mode 0755 root:root.
   - `systemctl daemon-reload`.
   - `systemctl start jabali.slice jabali-user.slice` (starting a slice is a no-op but validates the unit).
6. No agent or panel changes yet.

### Verification

- On a fresh smoke-test host after `bash install.sh`:
  - `systemctl is-active jabali.slice` → `active`.
  - `systemctl cat jabali-fpm@.service` shows the template.
  - `systemd-analyze verify /etc/systemd/system/jabali-fpm@.service` is silent.
  - `/usr/local/libexec/jabali/fpm-{pre-start,exec}` exist and are executable.

### Exit criteria

- Install script provisions the container slices + FPM template.
- Commit: `feat(systemd): install jabali root + user container slices + FPM template`.

---

## Step 3 — Agent commands: `user.slice.ensure` / `user.slice.remove`

**Parallel:** no. **Model tier:** default. **Agent:** `coder`. **Complexity:** MEDIUM.

### Context brief

Two new agent commands (registered via `commands.Default.Register` in `registry.go`) manage the per-user slice unit + the FPM service unit enablement. They do NOT start FPM yet — that's step 4. They write systemd unit files, daemon-reload, and return the paths.

### Tasks

1. `panel-agent/internal/commands/user_slice_ensure.go`:
   - Params: `{username}`.
   - Validate username matches `^[a-z][a-z0-9_-]{0,31}$` (existing regex).
   - Write `/etc/systemd/system/jabali-user-<user>.slice` — nesting under
     `jabali-user.slice` is achieved purely by the `-` separator in the
     name; **do not** add `PartOf=` (review F2: invalid on slice units):
     ```ini
     [Unit]
     Description=Jabali hosted user <user>
     Before=slices.target
     [Slice]
     CPUAccounting=yes
     MemoryAccounting=yes
     TasksAccounting=yes
     ```
   - Write `/etc/systemd/system/jabali-fpm@<user>.service.d/slice.conf` —
     per-instance drop-in on a template unit (valid path; instance drop-ins
     live at `<template>@<instance>.service.d/`):
     ```ini
     [Service]
     Slice=jabali-user-<user>.slice
     User=<user>
     Group=<user>
     ```
   - Re-parent the login user manager — fetch uid via `id -u <user>` and
     write `/etc/systemd/system/user@<uid>.service.d/jabali.conf` (review
     F4: the drop-in lives on `user@<uid>.service`, **not** on
     `user-<uid>.slice`; the section is `[Service]`, **not** `[Slice]`):
     ```ini
     [Service]
     Slice=jabali-user-<user>.slice
     ```
   - `systemctl daemon-reload`.
   - Return `{username, slice_unit_path, fpm_dropin_path, login_dropin_path, uid}`.
2. `panel-agent/internal/commands/user_slice_remove.go`:
   - Params: `{username}`.
   - `systemctl stop jabali-fpm@<user>.service jabali-user-<user>.slice` (ignore "not loaded" errors).
   - `systemctl disable jabali-fpm@<user>.service` (ignore errors).
   - Remove the unit files + drop-ins.
   - `systemctl daemon-reload`.
   - Return `{username, removed: true}`.
3. Register both in `registry.go`: `user.slice.ensure`, `user.slice.remove`.
4. Tests: table-driven with a fake `systemctl` runner (substitute `exec.CommandContext` behind a func var) + a tmpdir for unit-file writes. Assert file contents match expected structure. Skip the actual daemon-reload in tests.

### Verification

- `go test ./panel-agent/internal/commands/ -run UserSlice -race` passes.
- On smoke host after invoking the command for user `testuser`:
  - `/etc/systemd/system/jabali-user-testuser.slice` exists with correct content.
  - `systemd-analyze verify /etc/systemd/system/jabali-user-testuser.slice` is silent.
  - `systemctl cat user@$(id -u testuser).service` shows `Slice=jabali-user-testuser.slice`.

### Exit criteria

- Agent commands registered and tested.
- Commit: `feat(agent): user.slice.ensure + user.slice.remove commands`.

---

## Step 4 — Pool apply wires to per-user FPM service

**Parallel:** no. **Model tier:** default. **Agent:** `coder`. **Complexity:** MEDIUM.

### Context brief

Today `php.pool.apply` writes `/etc/php/<ver>/fpm/pool.d/jabali-<user>.conf` and reloads `php<v>-fpm.service` (the global master). After this step, pool apply:
- Writes the pool file (unchanged path, keeps `jabali-<user>.conf` for now — renaming to `<user>.conf` is a separate decision tracked in chat as option (B) from earlier, not in this plan).
- Writes `/etc/jabali-panel/user-phpver/<user>` = pool's php_version (one line).
- Ensures `systemctl enable jabali-fpm@<user>.service` is set.
- Reloads (USR2) or restarts the per-user service: reload if version unchanged, restart if version changed.
- Does NOT touch the global `php<v>-fpm.service`.

### Tasks

1. Modify `panel-agent/internal/commands/php_pool_apply.go`:
   - After writing the pool file + nginx template, write the version pin file (`/etc/jabali-panel/user-phpver/<user>`).
   - Replace `reloadFPMService(ctx, p.PHPVersion)` call with a new helper `restartUserFPM(ctx, p.Username, oldVersion, newVersion)`:
     - If `oldVersion == newVersion`: `systemctl reload jabali-fpm@<user>.service` (graceful USR2).
     - Else: `systemctl restart jabali-fpm@<user>.service` (full stop/start).
     - `oldVersion` comes from the existing version-pin file on disk before this apply.
   - **Concurrency guard (review F7):** the read-check-write sequence
     (read old version → write new version → systemctl restart/reload) is
     racy if two pool-applies for the same user land at the same time. Wrap
     it in a `flock` on `/run/jabali/pool-apply-<user>.lock` using
     `golang.org/x/sys/unix.Flock`. Acquire exclusive, do the three-step,
     release on return (deferred). Timeout the lock after 30s — longer
     than any legitimate apply takes.
2. Modify `panel-agent/internal/commands/php_pool_remove.go`:
   - After removing the pool file, `systemctl stop jabali-fpm@<user>.service` (if loaded).
   - Remove the version-pin file.
3. Leave the global FPM reload helper (`reloadFPMService`) in place for step 6 migration use; remove it in step 6's commit once all callers are gone.
4. Update tests: mock the per-user service lookup; assert correct systemctl invocation.

### Verification

- Agent tests green.
- On smoke host: after a pool apply for `testuser`, `systemctl status jabali-fpm@testuser.service` shows running, PID exists, socket `/run/php/php<ver>-fpm-testuser.sock` is live.
- `systemd-cgls /jabali.slice` shows the FPM worker processes under `jabali-user-testuser.slice`.

### Exit criteria

- Pool apply routes through per-user service units.
- Commit: `feat(agent): pool.apply uses per-user jabali-fpm@ service`.

---

## Step 5 — Wire slice lifecycle into user.create / user.delete + reconciler

**Parallel:** no. **Model tier:** default. **Agent:** `coder`. **Complexity:** MEDIUM.

### Context brief

Right now `user.create` does useradd + chown. After this step it ALSO calls `user.slice.ensure` as the last provisioning action. `user.delete` calls `user.slice.remove` BEFORE userdel (so systemd stops the service while the UID is still resolvable). Also: the reconciler's `ReconcilePHPPools()` pass that currently auto-creates default pools needs to also ensure the slice exists (idempotent) — so hosts upgrading from pre-slice panels get slices backfilled on next reconcile tick.

### Tasks

1. `panel-agent/internal/commands/user_create.go`: after the existing chown, invoke the slice-ensure logic (inline call — don't RPC to self). Failure here should roll back the useradd.
2. `panel-agent/internal/commands/user_delete.go`: before userdel, invoke slice-remove. A failure here must fail the whole delete (don't leave orphan slice units pointing at a nonexistent user).
3. `panel-api/internal/reconciler/reconciler.go`: in the `ReconcilePHPPools` pass, after ensuring the default pool exists, also RPC `user.slice.ensure` to the agent. Idempotent so it's safe to call every tick (but the agent should short-circuit if unit files already exist to avoid redundant daemon-reloads — add that short-circuit in step 3's implementation).
4. Integration test: a fake end-to-end test that drives API user-create → agent user.create → verifies both useradd AND slice-ensure are called in order. Use the existing test harness.

### Verification

- `go test ./... -race` green.
- On smoke host: `curl -X POST /api/v1/users ...` creates a user AND `systemctl is-active jabali-user-<newuser>.slice` is `active`.
- Delete the user: slice + service units are gone, `/etc/systemd/system/jabali-user-<user>.slice` absent.

### Exit criteria

- User lifecycle provisions + tears down slices end-to-end.
- Commit: `feat(agent): wire user lifecycle to slice provisioning + reconciler backfill`.

---

## Step 6 — Cutover: migrate existing users, mask global `php<v>-fpm.service`

**Parallel:** no. **Model tier:** strongest (risk). **Agent:** `coder` + human-in-the-loop. **Complexity:** HIGH. **Rollback plan below.**

### Context brief

This is the destructive step. Up to this point, both masters can run in parallel (different socket paths). Now we flip production to depend on per-user masters and stop the global one.

Pre-conditions (verify all before executing):
- Steps 1–5 shipped to the target host.
- Reconciler has run ≥1 tick after step 5 deploy (so all existing users have slices).
- `systemctl is-active jabali-fpm@<user>.service` returns `active` for EVERY row in `SELECT username FROM users`.
- Admin has announced a maintenance window (recommend 10 minutes; actual impact should be seconds).

### Tasks

1. Add a new one-shot admin command: `jabali-panel admin slice-cutover [--dry-run]` (cobra subcommand). Steps it runs:

   **Phase A — preflight (runs in both normal and --dry-run):**
   a1. Enumerate every user from the DB (`SELECT username FROM users`). Fail fast if DB unreachable.
   a2. For each user: assert `/etc/systemd/system/jabali-user-<user>.slice` exists AND `systemctl is-active jabali-fpm@<user>.service` returns `active`. Collect failures.
   a3. Enumerate installed PHP versions from `/etc/php/*/fpm/pool.d`.
   a4. For each user, pick a bound domain from the DB (one vhost is enough) and record its hostname for the probe phase.
   a5. If **any** user fails (a2), print the list and exit 2 with message "preflight failed; resolve missing slices before cutover". Do NOT proceed.
   a6. If `--dry-run`, print "preflight OK; N users ready, M versions to mask" and exit 0.

   **Phase B — cutover (only when not --dry-run):**
   b1. For each version: `systemctl stop php<v>-fpm.service`.
   b2. For each version: `systemctl disable php<v>-fpm.service`.
   b3. For each version: `systemctl mask php<v>-fpm.service` (blocks apt re-enabling on upgrade; review F5).

   **Phase C — probe (review F3: must hit a .php endpoint):**
   c1. Before cutover, the installer shipped `/var/www/jabali-disabled/index.html`. That is static. We cannot probe `/` — nginx might serve a 200 from a static index with no PHP executed.
   c2. Instead: for each user picked in a4, `curl -sSo /dev/null -w "%{http_code}" http://127.0.0.1/jabali-healthcheck.php -H "Host: <domain>"` — expect 200.
   c3. The health file must exist at `/home/<user>/<domain-docroot>/jabali-healthcheck.php`. Step 5 (reconciler) should be extended to write this file on pool-apply: a one-liner `<?php echo "ok"; ?>` owned by `<user>:www-data` mode 0644. On cutover the probe confirms FPM is actually executing PHP.
   c4. If any probe returns non-200 or a connection error: execute full rollback (next section) and exit 1 with the failing user list.

   **Phase D — finalize:**
   d1. Log cutover complete + duration.
   d2. Drop `reloadFPMService` from the agent pool-apply code (it has no remaining callers after step 4 + this step).
2. Document the command in `docs/runbooks/per-user-slices.md`.
3. Remove the now-unused `reloadFPMService` helper from `php_pool_apply.go`.

### Verification (on smoke host FIRST, then production)

- `jabali-panel admin slice-cutover` completes with "cutover complete" and all probes green.
- `systemctl is-enabled php<v>-fpm.service` = `masked` for every installed version.
- `systemctl status jabali-fpm@<user>.service` is `active (running)` for every user.
- Load a domain in the browser: PHP pages render.
- `systemd-cgtop /jabali.slice` shows live workers under per-user slices.

### Rollback

If cutover fails mid-way:
```
for v in 8.5 8.4 8.3 8.2 8.1 8.0 7.4; do
  systemctl unmask php${v}-fpm.service 2>/dev/null
  systemctl enable --now php${v}-fpm.service 2>/dev/null
done
```
Per-user units can stay — they're harmless. Pool files live at
`/run/php/php<ver>-fpm-<user>.sock` (unique per user), no collision with
the global master's default socket.

**Verify rollback worked (review L2):**
```
systemctl status php<v>-fpm.service    # expect: active (running)
curl -sSo /dev/null -w "%{http_code}" http://127.0.0.1/jabali-healthcheck.php -H "Host: <bound-domain>"
                                        # expect: 200
```
Reconciler continues provisioning slices on the side; operator
re-runs cutover after fixing the root cause.

### Exit criteria

- Production runs entirely on per-user FPM masters.
- Global `php<v>-fpm.service` masked on all target hosts.
- Commit: `feat(m9.5): cutover to per-user FPM masters; mask global services`.

---

## Step 7 — Shell + systemd-user timer capture

**Parallel:** yes, with step 8 after step 6. **Model tier:** default. **Agent:** `coder`. **Complexity:** MEDIUM.

### Context brief

Step 3 already wrote the `user@<uid>.service.d/jabali.conf` drop-in. But for the drop-in to take effect, (a) the user manager must exist (needs `loginctl enable-linger <user>` for users who may not be logged in), and (b) systemd needs to know about the UID mapping. This step closes those gaps and documents what IS and IS NOT captured.

Traditional `cron.service`-run crontabs are NOT captured — they stay under `cron.service`. That's acceptable for MVP; noted in the runbook with the migration path (systemd-user timers).

### Tasks

1. In `user.slice.ensure` (extend from step 3): after writing drop-ins, run `loginctl enable-linger <user>` so the user manager persists without an active login. **Error handling (review F10):** capture stdout+stderr; treat exit 0 OR stderr matching `"already"` as success; any other nonzero exit returns an AgentError with the captured stderr. Log the linger enable result to slog so admins can audit.
2. Write `docs/runbooks/per-user-slices.md` sections "What's captured", "What's NOT captured", "Converting crontabs to systemd-user timers" (one-page recipe).
3. No API/UI surface for this step.

### Verification

- `loginctl user-status <user>` → `Linger: yes`.
- `systemd-cgls jabali-user-<user>.slice` → after `ssh <user>@host sleep 60 &`, the sleep PID is listed under the user's slice.
- Runbook file exists.

### Exit criteria

- Shell/user-timer capture works; crontab capture documented as a known gap.
- Commit: `feat(agent): enable-linger for jabali users; runbook for shell/cron capture`.

---

## Step 8 — Admin UI: per-user slice status in versions/users pages

**Parallel:** yes, with step 7 after step 6. **Model tier:** default. **Agent:** `coder` (React/AntD). **Complexity:** MEDIUM.

### Context brief

Surface slice metrics in the admin UI. Not aiming for a full observability dashboard — just enough to debug "is user X's FPM running, how much memory is their slice using".

### Tasks

1. Agent command `user.slice.status {username}` returning `{active: bool, fpm_active: bool, memory_current_bytes: int, tasks_current: int, cpu_usage_seconds: float}`. Implementation: parse `systemctl show jabali-user-<user>.slice --property=MemoryCurrent,TasksCurrent,CPUUsageNSec` and the jabali-fpm@<user>.service status.
2. API: `GET /api/v1/admin/users/:id/slice-status` (admin-only) proxies to agent.
3. UI: add a "Slice" column (or a collapsible detail panel) to the admin Users list showing `Memory: 120 MB · Tasks: 34 · FPM: running`. Refresh on a 5s timer when the page is open; stop polling when closed.

### Verification

- UI shows non-zero memory for an active user; zero for an inactive one.
- Hitting 500 users' slice-status on the admin list page doesn't DOS the agent — throttle to one in-flight at a time or batch-query in a single agent call.

### Exit criteria

- Admins can eyeball per-user resource usage from the panel.
- Commit: `feat(ui): per-user slice status in admin Users list`.

---

## Step 9 — Closeout: docs, blueprint, memory, ADR-0023 cross-ref

**Parallel:** no (final). **Model tier:** default. **Agent:** `doc-updater`. **Complexity:** LOW.

### Context brief

Mark the work shipped; update authoritative docs; record learnings.

### Tasks

1. `docs/BLUEPRINT.md`: add new §4.11 "Per-user systemd slices" summarizing what ships (files, services, API, UI) with cross-refs to ADR-0025 and runbook.
2. `docs/adr/0023-m9-php-fpm-pool-manager.md`: add an "Amended 2026-04-18" note at top pointing at ADR-0025 for the runtime placement change.
3. `docs/runbooks/per-user-slices.md`: flesh out diagnosis scripts — `systemd-cgls`, `systemctl status jabali-fpm@<user>`, `systemctl show jabali-user-<user>.slice`.
4. Update memory files in `/home/shuki/.claude/projects/-home-shuki-projects-jabali2/memory/`:
   - `MEMORY.md`: add entry for per-user slices.
   - New `project_per_user_slices.md`.
5. No code changes.

### Verification

- `grep -q "ADR-0025" docs/BLUEPRINT.md`
- `grep -q "per-user-slices" docs/adr/README.md docs/BLUEPRINT.md`
- Memory MEMORY.md has an entry.

### Exit criteria

- Docs + memory reflect shipped state.
- Commit: `docs: per-user slices shipped — blueprint, runbook, memory`.

---

## Dependency graph

```
1 → 2 → 3 → 4 → 5 → 6 ┬→ 7 ┐
                      └→ 8 ┴→ 9
```

Steps 7 and 8 are file-disjoint (7: agent + docs; 8: API + UI) and can run in parallel via `isolation: "worktree"` per agent.

## Rollback

- Steps 1–5: fully additive. Reverting the commit cleanly removes features; existing global FPM stays running.
- Step 6: see §Rollback inside the step. Unmask + enable + start the global `php<v>-fpm.service`. Per-user units coexist safely.
- Steps 7–9: additive, no cutover. Revert commits freely.

## Anti-patterns explicitly forbidden

- **Do NOT** write to `/sys/fs/cgroup` directly. Always go through systemd unit files or `systemd-run`.
- **Do NOT** parse `systemctl` human-readable output in Go code — use `--property=NAME --value` output which is machine-stable.
- **Do NOT** use the same PID file path as the global FPM master. The per-user master uses `/run/php/jabali-fpm-<user>.pid`.
- **Do NOT** skip the smoke host. Step 6 cutover directly on `192.168.100.150` without a disposable host first is reckless.
- **Do NOT** embed `%i` expansion inside the Slice= directive on the template itself — systemd doesn't expand specifiers in `[Service]`/`[Slice]` directives the same way. Use per-instance drop-ins as specified.
- **Do NOT** auto-start `jabali-fpm@<user>.service` from the template's `[Install]` WantedBy — that would try to instantiate the template itself. Enable only concrete instances (`systemctl enable jabali-fpm@alice.service`).

## Success criteria for the whole plan

- Every user's FPM workers run under their own `jabali-user-<user>.slice`.
- Every user's login shell runs under the same slice.
- `systemctl mask php<v>-fpm.service` holds across reboot.
- Admin UI shows live per-user memory/tasks.
- No user-visible regression: bound vhosts serve PHP before, during (briefly), and after cutover.
- Docs + ADR-0025 are complete enough for a fresh agent to cold-resume any step.

## Review log

- 2026-04-18: plan drafted from `/blueprint real per-user systemd slices` invocation.
- 2026-04-18: Opus adversarial review — verdict **FIX-BEFORE-SHIP**. 3 CRITICAL + 4 HIGH + 3 MEDIUM + 4 LOW findings. CRITICAL + HIGH folded in:
  - F1 (Type=notify invalid) → step 2 task 3 now uses `Type=simple`, no PIDFile.
  - F2 (PartOf invalid on slices) → step 3 task 1: removed PartOf; nesting via `-` naming only.
  - F3 (cutover probe hit static) → step 6 now probes `jabali-healthcheck.php` which reconciler writes per domain.
  - F4 (drop-in syntax wrong) → step 3 clarified: `user@<uid>.service.d/jabali.conf` with `[Service] Slice=...`.
  - F5 (enable-linger cost) → added soft `MemoryHigh=80%` to `jabali-user.slice`; documented admin-tunable per-user caps.
  - F6 (race on cutover preconditions) → step 6 preflight enumerates all users, asserts slices active, supports `--dry-run`.
  - F7 (pool apply race) → step 4 adds flock on `/run/jabali/pool-apply-<user>.lock`.
  - L2, L4 folded. F8, F9, F10, L1, L3 noted for execution-phase attention (not plan-blocking).
