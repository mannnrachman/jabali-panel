# ADR-0032: M18 per-user resource limits — POSIX quota + cgroups v2 + nginx

**Date**: 2026-04-19
**Status**: accepted
**Deciders**: shuki + Claude
**Related**: ADR-0025 (per-user systemd slices), ADR-0023 (M9 PHP-FPM pool manager)

## Context

`hosting_packages.disk_quota_mb` has lived in the schema since migration #003 but was never enforced — just metadata surfaced in the admin UI. Admins can assign a "5 GB disk" package to a user, but that user can still write until the host's physical disk is full. The same gap exists for every other resource a hosting customer can abuse: CPU, memory, I/O bandwidth, task count, per-domain HTTP request/connection rate.

M18 closes this gap. Shipping it requires deciding:

1. **Which kernel/FS primitives do we use for each resource?** Disk, CPU, memory, I/O, tasks, and per-domain HTTP limits live at different layers.
2. **How does enforcement reach existing hosts?** Most already have M11+/M12 shipped with a live `/home`.
3. **How are package fields, per-user overrides, and per-domain rate limits modeled?**
4. **What's the policy when an admin shrinks a package that's already assigned to 500 users?**
5. **Which filesystems do we support?** Debian 13's default is ext4 but operators run xfs, btrfs, zfs too.

## Decision

### 1. Three enforcement surfaces, one agent command per user

| Resource | Primitive | Config location |
|---|---|---|
| Disk | POSIX user quota (`setquota -u`) | `aquota.user` at filesystem root |
| CPU, memory, IO, tasks | cgroups v2 via systemd slice drop-in | `/etc/systemd/system/jabali-user-<u>.slice.d/limits.conf` |
| Per-domain HTTP requests | nginx `limit_req` | `/etc/nginx/conf.d/00-jabali-ratelimits.conf` + per-vhost directives |
| Per-domain HTTP connections | nginx `limit_conn` | Same file/directives |

The per-user slice from ADR-0025 already has `CPUAccounting=yes`, `MemoryAccounting=yes`, `TasksAccounting=yes` enabled. M18 adds a *drop-in* with limit directives; we never rewrite the base slice unit.

A single agent command `user.limits.apply` takes the full bundle (disk + cpu + memory + io + tasks) and atomically writes the drop-in + calls `setquota` + `systemctl daemon-reload`. Nginx rate limits are managed separately at domain reconcile time because they're per-domain, not per-user.

### 2. Filesystem support: ext4 + xfs, not btrfs/zfs

POSIX `setquota -u` works on ext4 and xfs. xfs additionally requires `xfs_quota -x -c 'enable -u' /home` after the `usrquota` mount option is live — a foot-gun we handle in install.sh.

btrfs and zfs have their own quota models (subvolume-scoped and dataset-scoped respectively) that don't map cleanly to per-user hosting quotas. Rather than paper over the gap with ad-hoc tooling, install.sh detects them and fails loud with an upgrade path: migrate `/home` to ext4 or xfs, or deploy a dedicated ext4 `/home` mount. Documented in the runbook.

tmpfs and ramfs are rejected outright — they can't persist quota state.

### 3. `setquota` uses the explicit mount path, never `-a`

Hosts often have multiple quota-enabled filesystems. `setquota -u <user> ... -a` targets *all* quota-enabled mounts, which on a box with `/var` on its own quota-enabled partition would apply the user's hosting quota to `/var` as well. That's a correctness bug, not a convenience.

Instead, agent startup resolves the mount containing `/home` via `/proc/mounts` (walking parents until it finds a mount boundary) and caches the path. Every `setquota`/`quota` invocation passes that explicit path.

### 4. `/tmp` gets a tmpfs cap, not per-user quota

Quota on `/` to contain `/tmp` writes is a disaster (root-fs quota surprises every system daemon). Instead, install.sh mounts `/tmp` as `tmpfs` with a global size cap (default 1 GB, configurable). All users share this pool, but no single user can exhaust the host via `/tmp`. Standard multi-tenant practice.

Existing hosts with `/tmp` on `/` get a runbook procedure to migrate; we don't touch it automatically.

### 5. Inline limits application on user.ensure, drift-detect via reconciler

A new user must never exist on the host without its limits applied. Rather than letting the reconciler catch up one pass later, `user.ensure` (the orchestration step that creates the Linux user, slice, and FPM pool) inlines a call to `user.limits.apply` as its final sub-step. The user's first process is always under the cgroup and quota it's supposed to be under.

The reconciler still runs a drift-detection pass every tick (`ReconcileUserLimits`) that reports current state via `user.limits.report` and re-applies on mismatch — handles the "admin ran `systemctl edit` by hand" case.

### 6. daemon-reload updates cgroup v2 live, but doesn't retroactively reclaim memory

Systemd's `daemon-reload` re-reads the drop-in and pushes the new cgroup v2 properties (`memory.max`, `cpu.max`, `pids.max`, `io.max`) to the kernel immediately. A process in that slice that subsequently tries to allocate beyond `memory.max` hits the new OOM boundary right away.

What `daemon-reload` does NOT do is reclaim memory already allocated to live processes. If a PHP-FPM worker was holding 200 MB and the admin lowers the package to 100 MB, the worker keeps its 200 MB until it exits (FPM will respawn it, at which point the child inherits the new limit). An operator needing a hard cutover runs `systemctl restart jabali-user-<u>.slice` — kills everything in the slice, FPM master respawns workers under the new limits.

Agent docs and runbook both call this out. The plan's earlier claim that "limits are re-read live on daemon-reload" was technically incomplete and is corrected here.

### 7. Nullable override table — NULL inherits, 0 = unlimited

A dedicated `user_limit_overrides` table with nullable per-field columns (not a JSON blob on `users`). Semantics:

| `hosting_packages.X` | `user_limit_overrides.X` | Effective |
|---|---|---|
| Any value | `NULL` | inherit from package |
| Any value | `0` | unlimited (override to unbounded) |
| Any value | `N > 0` | cap at N |

A dedicated table gives us `updated_at`, audit trail potential, and cheap cascade-delete on user removal. JSON on `users` tangles identity state with settings we reconcile independently.

### 8. MemoryHigh = 90% of MemoryMax, invisible to admin

systemd's `MemoryMax` is the hard cap (OOM boundary); `MemoryHigh` is the soft cap where the kernel starts throttling allocations to push the process back under. 90% is the standard gap (systemd's own docs recommend a 5-20% headroom). Admins don't see `memory_high_mb` in the UI — it's computed from `memory_limit_mb`. If we ever get a customer asking for a specific split, we revisit then.

### 9. Decrease-confirmation deferred to v2

The risk "admin shrinks a package and squeezes 500 users' PHP-FPM workers into OOM" is real. Building a confirmation workflow (queue, admin notification, explicit confirm) is its own feature. v1 ships *without* it. The mitigation is:

- `jabali limits package apply --dry-run <pkg>` shows effective changes per user before writing anything.
- Runbook documents the "test on a canary user first" procedure.
- Admins who shrink and regret it can re-set the package and reconciler will converge on the next pass (no data loss, just transient latency).

Will we miss this? Possibly. If the postmortem on the first "I accidentally shrunk prod" incident demands it, we build it then.

### 10. Nginx rate limits: single fragment file, 00- prefix, per-domain zone names

`limit_req_zone` declarations must live in `http{}` context, not `server{}`. One reconciler-managed file at `/etc/nginx/conf.d/00-jabali-ratelimits.conf` holds every zone declaration — the `00-` prefix forces alphabetical load ahead of any per-vhost includes. Per-vhost `limit_req` + `limit_conn` directives live inside the existing vhost templates, referencing the zones.

Zone names are `rl_<domain_id>` / `cn_<domain_id>` — ULID-safe, rename-safe. Zone size defaults to `10m` (~218k entries). Documented formula in the runbook; `jabali limits check` reports zone utilization so operators can resize per-domain if they hit capacity.

### 11. CLI named `jabali limits`, not `jabali cgroup`

Disk quota is POSIX, not a cgroup; nginx limits are neither. One namespace covers all three layers:

- `jabali limits check` — pure probe, no root needed (kernel config + /proc/mounts + nginx modules)
- `jabali limits apply <user>` — shortcut for API re-apply
- `jabali limits status <user>` — human-formatted `user.limits.report`
- `jabali limits package apply [--dry-run] <package_id>` — bulk apply across users holding the package

## Alternatives considered

### A. Userspace polling (`du` + app-layer block)

- **Pros:** Zero kernel/FS changes. No remount. Works on btrfs/zfs.
- **Cons:** Can't *prevent* overage — only reports. A user can still fill the disk between poll cycles. Panic-deleting files after-the-fact is a worse UX than EDQUOT.
- **Why not:** We're building a hosting panel. Hard enforcement is the expected bar; soft-reporting alone fails the category test.

### B. XFS/ext4 project quotas (`prjquota`)

- **Pros:** Per-directory — could quota `/home/<user>/` even if multiple users shared a UID.
- **Cons:** Requires `prjquota` mount option + inode project IDs; new for ext4; more complex tooling. Doesn't actually help us because our users map 1:1 to UIDs already.
- **Why not:** Added complexity for no marginal benefit in our hosting model. POSIX user quota is the right primitive.

### C. systemd-oomd for memory enforcement

- **Pros:** Kills runaway processes proactively based on memory pressure, not absolute bytes.
- **Cons:** Policy lives in a separate daemon (`systemd-oomd`) with its own config files and rules. Splits the enforcement story across two services. And: we want predictable "this user's package says 4 GB, they get exactly 4 GB" behavior, not pressure-based heuristics.
- **Why not:** Pressure-based OOM is great for desktop workloads, wrong model for multi-tenant hosting where customers pay for a specific cap.

### D. Per-package Linux cgroups outside systemd

- **Pros:** Could group all users on a package into a single cgroup with shared limits.
- **Cons:** Breaks per-user accounting, breaks per-user reporting, breaks the one-user-hits-OOM-doesn't-affect-others invariant.
- **Why not:** Shared-cgroup tenancy is explicitly what we do NOT want.

### E. Userland memory limit via PAM `pam_limits`

- **Pros:** Traditional, works without cgroups.
- **Cons:** Per-process `rlimit` (`ulimit -v`), not per-user-aggregate. A user with 10 FPM workers at 100 MB each = 1 GB effective, not 100 MB.
- **Why not:** Needed per-user-aggregate, which cgroups give us natively.

## Consequences

### Positive

- Admins can finally set enforceable packages; "5 GB disk, 2 cores, 4 GB RAM" actually means that.
- Single source of truth (panel DB) converged by the reconciler onto all three surfaces.
- Per-user override table supports one-off exceptions without forking packages.
- CLI gives operators a minimal surface to debug limits without a DB dive.
- Uses existing slice infrastructure from ADR-0025; we're not introducing a new process-supervision story.

### Negative

- `usrquota` mount flag on `/home` requires a remount, which is operator-initiated and disruptive on hosts where `/home` shares the root filesystem.
- btrfs/zfs users are blocked from M18 until they migrate. Documented, but still friction.
- Shrinking a package can squeeze live users. v1 ships without a confirmation workflow; trust the operator + `--dry-run`.
- Admins surprised that "my package now says unlimited memory" on existing rows because new columns default to 0. Documented in migration notes.

### Risks

- **Shared /tmp exhaustion.** With a 1 GB tmpfs for `/tmp`, one noisy user can temporarily lock out others. Runbook: bump `/tmp` size or add per-user `~/.tmp` with shell env.
- **Zone overflow.** A busy domain with `>200k` unique client IPs will silently recycle `limit_req_zone` entries. `jabali limits check` surfaces zone utilization; operators resize as needed.
- **Reconciler cascade on package change.** Updating `hosting_packages` triggers `user.limits.apply` for every user on that package. At 10k users that's 10k agent calls per reconcile pass. Measured + optimized only when it becomes a real problem (we're not at that scale).
- **daemon-reload ≠ live reclaim of already-allocated memory.** Documented loudly in the runbook and in agent logs.
