# ADR-0025: Per-user systemd slices

**Date:** 2026-04-18
**Status:** Accepted
**Deciders:** Shuki

## Context

The Jabali Panel hosts multiple users and runs per-user PHP-FPM pools (ADR-0023).
Today, all processes (FPM workers, cron jobs, shell sessions) comingle in the
default cgroup hierarchy, offering no per-user resource isolation or killable
boundaries. This limits our ability to enforce per-user memory limits,
prevent resource starvation between users, or provide a clean "abort" switch
for a user's entire workload.

The objective of this ADR is to establish a real cgroup hierarchy that
separates each user's workload:

```
-.slice (root)
  ├─ system.slice
  │   ├─ php8.5-fpm.service (global, being phased out)
  │   └─ ...
  └─ jabali.slice (new)
      └─ jabali-user.slice (new, container for all users)
          ├─ jabali-user-alice.slice (new, one per user)
          ├─ jabali-user-bob.slice (new)
          └─ ...
```

This provides per-user accounting and resource control, while keeping the
hierarchy simple and predictable.

## Decision

We are implementing a **per-user systemd slice hierarchy** with per-user FPM
master service units, placed under jabali-specific cgroups. The following 8
design decisions lock the architecture, implementation shape, and cutover
strategy.

---

## Design Decisions

### 1. Slice hierarchy: jabali.slice → jabali-user.slice → jabali-user-<user>.slice

**Decision:** Three-level slice hierarchy:

1. **`jabali.slice`** (top-level under `-.slice`): Root of all Jabali workloads.
2. **`jabali-user.slice`** (child of `jabali.slice`): Container for all per-user slices.
3. **`jabali-user-<user>.slice`** (child of `jabali-user.slice`): One per panel user.

The `-` separator in systemd slice names is significant: systemd interprets
`foo-bar.slice` as nested under `foo.slice`. Thus:
- `jabali-user.slice` is a child of `jabali.slice`
- `jabali-user-alice.slice` is a child of `jabali-user.slice`

**Important:** The `PartOf=` directive is **invalid on slice units** in systemd.
Slice hierarchy is determined solely by the `-` prefix in the unit name.
This decision is documented in comments in the systemd unit files.

#### Alternatives considered

- **Flat slice structure** (`jabali-alice.slice`, `jabali-bob.slice` directly under `-.slice`).
  Simpler naming, but loses the container level (`jabali-user.slice`), making
  aggregate per-user-pool accounting harder and future fabric pooling impossible.
  Rejected.
- **Using `PartOf=` for hierarchy.** Cleaner intent, but `PartOf=` is not supported
  on slice units (only on service/socket/timer units). Rejected.

#### Consequences

- Slice hierarchy is explicit and navigable: `systemd-cgls /jabali.slice` shows
  the full tree with all users as direct children of `jabali-user.slice`.
- Resource limits can be applied at each level: `jabali.slice` for cluster-wide
  caps, `jabali-user.slice` for per-user-pool aggregate, `jabali-user-<user>.slice`
  for individual user caps.
- The `-` separator is non-obvious; admins must understand it to reason about the
  hierarchy. Documented in runbooks and comments.

---

### 2. FPM master per user (not per version, not global)

**Decision:** Each panel user gets exactly one FPM master service unit:
`jabali-fpm@<user>.service` (templated on username). That master is placed in
the user's slice (`Slice=jabali-user-<user>.slice`) and runs
`php-fpm<ver> -y /etc/php/<ver>/fpm/pool.d/<user>.conf -F`.

The system-wide `php<v>-fpm.service` (from Sury's package) is disabled and masked
at install time to prevent package upgrades from re-enabling it.

#### Alternatives considered

- **Global FPM master with per-user cgroup-only isolation.** One master running
  all pools. Simpler to bootstrap, but cgroups don't isolate Zend opcode cache
  collisions, memory fragmentation, or re-execution overhead. Rejected.
- **Per-version-per-user** (`jabali-fpm-8.5@alice.service`, `jabali-fpm-7.4@alice.service`).
  Eliminates version-switch restarts, but requires multiple masters per user
  (~10–20 MB RSS per extra master). Deferred to post-MVP if users request
  zero-downtime version switches. See decision 4.
- **Service group (systemd socket activation).** Cleaner dependency graph, but
  systemd socket activation via `Sockets=` requires socket template units
  (`jabali-fpm@.socket`), which adds surface area. Will revisit if we add
  dynamic pool creation. Rejected for MVP.

#### Consequences

- One master per user consumes ~10–20 MB of RSS per additional master (memory
  footprint grows linearly with user count). Acceptable up to hundreds of users;
  at 1000+ users, revisit per-version pooling.
- On a per-user PHP version upgrade, the agent rewrites the pool config file
  and restarts the master via `systemctl restart jabali-fpm@<user>.service`.
  Workers in-flight may be interrupted. Documented as expected during maintenance
  windows.
- Per-user FPM is strict isolation: one user's pool cannot share opcache or
  symlink table with another, reducing cache effectiveness slightly. Trade-off
  accepted for security and predictability.

---

### 3. Service unit shape: template + version pinned in drop-in

**Decision:** The FPM service unit is templated: `jabali-fpm@<user>.service`.

The unit file itself is **static** (installed via `install.sh`) and references
a placeholder for the PHP version, which is populated at runtime by a small shim
script:

- Unit file: `/etc/systemd/system/jabali-fpm@.service` (template)
- Shim script: `/usr/local/bin/jabali-fpm-runner` (looks up version from the
  per-user config file)
- Version config: `/etc/jabali-panel/user-phpver/<user>` (one-line file: `8.5`)

The unit's `ExecStart` line invokes the shim:
```
ExecStart=/usr/local/bin/jabali-fpm-runner %i
```

The shim reads `/etc/jabali-panel/user-phpver/%i`, sources the version, and execs:
```bash
exec /usr/sbin/php-fpm${VERSION} -y /etc/php/${VERSION}/fpm/pool.d/%i.conf -F
```

#### Alternatives considered

- **Bake version into the unit name** (`jabali-fpm-8.5@alice.service`).
  Version becomes immutable without recreating the service; admin sees clear
  version in `systemctl list-units`. Rejected because unit names are harder to
  template (three variables: version, user), and version upgrades require
  `stop old → rm link → create new link → start new`, which is brittle.
- **Use a drop-in override** (`/etc/systemd/system/jabali-fpm@alice.service.d/version.conf`
  with `ExecStart=`). Cleaner for version pinning per user. Rejected for MVP
  because it complicates the agent's file I/O (must maintain drop-in symlinks
  per user and handle reload timing). Deferred to post-MVP if we need to
  version-switch without service restart.
- **Hard-code version at install time** (one copy of `jabali-fpm@.service` per
  version). Defeats templating entirely. Rejected.

#### Consequences

- Templating the service unit keeps the install footprint minimal (one template
  per architecture, not per user/version combo).
- ExecStart shim adds a fork/exec on startup; negligible overhead (~1 ms) but
  not zero.
- Shim script must be robust against missing or malformed version files; logged
  errors aid debugging.
- Operator visibility: `systemctl list-units` shows `jabali-fpm@alice.service`,
  but the actual PHP version is in the per-user file. Document in runbooks.

---

### 4. No FPM multi-version per user (MVP)

**Decision:** In MVP, each user has exactly one PHP version (pinned in
`/etc/jabali-panel/user-phpver/<user>`). A user cannot run two versions
simultaneously. This matches ADR-0023 §3 ("one pool per user").

Future migration path: if multi-version per user becomes necessary (e.g.,
zero-downtime version upgrades), the template becomes `jabali-fpm-<ver>@<user>.service`,
and the agent manages both masters side-by-side. This design does not preclude it.

#### Alternatives considered

- **Multi-version from day one** (e.g., `jabali-fpm-8.5@alice.service` + `jabali-fpm-7.4@alice.service`).
  Supports zero-downtime version switches. Rejected for MVP to reduce agent
  complexity and avoid pre-allocating socket paths for every version/user combo.

#### Consequences

- Version upgrades incur a brief downtime (workers restart). Acceptable for
  maintenance windows (rare, pre-scheduled).
- Socket path `/run/php/php<ver>-fpm-<user>.sock` is unique per user/version,
  but per-user is fixed at pool creation time, so this is deterministic.
- If operators later need multi-version, the shim script can be enhanced to
  launch multiple masters. No breaking change to the slice hierarchy.

---

### 5. Shell + cron capture via user@<uid>.service drop-in

**Decision:** Capture login shells and user-spawned timers by installing a
drop-in override at `/etc/systemd/system/user-<uid>.slice.d/jabali.conf`
(note: `user-<uid>.slice`, not `user@<uid>.service`):

```ini
[Slice]
Slice=jabali-user-<user>.slice
```

systemd places `user@<uid>.service` and its entire scope (logind sessions,
`systemd --user`, user timers run by the user's `systemd --user` instance)
under `jabali-user-<user>.slice`. Login shells, `at` jobs, and systemd user
timers all then land in the slice and can be accounted + limited.

#### Alternatives considered

- **Drop-in on `user@<uid>.service` itself** (`/etc/systemd/system/user@<uid>.service.d/jabali.conf`).
  Sets `Slice=` on the service directly. Cleaner intent, but `user@<uid>.service`
  is a template (installed by the OS) and overriding it per-instance requires
  symlinks or templating. The slice drop-in is simpler because slice units
  are purely hierarchical (no service instances).
- **systemd user-scope override** (in `/home/<user>/.config/systemd/user/`).
  Per-user control, no admin involvement. Rejected because Jabali admins, not
  users, control the cgroup hierarchy (security).

#### Consequences

- Login shells land in the user's slice and are accounted in `jabali-user-<user>.slice` cgroup.
  `systemd-cgls /jabali.slice` shows shell and FPM under the same user branch.
- **Gap (MVP):** Traditional `cron.service` crontabs (from `/etc/cron.d/`) are
  not captured; they run under `cron.service`'s cgroup and are not limited by
  the user's slice. This is a documented limitation. Workaround: migrate to
  systemd user timers or use per-user `crond` (future work).
- If a user runs an interactive shell and starts a background job, the job is
  killed if the user's slice is stopped. Acceptable for cleanup; document as
  expected behavior during slice deletion.

---

### 6. Resource defaults at jabali-user.slice (aggregate level)

**Decision:** The `jabali-user.slice` (container for all users) is configured
with resource accounting enabled:

```ini
[Slice]
MemoryAccounting=yes
TasksAccounting=yes
CPUAccounting=yes
```

No hard resource caps are set in MVP; admins tune per-user via pool-ini
overrides (ADR-0023) or later via a per-user cgroup cap UI. Accounting alone
provides metrics via `systemd-cgtop`, `systemctl status`, and cgroup v2 files
(`/sys/fs/cgroup/jabali-user.slice/memory.max`, etc.).

#### Alternatives considered

- **Set hard caps immediately** (`MemoryMax=` at `jabali-user.slice`).
  Prevents runaway memory use, but requires tuning per deployment size.
  Deferred; start permissive and tighten as usage patterns emerge.
- **No accounting at all** (defer to later step). Wastes the opportunity for
  early feedback; admins can't see resource use today. Rejected.

#### Consequences

- Accounting adds minimal overhead (~0.5 % CPU per system, negligible memory).
- Admins can use `systemd-cgtop` to monitor resource use in real time:
  ```
  systemd-cgtop -r /jabali.slice
  ```
- Per-user limits can be added via `/etc/systemd/system/jabali-user-<user>.slice.d/limits.conf`
  without reloading the top-level units.
- Resource controls propagate to child slices (if `CPUQuota=` is set on
  `jabali-user.slice`, children inherit it unless overridden).

---

### 7. Cutover strategy (step 6)

**Decision:** The migration from global `php<v>-fpm.service` to per-user
`jabali-fpm@<user>.service` units happens in step 6 and follows this sequence:

1. **Pre-provision** (steps 2–5): Install `/etc/systemd/system/jabali.slice`,
   `jabali-user.slice`, `jabali-fpm@.service` template, and per-user drop-ins
   (`user-<uid>.slice.d/jabali.conf`) for all existing users.
2. **Boot per-user units** (step 6a): Start one `jabali-fpm@<user>.service`
   per user via agent commands. Each master binds a new socket path
   (`/run/php/php<ver>-fpm-<user>.sock`), which is already expected by nginx
   vhost templates (unchanged since install).
3. **Verify** (step 6b): For each user, hit their vhost with a test request.
   Nginx config already targets the per-user socket; the request should be
   served by the per-user master.
4. **Probe via health-check** (step 6c): Invoke `jabali-healthcheck.php` (a
   helper script placed by step 5) that tests a known endpoint in a per-user
   vhost and confirms response. If any user fails, skip cutover for that user
   and flag for manual review.
5. **Stop + mask global** (step 6d): `systemctl stop php<v>-fpm.service &&
   systemctl mask php<v>-fpm.service`. Global FPM is no longer running.
6. **Validate cutover** (step 6e): Smoke test: all vhosts still serve PHP.
7. **Rollback on failure** (step 6f): If any step fails, immediately `systemctl
   unmask php<v>-fpm.service && systemctl start php<v>-fpm.service` to restore
   the global master. Per-user units remain (no conflict on sockets).

#### Alternatives considered

- **Immediate cutover** (stop global, start per-user, no rollback).
  Faster, but no abort switch if something is wrong. Rejected.
- **Gradual cutover per-user** (redirect domains to per-user one-by-one via
  nginx reconfig). Safer, but requires domain-level coordination and is slower.
  Deferred to post-MVP.

#### Consequences

- Cutover requires a maintenance window (steps 6a–6f take ~10–20 minutes,
  with a few seconds of per-user downtime during master restart).
- Per-user units coexist with the global master during steps 2–5 (low risk).
- Rollback is fast (~2 seconds to unmask and start global).
- If a user's health check fails (e.g., custom PHP code in their vhost is
  incompatible with FPM isolation), the agent flags it. Manual intervention
  may be required (e.g., fix the code, or stay on global FPM for that user).

---

### 8. Rollback: unmask + restart global master

**Decision:** If any part of the cutover (step 6) fails, rollback is:

```bash
systemctl unmask php<v>-fpm.service
systemctl start php<v>-fpm.service
```

The global master restarts and binds its default socket paths. Per-user units
remain running alongside the global master (no conflict: different sockets).
After rollback, administrators review the failure, fix the issue (e.g., code
bug, misconfiguration), and re-attempt the cutover.

#### Alternatives considered

- **One-touch automated rollback** (agent detects failure and rolls back automatically).
  Prevents cascading failures, but obscures the root cause. Rejected; prefer
  manual escalation for MVP.

#### Consequences

- Per-user and global FPM can coexist safely (socket paths don't collide).
  This is transient (minutes) and safe.
- Nginx vhosts still route to per-user sockets, so rolling back the FPM global
  alone does not restore service. Agent must also revert nginx config or
  temporarily disable per-user vhosts. Documented in runbook.
- If rollback is necessary, per-user units are left in place (cleaning them up
  is deferred to a later step). Admins can manually `systemctl disable
  jabali-fpm@<user>.service` per user if desired.

---

## Consequences

### Positive

- **Per-user cgroup isolation:** Each user's workload (FPM, crons, shells) is
  visually separated in `systemd-cgls` and can be accounted independently.
- **Resource enforcement:** Future steps can cap per-user memory, tasks, and CPU
  without affecting other users.
- **Clean abort switch:** Admins can `systemctl stop jabali-user-<user>.slice`
  to kill all of a user's processes (emergency shutdown).
- **Minimal install footprint:** Only static slice and template service units
  are installed; per-user units are created on-demand by the agent.
- **Coexistence:** Per-user units and the global master can run simultaneously
  during cutover, reducing risk.

### Negative

- **Runtime complexity:** The agent must manage per-user unit lifecycle (create
  slice, create service drop-in, enable/start service). Add-on complexity in
  `user_create.go` and `user_delete.go`.
- **Shim script overhead:** ExecStart shim adds a fork/exec on FPM startup.
  Negligible (~1 ms) but not zero.
- **Cron gap:** Traditional `cron.service` crontabs are not captured by the
  slice. Documented limitation; migration to systemd timers is recommended but
  not enforced in MVP.
- **Version-pin file management:** Agent must maintain `/etc/jabali-panel/user-phpver/<user>`
  files and validate their contents. Breakage here breaks FPM startup.
- **Maintenance window required:** Step 6 cutover requires downtime. Plan a
  maintenance window; expected duration ~10–20 minutes.

---

## Cross-References

- **ADR-0023** (M9 PHP/FPM pool manager): This decision refines the runtime
  placement of the per-user pools defined in ADR-0023. The pool-per-user
  design in ADR-0023 §3 stands unchanged; this ADR adds the systemd slice
  hierarchy that hosts those pools.
- **`plans/per-user-systemd-slices.md`**: 9-step blueprint for implementation
  (currently in-flight; this ADR documents decisions for step 1).
- **`docs/BLUEPRINT.md` §4.10** (PHP/FPM pools): Amended to reference this ADR
  for slice placement.
- **`docs/runbooks/per-user-slices.md`**: Operations guide (TBD step 9).

---

## Related Runbooks & Guides

- `docs/runbooks/per-user-slices.md` — Operational guide (diagnosis,
  per-user resource tuning, rollback procedure).
