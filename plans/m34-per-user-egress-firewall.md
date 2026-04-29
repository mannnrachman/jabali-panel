# M34 — Per-user PHP-FPM egress firewall (cgroupv2 + nftables)

**Status:** drafted 2026-04-29 · adversarial-review pending · branch `m34/per-user-egress`

**ADR target:** [0084](../docs/adr/0084-per-user-egress-firewall-cgroupv2.md)

**Replaces:** Snuffleupagus / Suhosin path (rejected — operator FP intolerance).
**Companion to (deferred phase 2):** open_basedir + disable_functions PHP-FPM hardening blueprint.

## Why

Closes the "PHP runtime exfil" gap left after the M33 amendment 3 (ClamAV
removal, native maldet scanner). Stronger than Snuffleupagus because the
filter sits at the kernel packet layer (`socket cgroupv2 level 2 user-<u>.slice`),
not in PHP — webshells cannot bypass via PHP-extension exec, LD_PRELOAD,
or in-process payload obfuscation. A `connect()` from any process inside
a user's slice that targets a non-allowlisted destination has its SYN
dropped before any userspace code runs.

Default allowlist covers the typical web workload (loopback for
mariadb/redis, DNS, HTTP/HTTPS for WP+Composer, SMTP submission for
mail()), so FP rate on a stock LAMP stack is approximately zero.

## Verification — what "done" means

A test webshell planted at `/home/<u>/domains/<d>/public_html/shell.php`
calling `fsockopen('192.0.2.1', 4444)` produces NO outbound SYN under
ENFORCED state (verified via `tcpdump -i any 'tcp[tcpflags] & tcp-syn != 0
and host 192.0.2.1'` showing zero packets), AND the drop is visible
in the per-user counter, AND the M14 burst event fires when 50 such
attempts happen within 60s.

---

## Caveman wave map

| Wave | Steps | Parallel? | Ship-ready exit |
|---|---|---|---|
| A | 1 → 2 | sequential | nftables generator wired into reconciler; users created via `user.create` get default-ENFORCED rules; agent CLI lets you toggle state per user without panel-api involvement |
| B | 3 ‖ 4 | parallel after A | admin + user API endpoints exposed |
| C | 5 ‖ 6 ‖ 7 | parallel after B | UI tabs + drop counter + M14 source |
| D | 8 | sequential after C | LEARNING auto-flip + runbook + live-VM smoke + ADR-0084 accepted |
| E | 9 | deferred (post-launch) | Tetragon companion policy for L3 audit |

---

## Step 1 — Schema + models + repos + ADR-0084 stub

**Branch:** `m34/per-user-egress` (root branch, all subsequent steps stack on top — no separate per-step branches; we cherry-pick to `main` per wave)

**Context (cold-start brief):**

Jabali ships per-user systemd slices `user-<u>.slice` (M18, ADR-0068) with
cpu/memory/io/pids cgroup v2 controllers. We extend this with a network
egress filter implemented via `nftables` (already on host per ADR-0054)
using the `socket cgroupv2 level 2` match — native nftables, no eBPF
program loading, no Tetragon dependency.

DB-as-truth (ADR-0002): admin policy lives in two new tables. Reconciler
(ADR-0004) renders host state from these rows.

**Files to create:**

```
panel-api/internal/db/migrations/000100_user_egress_policies.up.sql
panel-api/internal/db/migrations/000100_user_egress_policies.down.sql
panel-api/internal/db/migrations/000101_user_egress_requests.up.sql
panel-api/internal/db/migrations/000101_user_egress_requests.down.sql
panel-api/internal/models/user_egress.go
panel-api/internal/repository/user_egress_policy_repository.go
panel-api/internal/repository/user_egress_request_repository.go
docs/adr/0084-per-user-egress-firewall-cgroupv2.md  (Status=Proposed; final flips to Accepted in Step 8)
```

**Schema — 000100_user_egress_policies:**

```sql
CREATE TABLE user_egress_policies (
  user_id              VARCHAR(26)  NOT NULL,
  state                ENUM('off','learning','enforced') NOT NULL DEFAULT 'enforced',
  allowed_extra        JSON         NOT NULL,
  drop_count_24h       BIGINT UNSIGNED NOT NULL DEFAULT 0,
  drop_count_at        TIMESTAMP    NULL,
  -- learning_started_at anchors the 7-day auto-flip-to-enforced timer
  -- (Step 8). NULL on enforced/off rows. Set when state transitions to
  -- 'learning' (DB trigger or app code; we use app code in the repo
  -- to keep migrations dumb).
  learning_started_at  TIMESTAMP    NULL,
  updated_at           TIMESTAMP    NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  updated_by           VARCHAR(26)  NULL,
  PRIMARY KEY (user_id),
  CONSTRAINT fk_user_egress_user FOREIGN KEY (user_id)
    REFERENCES users(id) ON DELETE CASCADE
) ENGINE=InnoDB CHARSET=utf8mb4;
```

`allowed_extra` shape: `[{"cidr":"203.0.113.0/24","port":443,"protocol":"tcp","comment":"corporate-bastion"}, ...]`.

`drop_count_24h` + `drop_count_at` denormalised from nft counters every reconciler tick (Step 7) so the admin UI doesn't need to shell into nft on every page load.

**Schema — 000101_user_egress_requests:**

```sql
CREATE TABLE user_egress_requests (
  id           VARCHAR(26)  NOT NULL,
  user_id      VARCHAR(26)  NOT NULL,
  cidr         VARCHAR(43)  NOT NULL,
  port         INT UNSIGNED NULL,
  protocol     ENUM('tcp','udp')  NOT NULL DEFAULT 'tcp',
  reason       VARCHAR(500) NOT NULL,
  status       ENUM('pending','approved','denied') NOT NULL DEFAULT 'pending',
  reviewed_by  VARCHAR(26)  NULL,
  decided_at   TIMESTAMP    NULL,
  created_at   TIMESTAMP    NOT NULL DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY (id),
  KEY idx_egress_req_user_status (user_id, status, created_at),
  CONSTRAINT fk_egress_req_user FOREIGN KEY (user_id)
    REFERENCES users(id) ON DELETE CASCADE
) ENGINE=InnoDB CHARSET=utf8mb4;
```

**Models:** mirror schemas. `UserEgressPolicy.AllowedExtra` is `[]EgressDestination` JSON, marshalled via `json.RawMessage`+lazy decode pattern from `MalwareEvent.RawJSON` (panel-api/internal/models/malware.go:72).

**Repo methods:**
- `UserEgressPolicyRepository`: `Get(userID)`, `Upsert(p)`, `EnsureDefault(userID)` (idempotent insert with state=enforced + empty allowed_extra), `List()`, `BumpDropCount(userID, delta)`, `ListAllForReconcile()` (joins users to fetch usernames in one query).
- `UserEgressRequestRepository`: `Create(r)`, `Get(id)`, `ListPending()`, `ListByUser(userID)`, `Decide(id, status, reviewedBy)`.

**Wire into `app.Deps`:** add `UserEgressPolicies` + `UserEgressRequests` fields next to `MalwareUserScans`. Init in `serve.go` next to `deps.MalwareUserScans = ...` (panel-api/cmd/server/serve.go:258).

**ADR-0084 stub** — Status=Proposed, decision body covers:
- Why nftables `socket cgroupv2` over eBPF cgroup_skb (no extra daemon, fewer moving parts, kernel ≥5.7 already required)
- Why per-user not per-pool (matches existing M18 user-slice topology; pools live inside slices)
- Default allowlist rationale (why :443, :587, etc.)
- Reconciler convergence model (DB → /etc/nftables.d/ → `nft -f`)
- Phase 2 reservations: open_basedir, disable_functions, Tetragon companion

**Build + verify:**
```bash
cd panel-api && go build ./... && go test ./internal/repository/... -run UserEgress -count=1
mysql -u jabali_panel_app -p$(cat /etc/jabali/db-password | tr -d '\n') jabali_panel \
  -e 'DESCRIBE user_egress_policies; DESCRIBE user_egress_requests'
```

**Exit criteria:**
- Migrations apply clean on a fresh DB and on a host that already ran every prior migration
- Repo unit tests pass (table-driven, hit a temp sqlite or mariadb fixture per existing pattern)
- ADR-0084 markdown lints (no broken cross-refs)
- `go build ./...` clean across panel-api

**Rollback:** `golang-migrate -path … down 2`; revert ADR file.

---

## Step 2 — nftables writer + reconciler + install.sh

**Depends on:** Step 1 (models + repo).

**Context:**

Reconciler ticks every 60s (existing pattern, panel-api/internal/reconciler/).
Adds a new pass that:

1. Reads all `user_egress_policies` joined to `users` (username, uid).
2. Renders `/etc/nftables.d/jabali-per-user-egress.nft` from a Go template.
3. Atomically `mv` over the prior file.
4. Runs `nft -f /etc/nftables.d/jabali-per-user-egress.nft` (NOT `systemctl reload nftables` — flushes other tables).
5. Verifies via `nft -j list table inet jabali_per_user` and stores success/error timestamp.

**Template (vmap dispatch — O(1) per-user routing):**

Naive per-user-rule emission is O(N) matches per packet (1000 users → 1000
`socket cgroupv2 level 2 "..."` evals per egress). Use nftables `vmap`
to dispatch to a per-user chain in one hash lookup:

```nft
# Generated by jabali — do not edit. Source of truth: user_egress_policies.
# Reload via: nft -f /etc/nftables.d/jabali-per-user-egress.nft

table inet jabali_per_user {
  set default_loopback4 {
    type ipv4_addr; flags interval;
    elements = { 127.0.0.0/8, 10.0.0.0/8, 172.16.0.0/12, 192.168.0.0/16 }
  }
  set default_loopback6 {
    type ipv6_addr; flags interval;
    elements = { ::1/128, fc00::/7, fe80::/10 }
  }
  set default_ports_tcp {
    type inet_service;
    elements = { 53, 80, 443, 587, 465, 25 }
  }
  set default_ports_udp {
    type inet_service;
    elements = { 53 }
  }

  # One counter per user — read by reconciler every tick, written into
  # user_egress_policies.drop_count_24h (Step 7).
  counter user_<u>_drops {}
  # ... one per ENFORCED user

  # Per-user policy chains. Only emitted for users with state in
  # (learning, enforced); off-state users have no chain so the vmap
  # lookup misses → packet falls through to default accept.
  chain user_<u>_enforced {
    ip  daddr @default_loopback4 accept
    ip6 daddr @default_loopback6 accept
    tcp dport @default_ports_tcp accept
    udp dport @default_ports_udp accept
    # Per-user extras (one rule per allowed_extra entry)
    ip daddr 203.0.113.0/24 tcp dport 443 accept comment "extra:<request_id>"
    # Default deny w/ counter
    counter name user_<u>_drops drop
  }

  chain user_<u>_learning {
    ip  daddr @default_loopback4 accept
    ip6 daddr @default_loopback6 accept
    tcp dport @default_ports_tcp accept
    udp dport @default_ports_udp accept
    # Per-user extras (same as enforced)
    ip daddr 203.0.113.0/24 tcp dport 443 accept comment "extra:<request_id>"
    # Would-drop: log + accept. Rate-limit log to 5/min/user so dmesg
    # doesn't drown if a runaway loop fires fsockopen() in tight cycle.
    limit rate 5/minute log prefix "jabali-egress-learn-<u> " group 0
    counter name user_<u>_drops accept
  }

  # Hashed dispatch by cgroup path → O(1) routing. Only entries for users
  # NOT in 'off' state get emitted; off-state slices fall through to the
  # default-accept policy outside this table.
  map cgroup_to_chain {
    type cgroupsv2 : verdict
    elements = {
      "user-<u_enforced>.slice"  : jump user_<u_enforced>_enforced,
      "user-<u_learning>.slice"  : jump user_<u_learning>_learning,
      ...
    }
  }

  chain output {
    type filter hook output priority 0; policy accept;
    socket cgroupv2 level 2 vmap @cgroup_to_chain
  }
}
```

Performance: 1 hash lookup per packet regardless of user count, vs N
linear matches in the naive form. Validated on a 1000-user benchmark
in Step 8 smoke (target: <2µs added latency per packet, p99).

The `limit rate 5/minute` on the LEARNING log line prevents dmesg flood;
the counter still increments on every drop so the M14 burst source
(Step 7) sees the true rate.

**Files:**

```
panel-agent/internal/commands/user_egress_apply.go
panel-agent/internal/commands/user_egress_apply_test.go
panel-agent/internal/commands/user_egress_template.go      (the nft Go template + render fn)
panel-api/internal/reconciler/user_egress_reconciler.go    (calls user.egress.apply once per tick)
install.sh — install_per_user_egress() wired into install_malware_stack neighbour position
```

**Agent commands:**
- `user.egress.apply` — body: `{users: [{username, uid, state, allowed_extra: [...]}]}`. Atomic write + nft -f. Returns `{applied: true, table_version: "..."}` or `{applied: false, error: "..."}`.
- `user.egress.read_counters` — returns `{counters: [{username, drop_count, last_reset}]}`. Reconciler calls every tick, writes back to DB.

**install.sh:**

```bash
install_per_user_egress() {
  # nftables already installed by install_ufw_crowdsec_nftables()
  install -d -m 0755 /etc/nftables.d
  install -d -m 0750 /etc/jabali

  # Default mode: ENFORCED on new installs. Hosts upgrading from a build
  # WITHOUT this feature get one-time LEARNING for 7 days (auto-flip via
  # Step 8 timer), gated by the marker.
  if [[ ! -f /etc/jabali/.per-user-egress-installed ]]; then
    if [[ -f /etc/jabali/installed ]]; then
      # Existing host: start in LEARNING. Flip-to-enforced timer set up below.
      echo "learning" > /etc/jabali/per-user-egress.mode
      _log "per-user egress: LEARNING mode for 7 days (existing host upgrade)"
    else
      echo "enforced" > /etc/jabali/per-user-egress.mode
      _log "per-user egress: ENFORCED on first install"
    fi
    touch /etc/jabali/.per-user-egress-installed
  fi

  # Empty file at first; reconciler fills it as soon as panel-api boots.
  if [[ ! -f /etc/nftables.d/jabali-per-user-egress.nft ]]; then
    cat >/etc/nftables.d/jabali-per-user-egress.nft <<'NFT'
# Generated by jabali — do not edit. Reconciler will overwrite.
table inet jabali_per_user { }
NFT
    nft -f /etc/nftables.d/jabali-per-user-egress.nft 2>/dev/null || true
  fi

  # Step 8 timer for the LEARNING auto-flip is wired below in install_malware_stack
  # neighbour function — keeps related units co-located.
}
```

**Build + verify:**
```bash
cd panel-agent && go test ./internal/commands/... -run UserEgress -count=1
go build ./...
# Smoke render with two synthetic users:
go run ./cmd/test/egress_render --users 'alice/1001/enforced/[]' 'bob/1002/learning/[]'
# Output validated by `nft -c -f -` (parser only, no apply).
```

**Exit criteria:**
- Agent unit tests pass (template golden file: 1 enforced + 1 learning + 1 off + 1 with extras)
- `nft -c -f generated.nft` parses on the dev box
- Reconciler runs once on the VM and produces a non-empty file matching expected user count
- No regression in other reconciler passes (run e2e smoke from `e2e/`)

**Rollback:** `rm /etc/nftables.d/jabali-per-user-egress.nft && nft delete table inet jabali_per_user 2>/dev/null; systemctl restart panel-api` (reconciler will recreate).

---

## Step 3 — Admin egress API endpoints (parallel with Step 4)

**Depends on:** Step 1.

**Context:**

Admin needs to view + edit per-user egress policy. Endpoints under
`/admin/users/:id/egress` follow the convention pattern (RegisterEgressRoutes
called from app.go) and reuse `RequireAdmin()` middleware.

**Files:**

```
panel-api/internal/api/user_egress.go
panel-api/internal/api/user_egress_test.go
panel-api/internal/app/app.go    (register route family)
```

**Routes:**

```
GET    /admin/users/:id/egress
PUT    /admin/users/:id/egress
POST   /admin/users/:id/egress/test     (5min ephemeral hole; audit row written; auto-revoked)
GET    /admin/egress-requests           (list pending)
POST   /admin/egress-requests/:id/approve
POST   /admin/egress-requests/:id/deny
GET    /admin/egress-summary            (drop counts aggregate; 24h)
```

**Body for PUT:**
```json
{
  "state": "enforced",
  "allowed_extra": [
    {"cidr": "203.0.113.5/32", "port": 443, "protocol": "tcp", "comment": "github API"}
  ]
}
```

Validation: `cidr` parsed via `net.ParseCIDR`; `port` ∈ [1, 65535]; `protocol` ∈ {tcp, udp}; `state` ∈ {off, learning, enforced}; `allowed_extra` capped at 50 entries (UI guardrail; raise if needed).

`POST .../egress/test` — temporary admin-debug allowlist entry with `expires_at = now+5min`. Reconciler renders it; another reconciler tick after expiry strips it. Audit row in `audit_log` table (existing M26 audit infra) capturing `{admin, target_user, cidr, port, expires_at}`.

**Build + verify:**
```bash
cd panel-api && go test ./internal/api/... -run UserEgress -count=1
# Manual: curl -u admin:tok PUT /admin/users/<id>/egress  → expect 200, then GET shows new state
```

**Exit criteria:**
- Handler unit tests cover: 401 for non-admin, 400 for malformed CIDR, 200 for happy path, audit row written for /test, expires_at honoured
- OpenAPI spec updated if `docs/openapi.yaml` exists (skip silently if not)

**Rollback:** revert route registration in app.go.

---

## Step 4 — User-facing /me/egress endpoints (parallel with Step 3)

**Depends on:** Step 1.

**Context:**

User can see their own policy + last 24h drop count, and submit a request
for a destination they need (e.g., third-party API). Admin reviews via Step 3.

**Files:**

```
panel-api/internal/api/me_egress.go
panel-api/internal/api/me_egress_test.go
panel-api/internal/app/app.go    (register route family)
```

**Routes:**

```
GET    /me/egress                  → {state, allowed_default: [...], allowed_extra: [...], drop_count_24h, last_drop_at}
POST   /me/egress/request          → body: {cidr, port?, protocol?, reason}; creates pending request; M14 notify admin
GET    /me/egress/requests         → list user's own requests (any status)
DELETE /me/egress/requests/:id     → cancel pending request (no-op if already decided)
```

`/me/egress` resolves user-id from session (Kratos JWT claim, existing
pattern from /me/* routes). User cannot see other users' policies —
authorization is implicit via session-derived user_id.

**Build + verify:**
```bash
cd panel-api && go test ./internal/api/... -run MeEgress -count=1
# Manual: log in as test1, curl /me/egress → expect own policy
```

**Exit criteria:**
- Tests cover: anonymous → 401; user A cannot read user B's policy; request creation fires M14 notification (mock dispatcher asserted)
- M14 notification source `egress_request_submitted` registered (Step 7 adds the burst source)

**Rollback:** revert route registration.

---

## Step 5 — Per-user UI: Egress tab (parallel with 6, 7)

**Depends on:** Step 3, Step 4.

**Context:**

Per-user view lives under `User profile → Egress` tab. Admin sees full
editor; user sees read-only + request modal. Same shell, different
permission gates.

**Files:**

```
panel-ui/src/shells/admin/users/UserEgressTab.tsx
panel-ui/src/shells/user/me/MeEgressPage.tsx
panel-ui/src/hooks/useUserEgress.ts
panel-ui/src/hooks/useMeEgress.ts
panel-ui/src/shells/admin/users/UserDrawer.tsx     (add tab)
```

**Components:**

- `EgressStateBadge` — Tag (green=off, yellow=learning, red=enforced — counterintuitive; double-check with operator. **Decision:** green=enforced (safer default), yellow=learning, grey=off)
- `DefaultAllowlistCard` — read-only display of the global default
- `ExtraDestinationsTable` — SearchableTable with CRUD via Drawer (per CONVENTIONS.md)
- `DropCounter` — last 24h count + sparkline if metric backend allows; otherwise just count + `last_drop_at`
- `RequestAccessModal` — for /me view; cidr+port+reason fields

**Build + verify:**
```bash
cd panel-ui && npx tsc -b && npm run build
# Manual: navigate to /jabali-admin/users → click test1 → Egress tab → set extra entry → save → tcpdump verifies dest opened
```

**Exit criteria:**
- TypeScript clean
- Drawer save round-trips (network panel shows PUT → 200)
- Mobile responsive (viewport 375px should show table with horizontal scroll, AntD pattern)

**Rollback:** drop the new tab key from `UserDrawer.tsx`.

---

## Step 6 — Admin Server Settings: global default allowlist + drops widget (parallel with 5, 7)

**Depends on:** Step 3.

**Context:**

Operator may want to harden defaults further (e.g., remove :25/:465/:587
on a no-mail-from-PHP host) or add a CIDR like internal monitoring.
Lives at `/jabali-admin/server-settings → Security` tab.

**Files:**

```
panel-ui/src/shells/admin/server-settings/EgressDefaultsCard.tsx
panel-ui/src/shells/admin/server-settings/EgressDropsWidget.tsx
panel-api/internal/api/admin_server_settings.go    (add /admin/server-settings/egress-defaults)
panel-api/internal/db/migrations/000102_server_settings_egress_defaults.up.sql
panel-api/internal/db/migrations/000102_server_settings_egress_defaults.down.sql
```

**Migration number guard:** before commit, run
`ls panel-api/internal/db/migrations/ | tail -5` and re-number to
`000103+` if any concurrent worktree has landed `000102` ahead of you
(memory: `feedback_merge_audit_migrations`).

`server_settings` table (singleton, M18+) gets two new columns:
- `egress_default_loopback_cidrs` JSON  (defaults: `["127.0.0.0/8","10.0.0.0/8","172.16.0.0/12","192.168.0.0/16"]`)
- `egress_default_ports_tcp` JSON  (defaults: `[53, 80, 443, 587, 465, 25]`)
- `egress_default_ports_udp` JSON  (defaults: `[53]`)

Reconciler (Step 2) reads these on every tick. Operator changes propagate
within one tick (~60s). Audit row written on update.

**Drops widget:** simple AntD `<Statistic>` showing total system-wide
drops in last 24h, with click-through to `/jabali-admin/users` filtered
to users with non-zero drop counts.

**Build + verify:**
```bash
cd panel-ui && npx tsc -b
cd panel-api && go test ./internal/api/... -run ServerSettings -count=1
```

**Exit criteria:**
- New columns added by migration (next free after Step 1)
- Operator-edited defaults surface in next reconciler tick (verify by editing → wait 90s → `nft list set inet jabali_per_user default_ports_tcp` shows new ports)
- Aggregate widget loads in <500ms with 100 users (perf check)

**Rollback:** `migrate down`.

---

## Step 7 — Drop counter + M14 source `egress_drop_burst` (parallel with 5, 6)

**Depends on:** Step 2.

**Context:**

nftables counters are read by reconciler (every 60s) via
`nft -j list counters table inet jabali_per_user`. Diff vs prior tick =
drops in last 60s. If a single user crosses N drops/min (default 50,
admin-tunable) we emit a M14 event `egress_drop_burst` to the
notifications queue (ADR-0056).

This is the SIGNAL operators care about: webshell → unable to phone
home → tries again → tries again. Hundreds of drops in 60s = "user is
infected, look now".

**Files:**

```
panel-api/internal/eventsources/egress_drop_burst.go
panel-api/internal/notifications/sources/egress_drop_burst.go
panel-api/internal/db/migrations/000103_alter_server_settings_egress_burst.up.sql
   ALTER TABLE server_settings ADD COLUMN egress_burst_threshold INT UNSIGNED NOT NULL DEFAULT 50;
panel-api/internal/db/migrations/000103_alter_server_settings_egress_burst.down.sql
panel-ui/src/shells/admin/server-settings/EgressBurstCard.tsx
```

**Migration number guard:** check + re-number if any concurrent worktree
has landed `000103` (memory: `feedback_merge_audit_migrations`).

**Event payload:**
```json
{
  "kind": "egress_drop_burst",
  "severity": "warn",
  "user_id": "01KQ...",
  "username": "shukivaknin",
  "drops_in_window_60s": 142,
  "threshold": 50,
  "since": "2026-04-29T...",
  "deep_link": "/jabali-admin/users?egress_filter=high"
}
```

**Suppression:** debounce per-user; do not re-fire within 15min of last
fire for the same user (avoids notification storm during sustained
attack). Implementation: in-memory map with TTL; survives panel-api
restart by reading `notification_history` for last fire timestamp.

**Build + verify:**
```bash
cd panel-api && go test ./internal/eventsources/... -run EgressDropBurst -count=1
# Manual VM smoke: plant infinite-loop fsockopen webshell, watch /admin/notifications history fill within 60s
```

**Exit criteria:**
- Threshold respected (test with 49 drops → no fire; 51 drops → fire)
- Debounce suppresses repeats within 15min
- M14 admin channel delivers (in-app bell + email/ntfy depending on operator config)

**Rollback:** `migrate down`; remove eventsource registration.

---

## Step 8 — LEARNING auto-flip + ADR-0084 accepted + runbook + live-VM smoke

**Depends on:** Steps 2, 3, 5, 6, 7.

**Context:**

Existing hosts upgrading via `jabali update` start in LEARNING for 7 days
(Step 2 marker). Step 8 ships:

1. systemd-timer `jabali-per-user-egress-flip.timer` (daily at 03:30 UTC)
   that runs `jabali per-user-egress flip-mature` — flips users that have
   been in LEARNING ≥ 7 days to ENFORCED unless `/etc/jabali/per-user-egress.mode = learning`
   pin file exists.

2. Operator dashboard tile under Server Status showing
   `<N> users still in LEARNING; <D> days remaining`.

3. Runbook at `plans/m34-per-user-egress-runbook.md`:
   - troubleshooting blocked legitimate egress
   - how to add a temporary admin-debug hole
   - how to read kernel log for LEARNING-mode "would-drop" hints
   - how to check nft counters
   - rollback procedure (set state=off for all users, reload nft)

4. ADR-0084 status flip: Proposed → Accepted; reference live-VM smoke
   evidence in the consequences section.

5. Live-VM smoke on `192.168.100.150`:
   - plant test-shell that fsockopen('192.0.2.1', 4444) every 1s for 2min
   - observe drop counter advance, M14 burst event fire, tcpdump silent
   - flip user to LEARNING, observe `dmesg | grep jabali-egress-learn` populate
   - flip user to OFF, observe SYN visible in tcpdump (regression baseline)
   - cycle back to ENFORCED, observe drops resume

**Files:**

```
internal/cli/per_user_egress_flip_mature.go
plans/m34-per-user-egress-runbook.md
docs/adr/0084-per-user-egress-firewall-cgroupv2.md  (status flip + evidence)
install.sh — write the systemd timer + service unit
```

**Build + verify:** all of the above + `e2e/per-user-egress.spec.ts` (Playwright) covering admin policy edit + user request flow.

**Exit criteria:**
- Live-VM smoke green
- Runbook exists, links from ADR-0084 and from `BLUEPRINT.md`
- ADR-0084 Accepted with live-evidence cite
- M34 BLUEPRINT.md entry added under "Security" section
- E2E pass

**Rollback:** disable the timer; the table-flip CLI is non-destructive.

---

## Step 9 — Tetragon companion policy (DEFERRED — phase 2)

**Depends on:** Step 8 shipped.

**Context:**

L3 audit trail. nftables drops at packet layer; if a kernel bug or
mis-configured user.slice ever lets traffic slip past the cgroup match,
Tetragon's tcp_v4_connect kprobe catches it userspace-side and emits
a separate event source. Belt-and-braces.

**File:** `/etc/tetragon/tetragon.tp.d/jabali-per-user-egress-violations.yaml`

Skip in initial M34 ship. Revisit after 30 days of operating data —
if the M14 burst source has zero false-positives and zero
"violations not in nft drops" gaps, we don't need Tetragon here.

---

## Cross-cutting invariants (verified after every step)

- `go build ./...` clean across panel-api + panel-agent
- `npx tsc -b && npm run build` clean in panel-ui
- No new direct DB writes from handlers — repos only (ADR-0003)
- No new `--no-verify` git commits, no force pushes
- Live-VM at `192.168.100.150` healthy after each step's deploy
- Rebase onto latest `origin/main` before merging
- Migration numbers checked against `ls migrations | tail` to catch concurrent collisions (memory: feedback_merge_audit_migrations)

## Out-of-scope (deferred follow-ups)

- IPv6 — `socket cgroupv2` works for IPv6 too but rule duplication needed; phase 2 (memory: jabali likely IPv4-only on test VM today, validate before phase 2)
- Per-pool granularity (some users run multiple PHP-FPM pools at different trust levels) — needs slice-level scoping per ADR-0068 amendment; defer until concrete operator ask
- Egress rate-limiting (vs binary drop) — nftables `limit rate` could throttle suspicious egress; phase 3
- IDS-style payload inspection — out of scope; that's CrowdSec AppSec's job at HTTP layer

## Memory + ADR cross-references

- ADR-0002 — DB-as-truth (this plan respects)
- ADR-0004 — Reconciler convergence (this plan adds a new pass)
- ADR-0054 — UFW over iptables (this plan extends nftables config)
- ADR-0068 — Per-user cgroup v2 slice metrics (foundation)
- M14 (notifications), M18 (slices), M26 (security tab), M27 (CrowdSec AppSec) — all depended-on, none modified
- Memory pin: `feedback_install_sh_is_truth` — install.sh owns the install_per_user_egress() postconditions; runbook doesn't carry recovery state
- Memory pin: `feedback_merge_audit_migrations` — Steps 1, 6, 7 each add migration; verify next-free at branch tip before commit

---

## Dispatcher notes (for the agent executing each step)

- Branch: single `m34/per-user-egress` for the whole feature; per-step commits with conventional messages.
- Cherry-pick to `main` per wave (A → B → C → D → E) once all wave steps green and live-VM smoke pass.
- Live-VM smoke is mandatory for Wave A (foundation) and Wave D (final). Wave B + C smoke = ad-hoc curl + UI screenshot.
- Run `mcp__codebase-memory-mcp__detect_changes` before each commit to verify scope.
- If a step's exit criteria can't be met, STOP and report; don't half-merge.
