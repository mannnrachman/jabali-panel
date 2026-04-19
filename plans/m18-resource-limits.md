# M18 — Per-user resource limits (disk quota + cgroups v2 + nginx rate limits)

**Status:** Reviewed + revised (2026-04-19). 5 CRITICAL + 4 HIGH findings folded. Ready for Wave A dispatch.
**Goal:** Hosting packages become *enforceable* bundles. Admins set disk, CPU, memory, I/O, task, and per-domain request/connection limits on a package; the reconciler converges the system so every hosting user is capped by kernel + fs + nginx. Per-user override rows allow one-off tuning without forking packages.

---

## 0. Key design decisions

1. **POSIX user quota for disk, cgroups v2 for everything else, nginx modules for per-domain HTTP limits.** Three separate enforcement surfaces because they live at three different kernel/userspace layers. Trying to unify them (e.g. per-user nginx via lua, or disk cgroup io accounting) leaves gaps. POSIX user quota is the hosting-industry default (cPanel/DirectAdmin/Hestia) and maps 1:1 to our existing per-user Linux UIDs. Cgroups v2 is already wired via per-user slices (ADR-0025) — we only add drop-ins with limit directives; accounting (`CPUAccounting=yes` etc.) is already on. Nginx `limit_req_zone` / `limit_conn_zone` are the battle-tested HTTP-layer primitive; anything per-domain beyond that is out of v1.

2. **`usrquota` on `/home` at install time — but only for ext4/xfs; btrfs/zfs/tmpfs fail loud.** On fresh hosts, install.sh detects the filesystem type via `stat -fc %T /home`, then branches:
   - **ext4/ext3:** add `usrquota,grpquota` to fstab + `quotacheck -cugm` + `quotaon -v`.
   - **xfs:** add `usrquota` to fstab + `xfs_quota -x -c 'enable -u' /home` (xfs does NOT auto-enable accounting from the mount flag; this was CRITICAL finding #1 from review).
   - **btrfs/zfs:** fail loud — "not supported in v1, migrate to ext4/xfs or deploy on a dedicated /home mount." Their quota models are subvolume/dataset-based and don't map to `setquota -u`.
   - **tmpfs:** fail loud immediately.
   On existing hosts, install.sh detects the missing mount option and **prompts** the operator (or fails loud in non-interactive mode). NEVER remounts automatically — busy mounts blow up mid-remount and we'd wedge FPM pools. ADR documents the support matrix and upgrade path.

2a. **`setquota` uses the explicit mount path, never `-a`.** Using `-a` on multi-mount hosts could hit the wrong filesystem (e.g. `/var` has quota enabled, `/home` doesn't — `-a` would set the quota on `/var`). The agent resolves `/home`'s mount point via `/proc/mounts` at startup, caches it, and passes it explicitly: `setquota -u <user> <blocks> <blocks> 0 0 <home_mount_path>`. (Review finding #2.)

3. **Package fields and a dedicated `user_limit_overrides` table.** Packages get the full set (disk/cpu/memory/io-read/io-write/tasks + domain-scoped rate_limit_rps, connection_limit). Per-user overrides go in a dedicated nullable-per-field table, NOT a JSON blob on `users`. Reason: auditability (admin history needs `updated_at`/`updated_by`), indexability (usage dashboards LEFT JOIN on it), and one-shot reversion (`DELETE FROM user_limit_overrides WHERE user_id = ?`). A JSON blob on users would tangle overrides with user identity state we already reconcile elsewhere.

4. **Effective-limits resolution is a pure function.** `resolveEffective(pkg, override) → EffectiveLimits` lives in `panel-api/internal/limits/resolve.go` and is shared between API (for display), reconciler (for convergence), and CLI. No field is ever computed in more than one place. Unit-tested with a table of every combination including nulls.

5. **Agent command surface is three verbs: apply / report / clear.**
   - `user.limits.apply` — atomically writes `/etc/systemd/system/jabali-user-<u>.slice.d/limits.conf` + calls `setquota -u <user> ... <home_mount>` (explicit mount path) + `systemctl daemon-reload`. **daemon-reload behavior, corrected:** cgroup v2 kernel properties (`memory.max`, `cpu.max`, `pids.max`, `io.max`) ARE updated live — a new allocation beyond `MemoryMax` will trigger OOM immediately. BUT memory already allocated to existing processes is NOT retroactively reclaimed. If tightening limits on a long-running PHP-FPM worker, the worker keeps its already-held pages until it exits or allocates fresh. Operators who need a hard cutover restart the slice: `systemctl restart jabali-user-<u>.slice`. (Review finding #4.)
   - `user.limits.report` — reads `systemctl show -p MemoryCurrent,CPUUsageNSec,TasksCurrent,IOReadBytes,IOWriteBytes jabali-user-<u>.slice` + `quota -u <u>` + merges into a single response.
   - `user.limits.clear` — removes the drop-in, sets quota to 0 (= unlimited), daemon-reload. Called on package removal / user delete.
   No separate `cpu.set`, `mem.set`, etc. — limits travel as a bundle to avoid partial-apply ordering bugs.

6. **Nginx per-domain rate limits live in a single included fragment per domain.** Reconciler emits `/etc/nginx/conf.d/jabali-ratelimits.conf` with all `limit_req_zone` + `limit_conn_zone` declarations (must be in http{}, not server{}), and per-vhost `limit_req` + `limit_conn` directives inside the existing vhost templates. Single file keeps zone-name uniqueness checkable; per-vhost references keep the wiring obvious. Zone names = `rl_<domain_id>` / `cn_<domain_id>` — ULID-safe, domain-rename-safe.

7. **CLI named `jabali limits`, not `jabali cgroup`.** Disk quota is POSIX, not a cgroup; nginx limits are neither. Single namespace covers all three layers. Subcommands: `jabali limits check` (cgroups v2 probe + quota-mount probe + nginx module probe), `jabali limits apply <user>` (idempotent), `jabali limits status <user>` (human-formatted `user.limits.report`). `jabali limits package apply <package_id>` (bulk across all users with the package). `jabali limits apply --all` is intentionally NOT offered — operators use the reconciler or loop the CLI.

8. **Reconciler convergence cadence — plus inline apply on user create.** `ReconcileUserLimits(ctx)` runs every pass after `ReconcileUsers` for drift detection. BUT on user creation, `user.limits.apply` is called **inline** during `user.ensure` (same pass, same orchestration step) so a new user never exists on the host without its cgroup/quota state. This closes the 1-pass race window flagged by review finding #3. `ReconcileUsers` already serializes user provisioning; inlining the limits call keeps the existing invariants. Drift detection via subsequent passes = `user.limits.report` for every user, compare to DB-effective, re-apply if mismatch. Cost at 10k users: one `systemctl show` per user, batchable via `--all`-style invocations — but we're not at that scale.

9. **MemoryHigh is pinned at 90% of MemoryMax.** Admins don't see `memory_high_mb` — it's derived. Rationale: the soft/hard gap is a tuning knob nobody except us should touch, and pinning it at 10% headroom matches every reputable systemd guide. If a customer asks for a specific split we cut a feature request, not a schema migration.

10. **Zero disk quota = unlimited**, mirroring how `hosting_packages.disk_quota_mb = 0` already works today. Same convention applies to every new field (cpu/memory/io/tasks) — `0` means no cap. This preserves backward compat: existing packages keep working without a data migration.

11. **Override semantics: NULL = inherit from package, 0 = unlimited, N = cap at N.** Each column in `user_limit_overrides` is nullable. NULL means "fall through to the package value." `0` means "override to unlimited for this user." `N>0` means "cap at N regardless of package." Resolver is a pure function, exhaustively table-tested for every combination. (Review finding #8.)

12. **Decrease-confirmation deferred to v2.** The risk of "admin shrinks a package and kills 500 users' processes" is real but implementing a workflow (queue, notification, admin confirm) is a feature of its own. v1 ships without it. Admins must test package changes on a non-production user first; runbook documents the procedure. A `--dry-run` flag on `jabali limits package apply` shows effective changes without writing anything. (Review finding #5.)

13. **`/tmp` isolation via tmpfs, not quota.** install.sh configures `/tmp` as a `tmpfs` mount with a size cap (default 1 GB, configurable). Users share this pool but no single user can exhaust disk via `/tmp` files — the tmpfs itself is capped. This is standard practice on multi-tenant hosts and sidesteps the "per-user tmpquota" rabbit hole. Existing hosts with `/tmp` on `/` get a runbook entry to migrate. (Review finding #7.)

---

## 1. Steps / waves

| Step | Wave | Parallel? | Summary | Outputs |
|------|------|-----------|---------|---------|
| 1 — ADR-0032 | A | w/ 2 | Record the three-surface enforcement model, usrquota remount policy (prompt-don't-auto), override table design, MemoryHigh=90% convention, `0 = unlimited` semantics. | `docs/adr/0032-m18-resource-limits.md` |
| 2 — Migration + models + repo | A | w/ 1 | `hosting_packages` gains `cpu_quota_percent`, `memory_limit_mb`, `io_read_mbps`, `io_write_mbps`, `max_tasks`. `domains` gains `rate_limit_rps`, `connection_limit`. New `user_limit_overrides` table. GORM models + nullable-field repo. | `panel-api/internal/db/migrations/000042_*.sql`, models/repo updates |
| 3 — Effective-limits resolver + FS detector + mount resolver | B | w/ 4 | `internal/limits/resolve.go` with `Resolve(pkg, override) EffectiveLimits` (pure, exhaustive table tests — explicit cases for NULL vs 0 vs positive on every field). `internal/limits/fsdetect.go` probes `stat -fc %T /home` and returns `FSExt4 / FSXfs / FSBtrfs / FSZfs / FSTmpfs / FSOther`. `internal/limits/quotamount.go` walks `/proc/mounts` to find the mount point containing `/home` (returns the explicit path for `setquota <mount>`). | `internal/limits/*.go`, `*_test.go` |
| 4 — install.sh: quota + tmpfs /tmp + probe | B | w/ 3 | Add `quota` + `quota-tools` + `xfsprogs` to base pkg list. `configure_disk_quota`: detect FS type, branch (ext4: `usrquota` + `quotacheck` + `quotaon`; xfs: `usrquota` + `xfs_quota -x -c 'enable -u' /home`; btrfs/zfs/tmpfs: fail loud with upgrade-path message). If `/home` shares a mount with `/` on an existing host, prompt (interactive) or fail loud (unattended). Also: `configure_tmp_tmpfs` — add tmpfs entry for `/tmp` with `size=1G,nosuid,nodev,noexec` to /etc/fstab if not already tmpfs (idempotent). Verify cgroups v2 unified hierarchy (`stat -fc %T /sys/fs/cgroup` = `cgroup2fs`). | `install.sh` diff |
| 5 — Agent commands | C | — | `user.limits.apply|report|clear` (handlers + unit tests that write drop-ins to a temp systemd root under `JABALI_SYSTEMD_ROOT`, same pattern as `user_slice_ensure`). Drop-in renderer emits only non-zero directives — zero means unlimited, no directive emitted. Agent-side bounds validation (defense-in-depth after API layer): reject cpu_quota_percent > 10000, memory_limit_mb > 1048576, max_tasks > 100000, io_*_mbps > 10000. After `daemon-reload`, verify in-kernel state by reading `/sys/fs/cgroup/jabali-user-<u>.slice/memory.max` etc. and comparing to expected — catches the "drop-in matches but daemon-reload silently failed" edge case. | `panel-agent/internal/commands/user_limits_*.go` |
| 6 — Nginx rate-limit fragment generator | C | — | Reconciler code that walks all domains with rate_limit_rps or connection_limit set, renders `/etc/nginx/conf.d/00-jabali-ratelimits.conf` (00- prefix ensures it loads before vhost files — review finding #11) with `limit_req_zone` + `limit_conn_zone` declarations, and injects `limit_req zone=rl_<id> burst=<rps*2> nodelay;` + `limit_conn cn_<id> <n>;` into the vhost template. `nginx -t` gate before reload, rollback on failure. Zone size `10m` per zone (default ~218k entries) — documented formula in runbook; zone utilization exposed via `jabali limits check` for tuning. | `panel-agent/internal/commands/nginx_*.go` updates, vhost template diff |
| 7 — Panel-API endpoints | D | — | `PUT /api/v1/packages/:id` accepts new fields. `GET /api/v1/users/:id/usage` returns agent report. `PUT /api/v1/users/:id/limit-overrides`, `DELETE` same (clear). Package writer's existing validation extended; negative values rejected, caps enforced (e.g. cpu_quota_percent <= 10000 = 100 cores). | `panel-api/internal/api/{packages,users}.go` |
| 8 — Reconciler wiring | D | — | `ReconcileUserLimits(ctx)` runs after `ReconcileUsers`. `ReconcileNginxRateLimits(ctx)` runs as part of nginx reconcile pass (step 6 wires the file generator; step 8 wires the invocation into the pipeline and ensures drift detection). | `panel-api/internal/reconciler/*.go` updates |
| 9 — CLI | E | — | New Cobra verbs: `jabali limits check`, `jabali limits apply <user>`, `jabali limits status <user>`, `jabali limits package apply <package_id>`. Each calls the existing agent UDS client; `check` is a pure probe that doesn't need the agent (probes kernel config + mounts + nginx modules locally). | `panel-api/cmd/server/limits_cmd.go` |
| 10 — Admin UI | F | w/ 11 | Package editor: new fields grouped under "Resource limits" + "Per-domain HTTP limits" sections with tooltips mapping to systemd directives. Per-user override page at `/users/:id/limits` with the same fields but "Inherit from package" default. User list gets a new "Disk usage" column (current / limit, colored bar). | `panel-ui/src/shells/admin/packages/*`, `admin/users/*` |
| 11 — User shell usage widget | F | w/ 10 | Widget on user dashboard: disk used / limit, RAM used / limit, CPU% (rolling 60s average from `CPUUsageNSec` delta). Refresh on 10s polling — no websocket yet. | `panel-ui/src/shells/user/dashboard/UsageCard.tsx` |
| 12 — E2E + runbook + blueprint flip | G | — | Playwright: create package with disk 100MB + memory 256MB; assign to a test user; fill `/home/testuser/` past 100MB → verify `write(EDQUOT)` from a shell step; allocate 300MB memory in PHP → verify OOM kill. Runbook: quotaon/quotaoff troubleshooting, systemd-cgtop cheat sheet, how to lift usrquota on an existing host, nginx 503-from-limit-req vs from-backend disambiguation. | `tests/e2e/limits.spec.ts`, `plans/m18-resource-limits-runbook.md`, `docs/BLUEPRINT.md` M18 flip |

**Dependency graph:**
- Wave A: 1 ∥ 2 (ADR is docs, migration is SQL)
- Wave B: 3 ∥ 4 (resolver + install.sh are independent; step 5 imports step 3)
- Wave C: 5 ∥ 6 (agent user-limits + agent nginx-ratelimits — different files, different kernel surfaces)
- Wave D: 7 ∥ 8 (API uses resolver from step 3; reconciler invokes agent commands from steps 5/6)
- Wave E: 9 alone (CLI — small, independent)
- Wave F: 10 ∥ 11 (admin + user UI — disjoint files)
- Wave G: 12 alone (E2E validates everything end-to-end)

**Model tiers:** Step 1 (ADR) → strongest. Step 3 (resolver semantics) → strongest. Steps 4, 6 (system boundaries: fstab + nginx) → strongest. Everything else → default.

---

## 2. Out of scope (v1)

- **Remounting `/home` automatically on existing hosts.** Operator-initiated only. ADR-documented.
- **Per-domain bandwidth (bytes/sec) caps.** `limit_rate` + `limit_rate_after` are nginx primitives; we can ship them later. v1 is requests + connections.
- **Per-domain storage caps.** Disk quota is per-user (POSIX `setquota -u`). Per-directory is xfs/ext4 `prjquota`; out of scope.
- **Kubernetes-style requests/limits** with burst credits or aggregator accounting. cgroups v2 doesn't do that out of the box and we're not adding lua to nginx.
- **Historical usage graphs.** `GET /users/:id/usage` returns current only. Historical is M13 territory — we surface the raw `CPUUsageNSec` so M13 can snapshot it later.
- **Grace periods for quota.** POSIX quota supports soft/hard + grace, but expressing grace sanely in a UI is its own feature. v1 is hard-limit only (`setquota -u <user> <blocks> <blocks> 0 0`).
- **Alerting / notifications** on threshold crossing. That's M14.
- **`limits apply --all`** / global re-apply. Reconciler does that on its own cadence; a CLI global-apply is a foot-gun (accidentally stomping on live overrides during maintenance).
- **Memory.swap_max / memory.zswap.max tuning.** Defaults are fine; exposing them blows the surface area for no customer-visible value.
- **CPU pinning / NUMA.** Not relevant at the VPS-hosting scale we target.

---

## 3. Invariants (must hold on every step)

- `user_limit_overrides.user_id` FK is `ON DELETE CASCADE`. Deleting a user wipes overrides; reconciler detects the missing override on next pass and re-applies pure-package limits.
- `hosting_packages.disk_quota_mb = 0` (and every other new field = 0) means **unlimited** — no systemd directive emitted for that field, no `setquota` call (well, `setquota -u <u> 0 0 0 0` to clear).
- Every non-zero cpu/memory/tasks/io value is bounded: `cpu_quota_percent <= 10000` (100 cores), `memory_limit_mb <= 1048576` (1 TB), `max_tasks <= 100000`, `io_read_mbps/io_write_mbps <= 10000`. Bounds caught at API validation, again at agent pre-apply.
- `MemoryHigh = floor(MemoryMax * 0.9)`. Pinned. Invisible to admin. If admin requests 100MB memory, we emit `MemoryMax=100M` + `MemoryHigh=90M`.
- Agent `user.limits.apply` is idempotent — diff drop-in content vs what exists, skip write if identical. Systemd `daemon-reload` is always safe, so we call it unconditionally after any change to ensure cgroups v2 config is live.
- Nginx reload MUST survive config-test failure: `nginx -t` before `nginx -s reload`; rollback fragment file on failure. Same pattern as existing nginx reconcile.
- `quota` command failures do NOT block the systemd drop-in from being written. Disk and cgroups are independent surfaces; one failing shouldn't pin the other. Agent reports both outcomes in the response, reconciler logs but re-tries on next pass.
- User must exist on the host before `user.limits.apply` is called (same invariant as `user.slice.ensure`). Agent returns `user_not_found` with admin-visible hint if the account is missing.
- `/etc/systemd/system/jabali-user-<u>.slice.d/limits.conf` NEVER contains directives other than CPUQuota / MemoryMax / MemoryHigh / TasksMax / IOReadBandwidthMax / IOWriteBandwidthMax. The drop-in is a pure *values* file; structural directives (Slice, CPUAccounting) stay in the unit file from ADR-0025.
- Nginx zone names MUST be unique across the entire http{} scope. Enforced by prefixing with domain ID and validating uniqueness in the fragment generator before render.

---

## 4. Schema

### 4.1 `hosting_packages` additions

```sql
-- migrations/000042_add_resource_limits_to_hosting_packages.up.sql
ALTER TABLE hosting_packages
  ADD COLUMN cpu_quota_percent INT UNSIGNED NOT NULL DEFAULT 0 AFTER disk_quota_mb,
  ADD COLUMN memory_limit_mb   INT UNSIGNED NOT NULL DEFAULT 0 AFTER cpu_quota_percent,
  ADD COLUMN io_read_mbps      INT UNSIGNED NOT NULL DEFAULT 0 AFTER memory_limit_mb,
  ADD COLUMN io_write_mbps     INT UNSIGNED NOT NULL DEFAULT 0 AFTER io_read_mbps,
  ADD COLUMN max_tasks         INT UNSIGNED NOT NULL DEFAULT 0 AFTER io_write_mbps;
```

### 4.2 `domains` additions

```sql
-- migrations/000043_add_ratelimits_to_domains.up.sql
ALTER TABLE domains
  ADD COLUMN rate_limit_rps   INT UNSIGNED NOT NULL DEFAULT 0 AFTER nginx_rules,
  ADD COLUMN connection_limit INT UNSIGNED NOT NULL DEFAULT 0 AFTER rate_limit_rps;
```

### 4.3 New `user_limit_overrides`

```sql
-- migrations/000044_create_user_limit_overrides.up.sql
CREATE TABLE user_limit_overrides (
  user_id           CHAR(26)      NOT NULL PRIMARY KEY,
  disk_quota_mb     INT UNSIGNED  NULL,
  cpu_quota_percent INT UNSIGNED  NULL,
  memory_limit_mb   INT UNSIGNED  NULL,
  io_read_mbps      INT UNSIGNED  NULL,
  io_write_mbps     INT UNSIGNED  NULL,
  max_tasks         INT UNSIGNED  NULL,
  updated_at        TIMESTAMP     NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  CONSTRAINT fk_user_limit_overrides_user_id FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
```

Rate-limit overrides on domains are intentionally absent — if per-domain customization is needed, admin edits the domain row directly. The override table is strictly for package-level resource limits.

---

## 5. Agent wire contract

### 5.1 `user.limits.apply`

Request:
```json
{
  "username": "shuki",
  "disk_quota_mb": 5120,
  "cpu_quota_percent": 200,
  "memory_limit_mb": 4096,
  "io_read_mbps": 100,
  "io_write_mbps": 50,
  "max_tasks": 500
}
```

Drop-in rendered at `/etc/systemd/system/jabali-user-shuki.slice.d/limits.conf`:
```ini
[Slice]
CPUQuota=200%
MemoryMax=4096M
MemoryHigh=3686M
IOReadBandwidthMax=/ 100M
IOWriteBandwidthMax=/ 50M
TasksMax=500
```

Disk quota via `setquota -u shuki 5242880 5242880 0 0 /home` (blocks in 1KB units, explicit mount path). The `/home` arg is resolved at agent startup by `limits.QuotaMount()` via `/proc/mounts` lookup — NEVER use `-a` (would hit wrong mount on multi-partition hosts).

Response:
```json
{
  "username": "shuki",
  "dropin_path": "/etc/systemd/system/jabali-user-shuki.slice.d/limits.conf",
  "cgroup_applied": true,
  "quota_applied": true,
  "no_change": false
}
```

If `cgroup_applied=true` but `quota_applied=false`, the caller sees the partial-success state and the error message. Reconciler re-queues.

### 5.2 `user.limits.report`

Request: `{"username": "shuki"}`

Response:
```json
{
  "username": "shuki",
  "disk": {"used_kb": 1048576, "limit_kb": 5242880},
  "memory": {"current_bytes": 2147483648, "max_bytes": 4294967296},
  "cpu": {"usage_nsec": 123456789012, "quota_percent": 200},
  "tasks": {"current": 42, "max": 500},
  "io": {"read_bytes": 987654321, "write_bytes": 123456789}
}
```

### 5.3 `user.limits.clear`

Request: `{"username": "shuki"}`. Removes drop-in (`rm` + `daemon-reload`) and calls `setquota -u shuki 0 0 0 0 <home_mount>`. Idempotent.

---

## 6. API contract

- `GET /api/v1/users/:id/usage` → merged effective-limits (from `Resolve`) + current (from `user.limits.report`).
- `PUT /api/v1/users/:id/limit-overrides` — body is the nullable-per-field override struct; reconciler picks up on next pass.
- `DELETE /api/v1/users/:id/limit-overrides` — removes row.
- `PUT /api/v1/packages/:id` — existing endpoint, extended to accept new fields. On change, reconciler re-applies to all users holding that package.
- `PUT /api/v1/domains/:id` — existing endpoint, extended for rate_limit_rps and connection_limit. On change, nginx reconcile re-renders.

All new endpoints admin-only (RequireAdmin middleware).

---

## 7. CLI shape

```
jabali limits check
  → Probes: cgroups v2 unified ✅, /home has usrquota ✅, nginx has
    ngx_http_limit_req_module ✅ + ngx_http_limit_conn_module ✅.
    Exit 0 if all pass, non-zero with diagnostic text otherwise.

jabali limits apply <username>
  → Shortcut for POST /internal/users/:id/reapply-limits;
    useful for ops after manual drop-in edits.

jabali limits status <username>
  → Human-formatted `user.limits.report`. Shows current vs max,
    percent used, with color flags at >80% / >95%.

jabali limits package apply <package_id>
  → Loop over all users with this package, call apply for each.
    Progress line per user. Stops on first failure (admin decides).
```

---

## 8. Nginx fragment shape

`/etc/nginx/conf.d/jabali-ratelimits.conf`:
```nginx
# Auto-generated by jabali reconciler — do not edit.
limit_req_zone $binary_remote_addr zone=rl_01JA82XXXXXXX:10m rate=60r/m;
limit_conn_zone $binary_remote_addr zone=cn_01JA82XXXXXXX:10m;
limit_req_zone $binary_remote_addr zone=rl_01JA83YYYYYYY:10m rate=600r/m;
...
```

Per-vhost template injection (in the existing nginx vhost template):
```nginx
server {
  ...
  {{- if .RateLimitRPS }}
  limit_req zone=rl_{{ .ID }} burst={{ mul .RateLimitRPS 2 }} nodelay;
  {{- end }}
  {{- if .ConnectionLimit }}
  limit_conn cn_{{ .ID }} {{ .ConnectionLimit }};
  {{- end }}
  ...
}
```

Zone size `10m` ≈ 160k IPs tracked — enough for every plausible hosting site in v1. Documented in runbook.

---

## 9. Risks & mitigations

| Risk | Mitigation |
|------|------------|
| Existing hosts have `/home` on `/` → can't add `usrquota` without reboot | install.sh detects, prompts or fails loud with runbook-pointer. Operator-initiated fix. |
| `setquota` for 1000s of users is serial and slow | We're not there yet; batch via `edquota -p <template_user>` later. |
| `MemoryHigh` squeezes PHP-FPM into sustained swap → latency complaints | Default pkg has memory=0 (unlimited). Admin explicitly opts in per package. Runbook has the diagnostic recipe. |
| Nginx zone-name collision on domain rename | ULID-based zone names don't change on rename. Guarded at fragment render time. |
| `limit_req_zone` leaky-bucket surprises customers with 503s during legitimate bursts | Use `burst=<rps*2> nodelay` — absorbs short bursts without delay. Documented. If still too aggressive for a site, admin sets `rate_limit_rps=0` for that domain. |
| Admin sets memory_limit_mb=100 on a PHP-heavy site → OOM kills mid-request | Agent pre-apply rejects memory_limit_mb < 64 (hard floor). Also surfaced in UI tooltip: "Memory limit below 128 MB is not recommended for PHP." |
| `quotacheck` on a production `/home` locks the FS during scan | install.sh only runs it on fresh mounts. Existing mounts are operator-initiated. |
| Drop-in written but `daemon-reload` fails | Agent tears down the drop-in, reports failure. Reconciler re-tries. |
| A package memory bump affects 500 users at once, each killing OOM pids | **v1: not mitigated in code** — `jabali limits package apply --dry-run <pkg>` shows effective changes before writing; admin must test on a non-production user first. Decrease-confirmation workflow is a v2 feature. Runbook documents the pre-flight procedure. |

---

## 10. Acceptance criteria

- `jabali limits check` returns 0 on a fresh install.sh-provisioned host.
- Creating a package with disk=100MB + memory=256MB and assigning a user: within 1 reconcile cycle, the user's slice drop-in contains `MemoryMax=256M` + `MemoryHigh=230M`, and `quota -u <user>` shows 102400 KB hard limit.
- `dd if=/dev/zero of=~/big bs=1M count=200` as that user returns `Disk quota exceeded` at ~100MB.
- `php -r 'str_repeat("x",1024*1024*300);'` under that user OOMs (systemd-journald shows `memory-oom-kill`).
- A domain with rate_limit_rps=1 and a test client hitting it rapidly sees 503 from the second request within the burst window.
- Deleting the override row reverts all four limits on next reconcile pass.
- Admin UI displays current disk + memory usage within 10s of a real allocation.

---

## 11. Timeline estimate

Wave A: 0.5d (ADR + migration)
Wave B: 1d (resolver + install.sh)
Wave C: 2d (agent cmds + nginx generator)
Wave D: 1d (API + reconciler wiring)
Wave E: 0.5d (CLI)
Wave F: 2d (admin UI + user widget)
Wave G: 1d (E2E + runbook)

Total: ~8 dev-days. Half of that is Wave F (UI work).

---

## 12. Adversarial review — CLOSED

Reviewed 2026-04-19 by `architect` sub-agent. 5 CRITICAL + 4 HIGH + 4 MEDIUM findings surfaced and folded:

| # | Finding | Folded into |
|---|---------|-------------|
| C1 | Filesystem detection missing (btrfs/zfs silently unsupported) | Decision #2, Step 3, Step 4 |
| C2 | `setquota -a` could hit wrong mount | Decision #2a, §5.1, §5.3 |
| C3 | 1-pass race on user create without limits | Decision #8 (inline apply on `user.ensure`) |
| C4 | `daemon-reload` doesn't retroactively reclaim memory | Decision #5 (corrected), runbook item |
| C5 | `decreases_require_confirmation` unimplemented ceremony | Removed; §9 mitigation replaced with `--dry-run` + runbook |
| H6 | nginx 10m zone sizing may overflow on busy domains | Step 6 (documented formula, CLI diagnostic) |
| H7 | `/tmp` disk escapes unmitigated | Decision #13 (tmpfs with size cap), Step 4 |
| H8 | NULL/0 resolver semantics error-prone | Decision #11 (explicit table-test mandate) |
| H9 | `limit_conn` bypassable via botnet IPs | Runbook item (out of scope for code fix) |
| M10 | daemon-reload failure → drop-in orphaned | Step 5 (verify cgroup state after reload) |
| M11 | nginx zone file load order | Step 6 (`00-` prefix) |
| M12 | Agent bounds validation missing | Step 5 |
| M13 | UID recycling on delete | Runbook item |

Original reviewer full output archived in session transcript. No outstanding gates.
