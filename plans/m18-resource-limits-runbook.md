# M18 — Per-user resource limits runbook

Operator reference for the M18 enforcement stack: POSIX user quota on
disk, cgroups v2 via systemd slice drop-ins for cpu/memory/io/tasks,
and per-domain nginx rate/connection limits. Pairs with
`plans/m18-resource-limits.md` (blueprint) and `docs/adr/0032-m18-resource-limits.md`.

---

## 1. Host prerequisites — the single probe command

```bash
jabali limits check
```

Passes on a fresh `install.sh`-provisioned host. Failure classes:

| Output | Cause | Fix |
|---|---|---|
| `cgroups v2   FAIL   /sys/fs/cgroup is tmpfs` | Host booted with legacy cgroup hierarchy | Edit kernel cmdline: remove `systemd.unified_cgroup_hierarchy=0`, reboot. |
| `/home fs     FAIL   btrfs` (or `zfs`) | /home on unsupported filesystem | Migrate /home to ext4 or xfs — see §4 below. |
| `/home fs     FAIL   tmpfs` | /home is a tmpfs mount (lab setup) | Real deployment needs persistent storage. |
| `quota mount  FAIL   no mount point found` | `/proc/mounts` parsing failed or `/home` doesn't exist | Check mount state; `stat /home` should not fail. |
| `quota mount  WARN   /home on root filesystem` | `/home` shares `/` | Not a hard fail but means quota-on-root, which is unsafe. Migrate `/home` to its own partition. |
| `nginx limit_req FAIL not compiled in` | Packaged nginx is stripped | Install `nginx-full` or equivalent. |

---

## 2. Day-to-day ops

### Apply limits to one user

```bash
jabali limits apply shuki
```

Recomputes their effective limits (package + override) and pushes them
to the agent. Useful after:
- An operator edited `/etc/systemd/system/jabali-user-shuki.slice.d/limits.conf` by hand.
- The reconciler is paused for SSO-key rotation and a user just had their package changed.

### View live usage

```bash
jabali limits status shuki
```

```
user       shuki
disk       102400 KB / 5242880 KB
memory     1073741824 B / 4294967296 B
cpu        123456789012 ns total, quota 200%
tasks      42 / 500
```

Add `--json` for machine-readable output suitable for monitoring scrapers.

### Bulk apply across a package

```bash
# Preview — shows every user that would be touched, no writes.
jabali limits package apply --dry-run 01HNZB8V6Y4P...

# Do it.
jabali limits package apply 01HNZB8V6Y4P...
```

The `--dry-run` flag exists because ADR-0032 §9 explicitly deferred a
"confirm before shrink" workflow to v2 — operators are expected to
preview before tightening a production package.

---

## 3. Host-level validation (post-deploy smoke test)

Run these **on the test VM** after every M18-touching deploy. They
exercise the kernel + filesystem + nginx surfaces that browser tests
can't reach.

### Disk quota actually blocks writes

```bash
# As the test user (assume 100 MB quota assigned):
su - testuser -c 'dd if=/dev/zero of=/home/testuser/big bs=1M count=200 2>&1 | head'
# Expected: "Disk quota exceeded" at or near 100 MB.
quota -u testuser    # shows used ≈ hard limit, status *
```

### Memory cap actually OOMs

**IMPORTANT:** use `systemd-run --slice=jabali-user-<u>.slice --uid=<u>`,
NOT `su - <u>`. `su` spawns children in the invoking session's scope
(`/user.slice/user-0.slice/session-*.scope` when invoked from root
shell) — the jabali user-slice's MemoryMax never applies. `systemd-run`
places the child directly into the target slice.

```bash
# With memory_limit_mb=256 on the package:
systemd-run --uid=testuser --slice=jabali-user-testuser.slice \
  --wait --collect --setenv=HOME=/home/testuser -p MemorySwapMax=0 \
  /usr/bin/php -r 'str_repeat(chr(97), 1024 * 1024 * 400);'
# Expected: "Finished with result: oom-kill" + "status=9/KILL"
# Peak memory should be ~255M (just under the 256M hard cap).
journalctl --since "30 seconds ago" --no-pager | grep -iE "oom-kill"
```

### CPU throttling kicks in

Same `systemd-run` trick as above (su doesn't route through the slice).

```bash
# With cpu_quota_percent=50 (half a core):
systemd-run --uid=testuser --slice=jabali-user-testuser.slice \
  --setenv=HOME=/home/testuser \
  /bin/bash -c 'for i in 1 2 3 4; do yes > /dev/null & done; sleep 4; jobs -p | xargs -r kill -9'

# Read cpu.stat twice, 2 seconds apart; Δusage_usec should be ~1e6
# (1s of CPU per 2s wall = 50%). nr_throttled should increment.
base=/sys/fs/cgroup/jabali.slice/jabali-user.slice/jabali-user-testuser.slice
cat $base/cpu.stat; sleep 2; cat $base/cpu.stat
# usage_usec delta ≈ 1,000,000 (50% of one core)
# throttled_usec should grow substantially.
```

### Nginx rate limit returns 503 on burst

```bash
# With rate_limit_rps=1 on a domain:
for i in $(seq 1 10); do curl -o /dev/null -s -w '%{http_code}\n' https://example.com/; done
# Expected: a few 200s then 503s as the leaky bucket drains.
```

### Cgroup state matches the drop-in

After applying package limits to a user with live processes:

```bash
cat /sys/fs/cgroup/jabali.slice/jabali-user.slice/jabali-user-testuser.slice/memory.max
# Should equal memory_limit_mb * 1024 * 1024 (bytes).
cat /sys/fs/cgroup/jabali.slice/jabali-user.slice/jabali-user-testuser.slice/pids.max
# Should equal max_tasks (decimal).
```

If they don't match, the agent's `user.limits.apply` returned a
`KernelMismatch` string. Check the agent log.

---

## 4. Migrating /home to a quota-compatible filesystem

If `jabali limits check` reports btrfs/zfs/tmpfs/on-root, the host is
not M18-enforceable. Migration procedure (destructive — **backup first**):

1. Provision a new ext4 partition (e.g. `/dev/sdb1`) sized for expected `/home` growth.
2. Stop the panel + FPM pools: `systemctl stop jabali-panel jabali-fpm@*.service`
3. `rsync -aHAX /home/ /mnt/new-home/`
4. `umount /home` (fails if any process holds it open — kill them).
5. Edit `/etc/fstab`: replace the `/home` line with `/dev/sdb1 /home ext4 defaults,usrquota,grpquota 0 2`.
6. `mount /home` then `quotacheck -cugm /home` then `quotaon -v /home`.
7. `systemctl start jabali-panel`.
8. `jabali limits check` should now pass.

---

## 5. Common incidents

### "I set memory_limit_mb=256 and my PHP site is OOMing on every request"

Expected — 256 MB is aggressive for modern WordPress + plugins.
Raise to at least 512 MB, ideally 1024. The UI tooltip says this but
the runbook says it louder. If you can't raise the limit, look at the
per-domain `php_memory_limit` override first — shrinking that lets
individual PHP processes fail cleanly instead of hitting the cgroup
cap.

### "I tightened a package and some users' PHP-FPM workers are wedged"

`daemon-reload` updates the cgroup property live but does **not**
retroactively reclaim already-held memory (see ADR-0032 §6). Workers
keep what they had until they exit. Force a cutover by restarting the
user's slice:

```bash
systemctl restart jabali-user-<u>.slice
```

This kills every process in the slice — FPM master respawns workers
under the new limits.

### "`jabali limits apply` returns KernelMismatch"

The drop-in was written but the kernel's cgroup state doesn't match.
Usually a stale systemd daemon. Try:

```bash
systemctl daemon-reload
systemctl restart jabali-user-<u>.slice   # kills user processes
jabali limits apply <u>                   # retry, verify mismatch is gone
```

If the mismatch persists, check `/sys/fs/cgroup/jabali.slice/.../memory.max`
manually — if it reports "max" where you expect a number, systemd didn't
pick up the drop-in. Look for typos in `/etc/systemd/system/jabali-user-<u>.slice.d/limits.conf`.

### "nginx -t fails after I edited a rate-limited domain"

The Wave C agent command backs up the ratelimits fragment before
overwriting and rolls back on `nginx -t` failure. If you're seeing a
persistent failure, the rollback worked but nginx's RUNNING state
still has the bad config:

```bash
ls -la /etc/nginx/conf.d/00-jabali-ratelimits.conf*   # check .bak presence
nginx -t                                              # reproduce error
systemctl reload nginx                                # retry
```

If `.bak` exists, the reconciler will redo the apply on its next tick
— no manual action needed. The fragment is always derivable from the
DB.

### "limit_req zone `rl_01JA82...` is full / nginx dropping rate-limit state"

The default zone size is 10 MB ≈ 218k tracked IPs per domain. If a
domain's traffic exceeds that, nginx silently recycles old entries.
v1 has no per-domain zone tuning — if this becomes a real problem,
add a `zone_size_kb` field to `hosting_packages` (or per-domain) and
thread it through `nginx.ratelimits.apply`.

Diagnose: `nginx -V` + `strings /var/log/nginx/error.log | grep 'limit_req'`
should show "limiting requests" entries for affected IPs.

### "I deleted a user but their cgroup dir is still under /sys/fs/cgroup"

That's normal until the slice's last process exits. systemd garbage-
collects the cgroup on transition from active-with-children to
inactive. The drop-in file has already been removed by
`user.limits.clear` at delete time, so no enforcement is happening —
just a stale empty directory.

### "I want to check what limits are actually applied right now"

```bash
# The drop-in:
cat /etc/systemd/system/jabali-user-<u>.slice.d/limits.conf

# What systemd thinks:
systemctl show jabali-user-<u>.slice -p MemoryMax,CPUQuota,TasksMax,IOReadBandwidthMax

# What the kernel actually enforces (ground truth):
cat /sys/fs/cgroup/jabali.slice/jabali-user.slice/jabali-user-<u>.slice/{memory.max,cpu.max,pids.max}

# Disk quota:
quota -u <u> -s
```

---

## 6. Known limitations

- **btrfs/zfs unsupported.** Filesystem-specific; migrating `/home` is the only path forward. Documented in §4.
- **Swap not capped.** `MemoryMax` doesn't cover swap; a user can technically exceed their memory cap if the host has generous swap and `memory.swap.max` isn't set. v2 feature if needed.
- **`/tmp` shared tmpfs.** Every user shares the same `/tmp` size cap (default 1 GB). One noisy user can briefly lock out others. Increase `JABALI_TMP_SIZE` at install time if this bites.
- **Decrease-squeeze workflow deferred.** `jabali limits package apply --dry-run` is the safety net. If an operator ships a bad shrink, re-run with the old numbers and the reconciler converges on the next tick.
- **nginx `limit_conn` is per-IP-per-domain.** A distributed attack from 100 different IPs can still open `100 * ConnectionLimit` connections to a domain. fail2ban or modsecurity sits above this if higher-layer protection is needed.
- **daemon-reload doesn't retroactively reclaim memory.** Already-allocated pages stay until the process exits (see ADR-0032 §6 + §5 in this runbook).
- **UID recycling.** Deleting a user and recreating with the same username may briefly inherit the previous cgroup directory state; waiting out the systemd GC tick before the new user spawns processes avoids this.

---

## 7. Rollback procedure

If M18 enforcement needs to be backed out entirely (e.g. a kernel bug
breaks cgroup v2 accounting):

1. Disable the reconciler's limits pass — set `packages` / `limitOverrides` deps to nil in `serve.go`, rebuild, redeploy.
2. Clear all slice drop-ins:
   ```bash
   rm /etc/systemd/system/jabali-user-*.slice.d/limits.conf
   systemctl daemon-reload
   ```
3. Clear all disk quotas:
   ```bash
   for u in $(awk -F: '/\/home/ && $3 >= 1000 {print $1}' /etc/passwd); do
     setquota -u "$u" 0 0 0 0 /home
   done
   ```
4. Delete the nginx fragment:
   ```bash
   rm /etc/nginx/conf.d/00-jabali-ratelimits.conf
   nginx -t && systemctl reload nginx
   ```

Hosts return to pre-M18 behavior: package fields are still stored in
the DB but nothing enforces them. Revert migrations 000042-000044 if
you also want the schema rolled back (unlikely — the columns don't
hurt anything idle).

---

## 8. Related docs

- `plans/m18-resource-limits.md` — blueprint + reviewer findings log
- `docs/adr/0032-m18-resource-limits.md` — design decisions + rationale
- `docs/BLUEPRINT.md` — project-wide milestone tracker
