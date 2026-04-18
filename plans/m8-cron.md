# M8 — Cron (user-schedulable jobs via per-user systemd-user timers)

**Status:** Drafted → Revised post-adversarial-review (2026-04-18), ready for Wave A dispatch.
**Goal:** Hosting users can schedule allow-listed commands on a cron-expression schedule. Jobs run under the user's own systemd-user manager inside the per-user slice (ADR-0025). UI has a preset picker plus an advanced cron-expression input. No arbitrary shell, no domain-scoped jobs, no backup tooling.

---

## 0. Key design decisions

1. **systemd-user timers, not classic crontab.** We already shipped per-user slices (ADR-0025). Timers fit the DB→generator→files→reload pipeline naturally (same shape as nginx vhosts), give us resource accounting via the existing slice, and expose per-job logs through `journalctl --user-unit`. Classic crontab would keep two parallel process-management stories and leave the reconciler scanning `/var/spool/cron/` for drift. Not worth the coupling debt.

2. **Unit files live under `/etc/jabali-panel/cron-units/<user>/`.** Named `jabali-cron-<job-id>.{service,timer}`. The agent writes them, then `systemctl --user --machine=<user>@.host link <path>` + `enable --now`. The panel DB is truth; these files are derivative. Reconciler regenerates on every pass.

3. **Shared validator lib at `panel-api/internal/cronvalidate/`.** Both the API handler (pre-accept gate) and the agent command (defense-in-depth before render) import the same `Validate(cmd, ownedDocroots []string) error`. This closes today's pattern-fire where API and agent drift on accepted values (see `feedback_cross_boundary_contracts.md` — today's grant_level bug was exactly this shape).

4. **Allowlist is closed-set in v1.**
   - `wp <subcommand> --path=<abs-docroot> [args...]` where `<abs-docroot>` MUST be a docroot owned by the user.
   - `php <abs-docroot>/<path>.php [args...]` where the resolved absolute path MUST be inside an owned docroot (reject `..` and symlinks via `filepath.EvalSymlinks` + prefix check).
   - **Defense = metachar rejection, not quoting round-trip.** Command is parsed server-side with Go shlex (shell-aware tokenization). Reject any unescaped shell metacharacters `& | ; $ \` ( ) < > \ newline NUL { } * ?`. Unit file renders `ExecStart=` as **argv array** (systemd parses a single exec line into argv via whitespace; we emit each token single-quoted), NOT via `/bin/sh -c`. Shell re-interpretation is impossible at runtime.
   - **No** `mysqldump`, `rsync`, `curl`, `bash`, arbitrary paths. Backups are a future milestone.

5. **Schedule is validated cron syntax only.** 5-field POSIX cron (minute, hour, dom, month, dow). Presets in UI map to canonical expressions (`@hourly` → `0 * * * *`, etc.) but server stores the 5-field form for uniformity. systemd's `OnCalendar=` supports cron syntax natively via `systemd-analyze calendar`. **Validator calls `exec.Command("systemd-analyze", "calendar", userInputExpr)` — argv array only, NEVER shell interpolation / `sh -c`.** Primary validation uses a pure-Go cron parser (robfig/cron v3 with Parser option `Minute|Hour|Dom|Month|Dow`); the systemd-analyze call is supplementary sanity check.

6. **One-shot via `run-now`.** `POST /api/v1/cron/:id/run-now` issues `systemctl --user --machine=<user>@.host start jabali-cron-<id>.service`. Does NOT touch the timer; if the job is mid-run, systemd serializes (service is `Type=oneshot` + default single-instance).

7. **Logging goes to journald per-user.** Units don't redirect stdout/stderr; `journalctl --user-unit=jabali-cron-<id>.service` works for admin, and for users we'll surface a "Last output" read-back via a dedicated agent command `cron.tail_log` (reads via `journalctl -u jabali-cron-<id>.service --user --machine=<user>@.host -n 50 -o cat`). This keeps unit files small and uses journal rotation policy we already have.

8. **Drift detection.** Reconciler lists `/etc/jabali-panel/cron-units/<user>/` every pass, diffs against DB rows, and removes orphan unit files (+ `systemctl --user disable --now` them first). Catches the case where a job was deleted while the panel was down.

9. **User-scoped only in v1.** No `domain_id` column. WordPress wp-cron still works — users just write `wp cron event run --due-now --path=/home/<user>/example.com/public_html` as the command, and the validator checks that path is owned.

---

## 1. Steps / waves

| Step | Wave | Parallel? | Summary | Outputs |
|------|------|-----------|---------|---------|
| 1 — ADR-0029 | A | w/ 2 | Record: systemd-user timers under per-user slice; closed-set allowlist; shared validator lib; drift-detecting reconciler. | `docs/adr/0029-m8-cron-systemd-user-timers.md` |
| 2 — Migration + model + repo | A | w/ 1 | `cron_jobs` table (see §4); GORM model; CRUD repo with `ListByUserID`, `FindByID`, `UpdateStatus(lastRunAt, exitCode, lastError)`. | `panel-api/internal/db/migrations/000037_*.sql`, `models/cron_job.go`, `repository/cron_job_repository.go` |
| 3 — Shared validator | B | — | `panel-api/internal/cronvalidate/cron.go`: `ValidateCommand(cmd string, ownedDocroots []string) (argv []string, err error)` + `ValidateSchedule(expr string) error`. Returns **parsed argv slice** so agent can feed directly to unit renderer (no re-parse). Unit-tested with table-driven cases covering every rejection class + fuzz target. | `internal/cronvalidate/cron.go`, `cron_test.go`, `cron_fuzz_test.go` |
| 4 — Agent commands | C | — | `cron.apply` (render + link + enable), `cron.remove` (disable + stop + rm files), `cron.run_now` (one-shot start), `cron.tail_log` (journalctl read-back). Systemd unit templates embedded as Go string constants. Agent imports `cronvalidate` and re-validates `(command, ownedDocroots)` before render as defense-in-depth. | `panel-agent/internal/commands/cron_*.go`, `panel-agent/internal/commands/registry.go` |
| 5 — API + reconciler | D | — | `/api/v1/cron` CRUD + `POST /:id/run-now` + `GET /:id/log`. Reconciler: `ReconcileCronJobs(ctx)` converges DB → units, drift-detects orphan unit files. Wire into main `ReconcileAll` loop after `ReconcilePHPPools`. | `panel-api/internal/api/cron.go`, `cron_test.go`, `reconciler/cron_reconcile.go` |
| 6 — UI | E | — | AntD page in user shell: list table (name, schedule, last_run, last_exit, enabled toggle), create modal with preset radio + advanced cron input, "Run now" button, "View log" drawer. | `panel-ui/src/shells/user/cron/*.tsx` + refine resource |
| 7 — E2E + runbook + blueprint flip | F | — | Playwright: create wp-cron job with `@hourly` → hit Run now → poll until `last_run_at` populates → verify exit code 0. Runbook: timer troubleshooting, systemctl invocation cheat sheet, journalctl recipes. | `tests/e2e/cron.spec.ts`, `plans/m8-cron-runbook.md`, `docs/BLUEPRINT.md` M8 flip |

**Dependency graph (revised after review — Steps 3 and 4 are NO LONGER parallel):**
- Wave A: 1 ∥ 2 (independent; ADR is docs, migration is SQL)
- Wave B: 3 alone (validator contract must ship first — agent in step 4 imports it)
- Wave C: 4 alone (agent commands; imports `cronvalidate` from step 3)
- Wave D: 5 (depends on 2, 3, 4 — wires everything)
- Wave E: 6 (depends on 5 for API shape)
- Wave F: 7 (depends on 6)

**Model tiers:** Step 1 → strongest (architectural). Steps 2-7 → default.

---

## 2. Out of scope (v1)

- **Backups** (`mysqldump`, `rsync`, tar archives) — future "Backups" milestone, separate allowlist design.
- **Arbitrary shell / bash scripts** — hard no, doesn't go through allowlist.
- **Domain-scoped cron** — v1 is user-scoped. Wp-cron works because the PATH arg lives under a user-owned docroot.
- **Email-on-failure** — depends on M6. For v1 the `last_error` column + UI badge is the signal.
- **Cron expression step syntax** (`*/5`) — validated via standard cron parser; accepted.
- **Random-time presets** (`@daily-random`) — not in v1.
- **Job history log beyond last run** — only `last_run_at/last_exit_code/last_error` on the row. Journal has full history via `journalctl --user-unit`.

---

## 3. Invariants (must hold on every step)

- `cron_jobs.user_id` FK is `ON DELETE CASCADE` (deleting a user wipes their cron state cleanly).
- Command + schedule BOTH re-validated server-side at `Create`, `Update`, and at reconciler render time. **Never trust the DB value** — if someone edits the row out-of-band, the reconciler refuses to render and logs.
- Agent `cron.apply` is idempotent: re-apply with same args is a no-op (diff unit file content before writing).
- Agent touches user timers ONLY via `sudo -u <user> XDG_RUNTIME_DIR=/run/user/<uid> systemctl --user …` (primary) or `systemctl --user --machine=<user>@.host …` (optional, probed in step 4). No `su -`, no raw `loginctl` calls, no bare `systemctl`. This keeps us inside the per-user manager that ADR-0025 provisioned.
- Reconciler treats the set `{unit files on disk} \ {DB rows}` as orphans and disables them. One-way garbage collection — never re-inserts DB rows from disk.
- User MUST have `loginctl enable-linger` set (ADR-0025 guarantees this). If `linger` is off for a user, cron.apply returns an explicit `user_not_lingering` error with an admin-visible recovery hint.
- The `run-now` endpoint MUST only accept jobs owned by the caller (ownership check identical to M10/M11/M12).

---

## 4. Schema — `cron_jobs`

```sql
-- migrations/000037_create_cron_jobs.up.sql
CREATE TABLE cron_jobs (
  id              CHAR(26)      NOT NULL PRIMARY KEY,
  user_id         CHAR(26)      NOT NULL,
  name            VARCHAR(100)  NOT NULL,
  command         VARCHAR(1024) NOT NULL,
  schedule        VARCHAR(100)  NOT NULL,            -- 5-field cron expr
  enabled         TINYINT(1)    NOT NULL DEFAULT 1,
  last_run_at     TIMESTAMP     NULL,
  last_exit_code  INT           NULL,
  last_error      VARCHAR(1024) NULL,
  created_at      TIMESTAMP     NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at      TIMESTAMP     NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  KEY idx_cron_jobs_user_id (user_id),
  CONSTRAINT fk_cron_jobs_user_id FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
```

`name` is user-visible and needn't be unique across users (only within a user if we want; v1 says no unique constraint to reduce friction).

---

## 5. Unit file templates (agent-rendered)

```ini
# /etc/jabali-panel/cron-units/<user>/jabali-cron-<id>.service
[Unit]
Description=Jabali cron job <id> (<name>)
After=default.target
# Prevent run-now + timer-fire races (see §9 risk):
StartLimitIntervalSec=1
StartLimitBurst=1

[Service]
Type=oneshot
RemainAfterExit=no
Environment=PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin
WorkingDirectory=%h
# ExecStartPre re-validates docroot ownership AS the target user (TOCTOU defense).
# If symlink was swapped between API-Create and now, stat as user catches it.
ExecStartPre=/usr/local/libexec/jabali/cron-precheck <RENDERED-DOCROOT>
ExecStart=<RENDERED-ARGV>
# No ExecStopPost needed; reconciler polls systemctl show on next pass.
```

`<RENDERED-ARGV>` is the validator's argv slice re-emitted with each token single-quoted (systemd parses whitespace → argv; single-quote wrap is literal). `/usr/local/libexec/jabali/cron-precheck` is a tiny setuid-nothing helper installed by `install.sh` that does `stat()` on the docroot path and exits non-zero if the real inode is outside user's home tree. This closes the validate-at-Create → render-later TOCTOU window flagged in review.

```ini
# /etc/jabali-panel/cron-units/<user>/jabali-cron-<id>.timer
[Unit]
Description=Jabali cron timer for <id>

[Timer]
OnCalendar=<5-field cron expr passthrough to systemd>
Persistent=true
Unit=jabali-cron-<id>.service

[Install]
WantedBy=timers.target
```

`<RENDERED-COMMAND>` is ArgvQuote-escaped (each token wrapped via `strconv.Quote`-style but using single quotes) before ExecStart substitution. `Persistent=true` means missed ticks replay on next boot — desirable for wp-cron.

---

## 6. Agent → systemd wiring per-user

The agent runs as root. **Primary invocation (pinned):** `sudo -u <user> XDG_RUNTIME_DIR=/run/user/<uid> systemctl --user …`. This always works when the user is lingering (ADR-0025 guarantee) and does NOT depend on machined / systemd-on-dbus compat quirks.

```
AGENT_EXEC=(sudo -u <user> XDG_RUNTIME_DIR=/run/user/$(id -u <user>))

${AGENT_EXEC} systemctl --user daemon-reload
${AGENT_EXEC} systemctl --user enable --now jabali-cron-<id>.timer
${AGENT_EXEC} systemctl --user disable --now jabali-cron-<id>.timer
${AGENT_EXEC} systemctl --user start jabali-cron-<id>.service           # run-now
${AGENT_EXEC} journalctl --user -u jabali-cron-<id>.service -n 100 -o cat
${AGENT_EXEC} systemctl --user show -p ExecMainStatus -p InactiveExitTimestamp jabali-cron-<id>.service
```

**Optional modern form** (`systemctl --user --machine=<user>@.host …`) is tested in step 4 and used ONLY if the primary form fails; it's a nice-to-have, not a dependency. Step 4 must commit the probe results in the runbook.

**cron.remove semantics** (review-mandated explicit handling):
1. Attempt `systemctl --user disable --now jabali-cron-<id>.timer`.
2. If command succeeds OR fails with "unit not loaded" → `rm -f` the `.service` and `.timer` files.
3. If it fails with "failed to connect to bus" (user manager dead / linger off) → return `user_manager_unreachable` error to panel; do NOT rm. Reconciler GC will clean orphan files on a later pass once user manager is back.

---

## 7. UI shape (for Step 6)

- `/jabali-panel/cron` → `CronList` page.
- Table columns: Name, Command (truncated, tooltip full), Schedule (humanized — "every hour", "3:00 AM daily"), Last run, Last exit, Enabled (Switch), Actions.
- Actions: Edit, Run now, Delete.
- Create modal:
  - Name
  - Command: textarea, monospace, with live validator hint ("Must start with `wp ` or `php `...")
  - Schedule: radio group `{Hourly, Daily at 3 AM, Weekly on Sunday 3 AM, Monthly, Advanced}`; advanced reveals a plain text input + cron expression doc link.
  - Submit returns 400 with `error.detail` on validator failure; UI shows inline under the offending field.
- "View log" drawer per row: fetches `GET /api/v1/cron/:id/log`, renders in `<pre>` with copy button.

---

## 8. Exit criteria per step

- **Step 1**: ADR committed; sections for Context/Decision/Alternatives/Consequences present; lists ADR-0025 as predecessor.
- **Step 2**: `go test ./panel-api/internal/repository/... -run CronJob` green; migration applied + rolled back cleanly on dev.
- **Step 3**: `go test ./panel-api/internal/cronvalidate/... -count=1` green; ≥20 table rows covering injection, traversal, unicode, empty, allowlisted happy paths; `FuzzValidateCommand` target compiles and runs ≥10k iterations without crash. `ValidateCommand` returns parsed argv slice (not just error).
- **Step 4**: Agent unit test of command dispatch; manual smoke on dev host creating/removing a test job for user `shuki` (proves `sudo -u <user> … systemctl --user` primary path works); `cron-precheck` helper installed by `install.sh` and refuses out-of-tree paths.
- **Step 5**: API integration tests green; reconciler test with mocked agent asserts orphan file deletion path fires.
- **Step 6**: `npm run build` green; manual click-through in dev session.
- **Step 7**: Playwright E2E green in CI (uses `page.waitForFunction(..., { timeout: 360_000 })` to allow ≥5 min for `last_run_at` population); runbook committed; BLUEPRINT changelog row added; memory index entry `project_m8_cron.md` written.

---

## 9. Risks + mitigations

| Risk | Mitigation |
|---|---|
| `systemctl --machine=` syntax fails on our systemd version | Step 4 probes first on dev. Primary invocation is already `sudo -u <user> XDG_RUNTIME_DIR=/run/user/<uid> systemctl --user …` (see §6) — machine= is additive, not a dependency. |
| Orphan unit files accumulate if reconciler skips | Unit test on reconciler asserting orphan deletion; runbook includes manual recovery procedure. |
| Validator misses a new shell metachar | Table-driven tests seed every known-bad class; fuzz test with `go test -run FuzzValidate` covers 10k random inputs. Step 3 exit requires the fuzz target exists. |
| Schedule parses on panel but rejects on systemd | Primary validation via pure-Go cron parser; supplementary `systemd-analyze calendar` subprocess call uses **argv array** (never `sh -c`). |
| `last_run_at` staleness (max 30s behind reality) | Document as known limitation in runbook. E2E tests wait ≥35s after run-now before asserting `last_exit_code`. UI badge shows "last run (~30s ago)" semantics. |
| **TOCTOU — symlink swap between Create validation and service exec** | `ExecStartPre=/usr/local/libexec/jabali/cron-precheck` re-validates docroot ownership AS the target user at each run. Install this helper in step 4 alongside agent. |
| **Race — run-now start while timer fires** | `StartLimitBurst=1` + `StartLimitIntervalSec=1` in service unit prevents double-start within a 1s window. `Type=oneshot` serializes within that window. |
| Cron job fires while panel is down, panel never updates `last_run_at` | Reconciler polls `systemctl --user show -p ExecMainStatus -p InactiveExitTimestamp` on every pass (30s cadence) — eventual consistency. No cross-process callback needed in v1. |
| Log-view API performance with many jobs per user | UI fetches log **only for the selected job** (one journalctl call per drawer open), never bulk-fetches. Pagination via `-n 50` default, `?lines=N` override up to 500. |

---

## 10. Post-merge verification checklist

- [ ] `/api/v1/cron` returns 200 with empty list for a fresh user.
- [ ] Create a wp-cron job for an existing install; wait 5 min; `last_run_at` is populated; `journalctl --user-unit=jabali-cron-<id>` shows the wp-cron run.
- [ ] Delete the job; `/etc/jabali-panel/cron-units/<user>/` no longer contains it; `systemctl --user --machine=<user>@.host list-timers` doesn't show it.
- [ ] Manually drop a bogus `.timer` file in the cron-units dir; trigger reconcile; file is removed + `list-timers` reflects removal.
- [ ] Create a job with `cat /etc/passwd` as command; API returns 400 `command_not_allowed`.
- [ ] Create a job with `php ../../etc/passwd` as command; API returns 400 `command_not_allowed`.
- [ ] Run-now on someone else's job returns 403.
- [ ] ADR-0029 index entry present in `docs/adr/README.md`.
- [ ] BLUEPRINT M8 row flipped to Shipped with commit anchor.
