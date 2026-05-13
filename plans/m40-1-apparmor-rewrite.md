# M40.1 — AppArmor profile rewrite for AA 4.x

**Status:** drafted 2026-05-09 · branch `m40.1/apparmor-rewrite` (not yet created)

**Supersedes:** M40 implementation (parked per ADR-0086 amendment 2026-05-09).

**ADR target:** new **0092** — AppArmor profile authoring patterns for AA
4.x (or amend ADR-0086 with a §"Implementation v2").

## Why

M40 shipped 5 jabali daemon profiles. Live audit on mx
2026-05-09 found:

1. AA 4.x on Debian 13 returns EACCES for every Unix-socket
   `connect()` made by an in-profile process, regardless of whether
   the profile carries `unix (...)` rules, `network unix stream`,
   path rules for the target socket, or the canonical
   `abstractions/mysql` include. Disabling the profile lifts the block.
2. Three of four non-agent profiles (panel, kratos, stalwart-mail)
   never auto-attached because their declarations were
   `profile <name> flags=(complain) {` without a binary path attach.

All five profiles are now parked in `install/apparmor/*.disabled`;
`cleanup_apparmor_legacy()` keeps `/etc/apparmor.d/` clear of stale
copies on every install + jabali update.

Defense-in-depth on a panel-API RCE remains a real gap. M40.1 is the
re-author.

## Out-of-scope

- `php-fpm` / `nginx` profile authoring — same FP cliff that killed
  the M9 Snuffleupagus default; tenant code surface is too dynamic.
- Promoting `pdns` / `mariadb` / `redis-server` / `pdns-recursor`
  beyond the distro-supplied profiles in `apparmor-profiles-extra`
  (M40 Wave B already loads + complain-modes them; flip-mature CLI
  exists for operator promotion).

## Verification — what "done" means

1. `aa-status --json | jq '.profiles[] | select(.name | startswith("jabali-"))' | length` ≥ 4.
2. Each jabali- profile process attaches at exec —
   `cat /proc/$(pgrep -x jabali-panel)/attr/current` shows
   `jabali-panel (complain)` not `unconfined`.
3. **`aa-exec -p <name> -- /tmp/aa_smoke <socket>` passes for every
   socket the daemon dials in production**:
   - jabali-agent: agent socket, mariadb socket (gmysql DSN), redis
     socket, kratos admin socket, stalwart admin socket.
   - jabali-panel: panel-api socket, agent socket, mariadb, redis,
     kratos public socket.
   - jabali-bulwark: bulwark socket, panel-api socket.
   - jabali-kratos: kratos admin socket, kratos public socket,
     mariadb.
   - jabali-stalwart: stalwart admin/mail sockets, mariadb.
4. Real-workload smoke (after Step 5 soak) passes:
   login, user create, WordPress install, mail send + receive,
   `jabali update` tick, reconciler full pass — zero
   `apparmor="DENIED"` lines tagged with `profile=jabali-*`.
5. 7-day complain-soak on mx surfaces zero unexpected ALLOWED
   entries before flip-to-enforce.

## Caveman wave map

| Wave | Steps | Parallel? | Ship-ready exit |
|---|---|---|---|
| A | 1 → 2 | sequential | AA 4.x test bench + per-rule corpus on mx |
| B | 3 ‖ 4 | parallel after A | jabali-agent + jabali-panel rewritten + verified |
| C | 5 ‖ 6 | parallel after B | jabali-bulwark / kratos / stalwart rewritten + verified |
| D | 7 | sequential | 7-day soak → flip-mature each daemon → live smoke |
| E | 8 | sequential | ADR-0092 (or 0086 amendment), runbook, BLUEPRINT, memory |

---

## Step 1 — AA 4.x test bench

**Brief:** Build an `aa_smoke` Go binary that takes a socket path and
returns OK / FAIL, invoked under `aa-exec -p <name> --` to verify rule
coverage. Stash under `tools/aa-smoke/`; ship as a CLI tool the
operator can rerun on every kernel/AA bump.

**Tasks:**
1. Create `tools/aa-smoke/main.go` — accepts `<unix-socket-path>` arg,
   returns exit 0 + "OK" on connect, exit 1 + reason on failure.
2. Create `tools/aa-smoke/profile-corpus.yaml` — flat list mapping
   each profile name to the sockets it must reach.
3. Create `make aa-smoke` target — builds the binary, iterates the
   corpus, runs each via `aa-exec -p <profile>` and asserts. Exits
   non-zero on any FAIL.
4. Pre-corpus run (every profile DISABLED) confirms baseline OK.

**Verification:**
```bash
make aa-smoke
# Expect: every entry passes (no profiles loaded yet — pure baseline)
```

**Exit:** Test bench operates standalone; can drive the rewrite.

---

## Step 2 — Per-rule corpus on AA 4.x

**Brief:** Empirically map AA 4.x's unix-mediation rule grammar.
Document what does + does not work. The Debian 13 / AA 4.0.1
combination on mx is the canonical reference platform.

**Tasks:**
1. Create `docs/security/apparmor-aa4-rules.md` — minimal-reproducer
   profile that allows ONE socket, paste rule variants tested, mark
   each as PASS / FAIL with kernel version + AA parser version.
   Variants to test:
   - `network unix stream,` only
   - `unix (connect, receive, send) type=stream,` (broad)
   - `unix peer=(addr=<path>),` — known-bad syntax (addr= is for
     abstract sockets only); FAIL is the expected baseline.
   - `unix (connect, receive, send) type=stream peer=(label=<other-profile>),`
   - `unix (connect, receive, send) type=stream peer=(addr=@<abstract>),`
     — abstract sockets, where applicable.
   - File rules `<path> rw,` paired with `network unix stream,`.
   - `abstractions/mysql` standalone (uses `@{run}` macro).
2. For each PASS row, record the WORKING profile fragment so the
   rewrite step can copy-paste known-good snippets.

**Verification:**
- Every "PASS" row reproducible via `aa-exec -p <profile-stub> --
  ./tools/aa-smoke <socket>`.

**Exit:** Hard data on what AA 4.x accepts. Step 3+ uses the table.

---

## Step 3 — Rewrite jabali-agent

**Brief:** Most-privileged profile + the one that broke production
on M40 — start here. Use the corpus from Step 2 to author rules
that actually mediate (not just load).

**Tasks:**
1. Author new `install/apparmor/usr.local.bin.jabali-agent` against
   the per-rule corpus.
2. Add to `aa-smoke/profile-corpus.yaml` — every socket the agent
   dials in production (mariadb, redis, kratos admin, stalwart
   admin, agent's own listen socket, etc.).
3. Run `make aa-smoke` until all rows PASS.
4. Live-load the profile in COMPLAIN mode on mx; run a real
   `jabali update` cycle + a `domain create` + a backup; tail
   `journalctl -k` for `apparmor="DENIED"` lines.

**Exit:** jabali-agent profile in COMPLAIN mode passes the smoke
suite + a real reconciler tick on mx.

---

## Step 4 — Rewrite jabali-panel

**Brief:** Same approach. Profile MUST attach at exec — declaration
takes a binary path: `profile jabali-panel /usr/local/bin/jabali-panel { ... }`.

**Tasks:**
1. Author profile + corpus entry.
2. Verify `/proc/$(pgrep -x jabali-panel)/attr/current` is
   `jabali-panel (complain)` after the daemon restart.

**Exit:** Profile attaches + smoke passes.

---

## Step 5-6 — Rewrite jabali-bulwark / kratos / jabali-stalwart

**Parallel after agent + panel land.** Same recipe.

---

## Step 7 — 7-day soak + flip-mature

**Brief:** Same drill as M40 Step 5. Daily report timer counts
`apparmor="ALLOWED"` per profile; operator promotes via
`jabali apparmor flip-mature --profile <name>` only on profiles
with zero unexpected ALLOWEDs over 7 days.

**Tasks:**
1. Live-host on mx for 7 days.
2. Daily soak-report email landed via the existing M40 timer.
3. Flip in priority order: jabali-bulwark (smallest) →
   jabali-stalwart → jabali-kratos → jabali-panel → jabali-agent.

---

## Step 8 — ADR-0092 + runbook + memory

**Tasks:**
1. New `docs/adr/0092-apparmor-aa4-rules.md` — the empirical
   findings + the chosen rule patterns + non-obvious failure modes.
2. Reverse-link from ADR-0086 amendment.
3. New `plans/m40-1-apparmor-rewrite-runbook.md` — operator
   procedure for adding a new path to a profile, debugging
   `aa-exec -p` failures, rolling back to disabled state.
4. `docs/BLUEPRINT.md` — flip M40.1 row to Shipped.
5. Memory: `project_m40_1_shipped.md` linked from `MEMORY.md`.

---

## Notes

- **Don't try this without the test bench.** M40 shipped on
  hand-authored profiles and untested complain-mode assumptions.
  The aa-smoke binary is the regression net for every future
  kernel bump.
- **AA's syntax shifted across versions** — pinning the corpus to
  the host's kernel + apparmor parser version is required; the
  bench captures both at run time.
- **Per `feedback_never_agents`** execute every step inline.
