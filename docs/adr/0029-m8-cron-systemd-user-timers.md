# ADR-0029: M8 Cron via systemd-user timers with closed-set allowlist

**Date:** 2026-04-18
**Status:** Accepted
**Deciders:** Shuki

## Context

Hosting users need the ability to schedule recurring jobs (e.g., WordPress cron
events via `wp cron event run`, database maintenance, custom PHP scripts). Today,
Jabali provides no built-in cron mechanism. Manual workarounds (custom crontabs,
external schedulers) are fragile and unsupported.

The architecture already provides two critical building blocks:

1. **Per-user systemd-user managers** (ADR-0025): Each user's shell sessions,
   FPM pools, and other workloads run under `jabali-user-<user>.slice`. The
   panel can spawn systemd units inside those slices.

2. **Panel → Agent → Files → Reload pipeline** (ADR-0004, ADR-0009): The
   reconciler has proven this pattern with nginx vhosts — write a config file
   from the DB, signal systemd/nginx to reload, detect and correct drift.

The question is how to extend this pipeline to user cron jobs without
introducing arbitrary shell execution (a major attack surface), without
requiring separate cron infrastructure, and without breaking the per-user
isolation already established.

## Decision

We are implementing user-schedulable jobs via **systemd-user timers** under
per-user slices, with **closed-set allowlist** of commands and **shared
validator library** to prevent API/agent contract drift. The key design
decisions are:

---

## Design Decisions

### 1. systemd-user timers, not classic crontab

**Decision:** Jobs run via systemd timer units (not `/var/spool/cron/` crontabs).
Unit files live under `/etc/jabali-panel/cron-units/<user>/`, named
`jabali-cron-<job-id>.{service,timer}`. The agent writes them, links them into
the user's systemd session via `systemctl --user --machine=<user>@.host`, and
enables them. The reconciler regenerates on every pass to detect and correct drift.

**Rationale:**
- Reuses per-user slices from ADR-0025; no new systemd infrastructure required.
- Timers natively support cron expressions (via `OnCalendar=`) — no hand-rolled
  scheduler.
- Per-job logging via `journalctl --user-unit` comes free from journald.
- Drift detection matches the nginx vhost pipeline (ADR-0009) — reconciler diffs
  DB rows against `{unit files on disk}` and corrects divergence.
- Classic crontab would require the agent scanning `/var/spool/cron/` manually
  and maintain two parallel process-management stories (systemd + cron).

**Consequences:**
- Jobs are bound to users via per-user slice placement; no global system cron.
- `systemctl --user` invocation for a lingering user (ADR-0025 guarantee)
  is always available.

### 2. Closed-set allowlist, no arbitrary shell

**Decision:** Only two command families are allowed in v1:
- `wp <subcommand> --path=<abs-docroot> [args...]` — WordPress CLI, path must
  be a docroot owned by the user.
- `php <abs-docroot>/<path>.php [args...]` — Direct PHP invocation, resolved
  absolute path must be inside an owned docroot (reject `..` and symlinks via
  `filepath.EvalSymlinks` + prefix check).

Reject unescaped shell metacharacters: `& | ; $ \` ( ) < > \ newline NUL { } * ?`.
Command is parsed server-side with Go `shlex` (shell-aware tokenization) and
rendered as systemd `ExecStart=` with each token **single-quoted** (systemd
parses whitespace into argv; single quotes make tokens literal). Shell
re-interpretation at runtime is impossible because systemd executes argv
directly, not via `/bin/sh -c`.

**Rationale:**
- Eliminates arbitrary shell injection via command/schedule fields.
- Metachar rejection is a **parsing boundary defense**, not a quoting
  round-trip (which is fragile). Parsing happens on the panel; the validator
  returns a ready-to-execute argv slice.
- No `mysqldump`, `rsync`, `curl`, `bash`, or arbitrary paths — future
  "Backups" milestone will have its own allowlist design.

**Consequences:**
- Users cannot run custom scripts in v1 (strict but necessary for launch).
- `wp cron event run --due-now --path=/home/<user>/example.com/public_html`
  and `php /home/<user>/example.com/public_html/check.php` are the primary
  use cases; both are supported.

### 3. Shared validator library at `cronvalidate`

**Decision:** Both the API handler (pre-accept gate at `POST /api/v1/cron`)
and the agent command (defense-in-depth before unit render) import the same
`panel-api/internal/cronvalidate/` library. `ValidateCommand(cmd string,
ownedDocroots []string) (argv []string, err error)` is the contract enforced
on both sides.

**Rationale:**
- Today's `grant_level` bug (see feedback_cross_boundary_contracts.md) shows
  API and agent can drift on accepted values. A **shared validator library**
  prevents this pattern entirely — both sides parse the same way.
- The API calls the validator at Create/Update; agent re-validates before
  rendering as a defense-in-depth layer (TOCTOU protection in step 5).
- Validator returns parsed argv slice (not just error), so agent never
  re-parses — exact same tokens are used in both places.

**Consequences:**
- Agent and API have an explicit, testable contract on what commands are valid.
- Validator drift is impossible (both sides link the same binary).

### 4. Schedule is validated cron syntax

**Decision:** Schedule field accepts **5-field POSIX cron expressions** only
(minute, hour, dom, month, dow). Validation is two-tiered:

1. Pure-Go parser (robfig/cron v3) with options `Minute|Hour|Dom|Month|Dow`.
   Fast, deterministic, zero subprocess.
2. Supplementary `systemd-analyze calendar <expr>` call to sanity-check
   against systemd's parser (invoked with **argv array**, never `/bin/sh -c`).

Server stores the 5-field form for uniformity (no `@hourly` aliases in DB).
UI presets (`@hourly` → `0 * * * *`, etc.) map to canonical expressions on
submit.

**Consequences:**
- `*/5` syntax (5-minute intervals) is supported.
- Systemd parser quirks are caught early (e.g., leap-second edge cases).
- Validation never shells out with interpolated user input.

### 5. One-shot via `run-now` endpoint

**Decision:** `POST /api/v1/cron/:id/run-now` issues `systemctl --user start
jabali-cron-<id>.service` (one-time execution, not timer-based). Since the
service is `Type=oneshot`, systemd serializes concurrent runs — if the job is
already running, systemd queues (not starts a second copy).

**Consequences:**
- Users can trigger jobs immediately without waiting for the timer.
- Concurrency is handled by systemd (no double-run risk).

### 6. Logging via journald, user-accessible

**Decision:** Units do not redirect stdout/stderr; logs go to journald
per-user. The agent command `cron.tail_log` invokes `journalctl --user-unit
--machine=<user>@.host` to read logs. API endpoint `GET /api/v1/cron/:id/log`
returns the last N lines.

**Consequences:**
- Log retention is managed by journald's rotation policy (already configured).
- Users cannot tamper with logs (owned by systemd, not user).
- Logs are searchable and filterable via standard journalctl tools.

### 7. Drift detection via reconciler

**Decision:** Reconciler lists `/etc/jabali-panel/cron-units/<user>/` every
pass, diffs against DB rows (`cron_jobs`), and removes orphan unit files
(after disabling them via `systemctl --user disable --now`). One-way garbage
collection — orphans are cleaned, but DB rows are never inserted from disk.

**Consequences:**
- If a job is deleted while the panel is down, the unit file and timer are
  cleaned up on the next reconciler tick.
- Panel DB is immutable source of truth; filesystem is derivative.

### 8. User-scoped only (no domain_id)

**Decision:** Jobs have no `domain_id` column in v1. Scheduling is per-user,
not per-domain. WordPress wp-cron still works — users write `wp cron event
run --due-now --path=/home/<user>/example.com/public_html` as the command,
and the validator checks that path is owned by the user.

**Consequences:**
- Simpler data model (one ownership dimension: user_id).
- Admins cannot restrict jobs per-domain; all jobs are user-scoped.

### 9. Unit file template with TOCTOU + race mitigations

**Decision:** Service unit templates embed:
```ini
StartLimitIntervalSec=1
StartLimitBurst=1
ExecStartPre=/usr/local/libexec/jabali/cron-precheck <docroot>
ExecStart=<argv-tokens-single-quoted>
```

`cron-precheck` is a tiny setuid-nothing helper (installed by `install.sh`)
that re-validates docroot ownership **as the target user** at each run. This
closes the TOCTOU window: if a symlink is swapped between API Create
validation and service execution, the helper catches it (stat fails).

`StartLimitBurst=1` prevents run-now + timer-fire race (only one start per
1s window; systemd serializes within that).

**Rationale:**
- Re-validation happens in the security context of the user (not root),
  preventing privilege-escalation via symlink tricks.
- `ExecStartPre` failure aborts the job (no silent ignore).

**Consequences:**
- `cron-precheck` must be installed and kept up-to-date.
- Extra stat() call on each job invocation (~1 ms, negligible).

---

## Alternatives Considered

### Alternative 1: Classic `/var/spool/cron/` crontabs

**Option:** Use traditional crontab files for each user.

**Pros:**
- Familiar to sysadmins.
- No systemd dependency (if that mattered, which it doesn't — we're on systemd).

**Cons:**
- Two parallel process-management stories (systemd for FPM/slices, cron for jobs).
- No per-job resource accounting in cgroups.
- Reconciler must scan `/var/spool/cron/` manually for drift; no native systemd
  integration.
- No journalctl logging; must parse syslog or redirect stderr per-job.
- Validator logic is harder to share (crontab format is stricter than systemd
  timer format; would need two parsers).

**Decision:** Rejected. Systemd timers are simpler and reuse existing infra.

### Alternative 2: System-wide systemd timers (not per-user)

**Option:** All timers run under `system.slice`, with `User=<user>` directive
to change runtime user.

**Pros:**
- Simpler to manage (single systemd instance, not per-user).
- Avoids per-user systemd-user manager complexity.

**Cons:**
- Jobs do NOT land in the per-user cgroup hierarchy (ADR-0025); breaks resource
  isolation and accounting.
- Misses the whole point of per-user slices (separate cgroup boundaries).
- User cannot kill their own jobs with `systemctl --user` commands.
- Contradicts the architecture that all user workload (FPM, shells, now cron)
  belongs under per-user slices.

**Decision:** Rejected. Per-user timers are architecturally consistent with
ADR-0025.

### Alternative 3: Open allowlist with shell escaping

**Option:** Allow arbitrary commands but use robust shell quoting (e.g., Go
shlex round-trip escape) to prevent injection.

**Pros:**
- Maximum flexibility (users can run custom scripts, tools).

**Cons:**
- Shell escaping is notoriously fragile (CSV injection, locale-dependent
  `IFS`, quoting edge cases).
- Harder to reason about security when arbitrary tools can be invoked.
- No defense-in-depth (one escaping bug breaks everything).
- Doesn't match customer expectations (hosters rarely allow arbitrary shell).

**Decision:** Rejected. Closed allowlist + metachar rejection is simpler and
more defensible.

---

## Consequences

### Positive

- **Reuses per-user slice infrastructure:** No new cgroup machinery; jobs
  inherit resource limits from ADR-0025.
- **Free journald integration:** Logging, rotation, and audit trail come from
  existing systemd logging.
- **Reconciler shape matches vhosts:** Drift detection mirrors nginx vhost
  pipeline (ADR-0009). Same convergence guarantees.
- **Shell-injection hardened:** Closed allowlist + single-quoted ExecStart argv
  make shell escape impossible at runtime.
- **Validator contract enforced:** Shared library prevents API/agent drift;
  reduces attack surface from misconfigured cross-boundary contracts.

### Negative

- **No backup/maintenance tools in v1:** `mysqldump`, `rsync`, tar archives
  are deferred to a future "Backups" milestone. Users cannot schedule
  infrastructure jobs yet.
- **Validator drift is a concrete contract:** Both panel and agent must import
  the same cronvalidate library and stay in sync. New allowlist entries require
  coordinated changes on both sides.
- **systemctl --user --machine= quirks:** Not all systemd versions support
  `--machine=` syntax. Primary fallback is `sudo -u <user> XDG_RUNTIME_DIR=/run/user/<uid>
  systemctl --user …` (proven path; see plan §6).

### Risks

**TOCTOU (symlink swap between Create and exec):**
- **Mitigation:** ExecStartPre=/usr/local/libexec/jabali/cron-precheck validates
  docroot ownership as the target user at run time. Catches swapped symlinks.
- **Residual risk:** Low.

**Run-now + timer-fire race (double-start):**
- **Mitigation:** `StartLimitBurst=1` + `StartLimitIntervalSec=1` in service
  unit. systemd serializes starts within the 1s window; Type=oneshot ensures
  no parallel runs.
- **Residual risk:** Low.

**Validator misses a shell metachar:**
- **Mitigation:** Table-driven unit tests cover every injection class
  (traversal, unicode, empty, allowlisted happy paths). Fuzz target runs 10k+
  iterations. Acceptance criteria (step 3 exit) require both.
- **Residual risk:** Low.

**Schedule parses on panel but rejects on systemd:**
- **Mitigation:** Primary validation via pure-Go robfig/cron parser.
  Supplementary systemd-analyze call (using argv array, never `sh -c`) catches
  version-specific parser edge cases early.
- **Residual risk:** Very low.

**Validator library drift between panel and agent:**
- **Mitigation:** Both import the same Go package. If library changes, both must
  rebuild and redeploy together. CI/CD should catch accidental divergence
  (e.g., agent linked against stale library).
- **Residual risk:** Medium (operational, not security). Documented as a
  concrete cross-boundary contract requiring shared test fixtures.

---

## Related ADRs

- **ADR-0025** (Per-user systemd slices): PREREQUISITE. M8 is only viable because
  lingering per-user systemd-user managers are guaranteed. Jobs run under
  jabali-user-<user>.slice.
- **ADR-0004** (Reconciler-driven convergence): Drift detection pattern (DB →
  reconciler → files → systemd reload).
- **ADR-0009** (Nginx file-per-vhost): Similar reconciler pipeline; lessons
  applied to cron job generation and cleanup.

---

## Schema: `cron_jobs` table

```sql
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

---

## Implementation Checklist (from plan §8)

- [ ] ADR-0029 committed; lists ADR-0025 as prerequisite.
- [ ] Shared validator library `cronvalidate/cron.go` with ≥20 table-driven
  test cases and fuzz target (step 3 exit criterion).
- [ ] Agent commands `cron.apply`, `cron.remove`, `cron.run_now`, `cron.tail_log`
  re-validate before render; `cron-precheck` helper installed (step 4).
- [ ] Reconciler `ReconcileCronJobs` detects and removes orphan unit files
  (step 5).
- [ ] API CRUD endpoints + `run-now` + log-read working (step 5).
- [ ] UI with preset picker + advanced cron input + "Run now" button (step 6).
- [ ] E2E test: create wp-cron job → Run now → verify `last_run_at` populated
  (step 7).
- [ ] Runbook: timer troubleshooting, systemctl invocation, journalctl recipes
  (step 7).
- [ ] BLUEPRINT.md M8 row flipped to Shipped (step 7).

---

## Cross-References

- **`plans/m8-cron.md`** — Full 9-step implementation blueprint with risks,
  schema, unit templates, and exit criteria.
- **`plans/m8-cron-runbook.md`** — TBD: Operational guide post-shipment.
- **`docs/BLUEPRINT.md` §5.8** — M8 scope and dependencies.
