# M24 — IP Address Manager

**Branch:** `m24/ip-manager`
**Target:** 10 PR-sized steps across 5 waves, ~2 days dispatch.
**Status:** Draft, pending opus adversarial review.

## Goal

Admins CRUD a pool of managed IPs (IPv4 + IPv6) on the panel host and bind specific IPs to specific domains. When a domain is bound to an IP, nginx listens on that IP for the vhost and the DNS zone's apex/mail A/AAAA records resolve to that IP. Unbound domains continue to listen on the server's primary public IP (`public_ipv4`/`public_ipv6` from M3 — unchanged default).

## Scope

**In scope**
- `managed_ips` table (pool of admin-managed IPs).
- Admin UI: list, add, edit-label, delete IPs.
- Per-domain IP picker (admin + user shells): one IPv4 + one IPv6 max per domain, both optional.
- Agent binds/unbinds IPs ephemerally via `ip addr add/del`.
- nginx vhost template emits `listen <ip>:<port>` when domain has a bound IP.
- DNS zone `@` + `mail` records resolve to domain's bound IP (fallback = server primary).
- Reconciler converges nginx + DNS when a domain's IP binding changes.
- Atomic multi-domain reassignment (DB transaction + single reconcile pass).

**Explicitly out of scope — must stay out**
- **Netplan / systemd-networkd / ifupdown persistence.** Ephemeral binds via `ip addr add` only. UI warns operators to add IPs to their provider-level network config (Hetzner, Vultr, netplan, `/etc/network/interfaces.d/`) for reboot persistence. Owning distro-level network config is a footgun class we do not open in v1.
- Cloud provider API integration (Hetzner/Vultr floating IPs auto-provision).
- IP reputation / warming / blacklist checks.
- IPv6-only domains (every domain keeps an implicit IPv4 listener).
- Multiple IPv4s or multiple IPv6s per domain.
- NAT/CGNAT IP pools (only routable, interface-bound IPs).

## Architecture decisions (fold into ADR-0048)

1. **`managed_ips` is the pool; `server_settings` is authoritative for defaults.** The primary `public_ipv4`/`public_ipv6` stay in `server_settings`. On migration, they are copied into `managed_ips` as seed rows with `is_default=true`. Subsequent changes to the primary in server_settings reconcile into managed_ips; the default row in managed_ips cannot be deleted (409 until another IP is promoted to default or server_settings changes).
2. **FK on `domains.listen_ipv4_id` / `listen_ipv6_id` — nullable, `ON DELETE RESTRICT`.** NULL means "use server default". Delete-IP-in-use returns 409 with the list of affected domains; admin must reassign first.
3. **Agent command `ip.list` / `ip.bind` / `ip.unbind`.** `ip.list` parses `ip -j addr show` (JSON). `ip.bind` validates the address isn't already bound to any interface, then runs `ip addr add <addr>/<prefix> dev <iface>`. `ip.unbind` runs `ip addr del` — idempotent (already-removed returns 0). **No netplan writes.**
4. **IP family is derived, not stored as an enum-on-the-wire.** We store `family` in the DB for indexed lookup but derive at API boundary from the address string; never trust a client-sent family.
5. **Interface selection is automatic in v1.** Agent picks the interface that owns the server's primary `public_ipv4` (for IPv4) or primary `public_ipv6` (for IPv6). No admin-selectable interface dropdown — reduces footgun surface. If the primary isn't bound to any interface, error out loudly.
6. **Reconciler re-bootstraps DNS zone records when a domain's IP binding changes.** `BootstrapRecords` becomes idempotent-upsert (it already nearly is); reconciler calls it every pass for every enabled domain. Today it runs once at zone create; the contract change is that `@` and `mail` A/AAAA content may change between passes when a domain rebinds.
7. **Atomic reassignment = single DB tx + single reconcile pass.** Admin/user PATCH of `listen_ipv4_id`/`listen_ipv6_id` is already one request, one row. Multi-domain reassignment (e.g., admin drains an IP to reassign it) is done through individual PATCHes; reconciler's periodic pass already converges both in the same ReconcileAll cycle (no partial-state window visible to end users since nginx + DNS both update together; cert paths are unaffected).

## Risk register

| ID | Risk | Severity | Mitigation |
|----|------|----------|---|
| R1 | `ip.bind` on wrong interface isolates the box | **CRITICAL** | Auto-select the interface owning the primary public IP; refuse if primary isn't bound; never touch the default-route interface's primary address. Tests exercise "interface ambiguity" (multiple NICs each with their own /24). |
| R2 | Ephemeral bind lost on reboot → nginx fails to start because `listen <ip>:80` refers to a missing IP | HIGH | On agent start, `ip.list` check runs; any `managed_ips.is_bound=true AND NOT on-interface` is re-bound. Reconciler runs this check every pass. If re-bind fails (IP conflict), mark the IP as `degraded` and alert in UI. |
| R3 | Delete IP while domain references it | HIGH | `ON DELETE RESTRICT` + API returns 409 with affected domains. No cascading silent domain bind-to-NULL. |
| R4 | DNS `@` and `mail` records lag nginx because resolvers cache A records for the full TTL window (default 3600s per M4), and client-side stubs (Windows DNS Client, browsers) can cache beyond authoritative TTL | **HIGH** | (a) DNS reconcile runs FIRST in each ReconcileAll pass (line 320, before agent RPC at 339) so the zone is canonical before nginx flips. PowerDNS serves from DB directly — no zone-file stale window at the authoritative side. (b) UI `IP picker` shows the domain's current zone TTL next to the save button: *"DNS caches may serve old records for up to N seconds after save."* (c) Runbook documents the "TTL dance": lower `@`/`mail` A/AAAA TTL to 60s ≥ (current-TTL)s in advance, wait, do the rebind, raise TTL back. (d) Out of scope for v1: auto-TTL-lowering on pending rebinds (revisit in M24.1 if operators hit this often). |
| R5 | nginx reload fails because `listen <ip>:443` conflicts with another vhost on same ip+port | MEDIUM | Agent validates: before accepting a bind, check no other vhost on the target host already listens on `<ip>:80` or `<ip>:443`. Combined with per-domain one-IP-per-family constraint, prevents overlap. |
| R6 | Installer fresh-boot ordering: agent starts before a secondary IP is back | MEDIUM | Agent's "rebind on start" loop tolerates "address already in use" and "no such device" — it retries twice at 2s intervals then continues; reconciler picks up the rest. |
| R7 | User can bind their domain to an IP the admin hasn't approved | HIGH | User PATCH endpoint restricts `listen_ipv*_id` to `managed_ips` rows with `is_user_selectable=true` (new column; default false for the seeded server primary). |
| R8 | Rate-limit zone declarations (nginx.conf) key on the server-wide map_domain — per-IP listen directives don't break them | LOW | Verified: rate-limit zones are declared in nginx.conf, not per-vhost. Per-vhost just `limit_req` references them. No change needed. |
| R9 | Firewall silently drops traffic to the newly-bound IP (iptables/nftables/ufw/firewalld rule doesn't cover the secondary address) | **HIGH** | jabali does NOT manage the host firewall — same reasoning as netplan. Mitigations: (a) agent runs a post-bind connectivity probe: open a listening socket on the new IP:some-ephemeral-port, attempt TCP connect to it from the same host via the new IP (loopback-equivalent). If the connect fails, flag the IP `degraded=true` with reason `firewall_suspected` and surface in UI. (b) UI banner on `add IP`: *"Ensure your host firewall (iptables/nftables/ufw/firewalld) allows inbound TCP 80 and 443 on this address. jabali does not manage firewall rules."* (c) Runbook has `iptables -I INPUT -d <ip> -p tcp --dport 80 -j ACCEPT` examples for common setups. |
| R10 | Operator expects **Bulwark webmail / Stalwart mail / SFTP / PowerDNS / panel-api** to follow the domain's IP binding; they do not | **HIGH** | Documented scope limit. v1 per-domain bindings affect **nginx HTTP/HTTPS vhosts only**. All other services keep listening on 0.0.0.0/[::] (or their own configured addresses). UI tooltip on picker: *"Applies to HTTP/HTTPS only. Email (SMTP/IMAP/JMAP), SFTP, and DNS continue to use the server's default addresses."* See §"Services affected by IP binding" below for the full table. |

## Services affected by IP binding

Explicit matrix so nobody dispatches a step assuming the wrong scope:

| Service | Listens via | Per-domain bind in v1? | Rationale |
|---|---|---|---|
| **nginx HTTP** (:80) | vhost `listen` directive | **YES** | Primary target of this milestone. Step 6 rewires the template. |
| **nginx HTTPS** (:443) | vhost `listen ... ssl` | **YES** | Same template path as HTTP. Cert selection is SNI so one IP can host multiple certs. |
| **PowerDNS authoritative** (:53) | `local-address` in `pdns.conf` | NO | DNS server is per-host, not per-tenant. Answers for every zone over every bound IPv4/IPv6. Out of scope. |
| **PowerDNS recursor** (127.0.0.53:53) | recursor loopback | NO | Host-internal resolver. Shouldn't be externally reachable anyway (M6.3). |
| **Stalwart SMTP** (:25, :465, :587) | Stalwart `server.listen` config | NO | SMTP auth is by user credential, not domain IP. Per-domain SMTP routing would require running N Stalwart instances or SNI-like ESMTP extension that doesn't exist in practice. Out of scope. |
| **Stalwart IMAP** (:143, :993) | Stalwart `server.listen` | NO | Same: authenticated by user, not domain. Out of scope. |
| **Stalwart JMAP** | Stalwart listen | NO | Same. Out of scope. |
| **Bulwark webmail UI** | served via nginx at `/webmail` on apex vhost | **YES, transitively** | Webmail is HTML served by the vhost nginx proxies. If the domain is bound to a specific IP, `https://<domain>/webmail` naturally follows via the same `listen` directive. JMAP/SMTP backends behind it still use Stalwart (unaffected). |
| **openssh SFTP** (:22) | `sshd_config ListenAddress` | NO | One sshd instance per host. Users authenticate by SSH key regardless of IP. Per-IP SFTP would require multiple sshd drop-ins — out of scope. |
| **panel-api** (:8080 local) | served via nginx at `/jabali-admin`, `/jabali-panel` on primary host | NO | Admin surface is host-wide, not per-tenant. Always on server primary IP. |
| **phpMyAdmin SSO** | served via nginx vhost | **YES, transitively** | Same as Bulwark — proxied under the domain vhost, so `listen` follows. |

Out-of-scope items above are NOT bugs; they are design constraints. If an operator needs per-domain mail routing, the v1 answer is "use a separate mail host". M24.1 could reconsider SMTP IP binding if real demand emerges.

## Plan mutation rules

Any step that hits a CRITICAL risk rule must STOP and report to dispatcher before proceeding. Any step that changes schema on a migration > 000056 must first verify the numbering is unused on `main` (use `ls panel-api/internal/db/migrations/`). No agent may push to main; every step branches off `m24/ip-manager` as `m24/step-N-<slug>`.

---

## Waves

```
Wave A (foundation)     Step 1                              [strongest]
Wave B (backend CRUD)   Step 2 ∥ Step 3 ∥ Step 4            [2+3 parallel; 4 depends on both]
Wave C (domain wiring)  Step 5 → Step 6 → Step 7            [serial — wire-contract sensitive]
Wave D (UI)             Step 8 ∥ Step 9                     [parallel]
Wave E (ship)           Step 10                              [cleanup + E2E + runbook]
```

---

## Step 1 — Schema + `ManagedIP` model + seed (Wave A)

**Dependencies:** none. **Model tier:** strongest. **Branch:** `m24/step-1-schema`.

**Context brief (cold-start):**
The panel-api uses GORM-managed MariaDB migrations at `panel-api/internal/db/migrations/`. The latest migration is `000056_mailbox_sso`. Server-wide IPs live in `server_settings` (table has id=1, columns `public_ipv4 VARCHAR(45)`, `public_ipv6 VARCHAR(45)` — `panel-api/internal/db/migrations/000012_create_server_settings.up.sql`). We're adding a pool table `managed_ips` plus two nullable FK columns on `domains`.

**Tasks:**
1. **Dispatcher freeze check.** Before creating files, dispatcher runs `ls panel-api/internal/db/migrations/ | tail -5` on the **current `main` tip** and confirms the highest existing number. If another branch has landed a migration between plan draft and Wave A dispatch, renumber `000057`/`000058` on the spot before agents start (fold F-C-3 from review).
2. Create `panel-api/internal/db/migrations/000057_create_managed_ips.up.sql`:
   ```sql
   CREATE TABLE managed_ips (
     id             BIGINT UNSIGNED NOT NULL AUTO_INCREMENT PRIMARY KEY,
     address        VARCHAR(45)  NOT NULL,
     family         ENUM('ipv4','ipv6') NOT NULL,
     label          VARCHAR(120) NOT NULL DEFAULT '',
     is_default     BOOLEAN      NOT NULL DEFAULT FALSE,
     is_bound       BOOLEAN      NOT NULL DEFAULT FALSE,  -- jabali-managed kernel binding
     is_user_selectable BOOLEAN  NOT NULL DEFAULT FALSE,  -- exposed in user shell
     degraded       BOOLEAN      NOT NULL DEFAULT FALSE,  -- re-bind failed, operator intervention needed
     created_at     DATETIME     NOT NULL DEFAULT CURRENT_TIMESTAMP,
     updated_at     DATETIME     NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
     UNIQUE KEY uq_managed_ips_address (address),
     KEY idx_managed_ips_family (family)
   ) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
   ```
   NOTE: `degraded` is included HERE (not in a later Step-4 migration) to keep every schema change in one atomic wave — per review finding F-H-5.

   Plus a **seed block** at the end — fail-loud on missing primary (per review finding F-C-1):
   ```sql
   -- Fail the migration if server_settings hasn't been populated yet.
   -- install.sh requires the operator to configure public_ipv4 before
   -- panel-api ever starts migrations, so hitting this branch indicates
   -- a broken bootstrap that must be fixed before M24 can function.
   SET @v4 := (SELECT public_ipv4 FROM server_settings WHERE id = 1);
   SET @v6 := (SELECT public_ipv6 FROM server_settings WHERE id = 1);
   SET @err := IF(@v4 IS NULL OR @v4 = '',
                  'M24 bootstrap: server_settings.public_ipv4 is empty; configure it before running this migration',
                  NULL);
   -- Forces migration failure when @err is non-null (MariaDB has no RAISE EXCEPTION).
   SET @fail := IF(@err IS NOT NULL, (SELECT CONCAT('ERROR:', @err) FROM nonexistent_table_m24_assert), NULL);

   INSERT IGNORE INTO managed_ips (address, family, label, is_default, is_bound, is_user_selectable)
     VALUES (@v4, 'ipv4', 'server primary (v4)', TRUE, FALSE, FALSE);
   INSERT IGNORE INTO managed_ips (address, family, label, is_default, is_bound, is_user_selectable)
     SELECT @v6, 'ipv6', 'server primary (v6)', TRUE, FALSE, FALSE WHERE @v6 IS NOT NULL AND @v6 <> '';
   ```
   (IPv6 seed is conditional because IPv6 is genuinely optional in M3; IPv4 is not.)
2. Create `000057_create_managed_ips.down.sql` — `DROP TABLE managed_ips;`.
3. Create `000058_add_listen_ip_ids_to_domains.up.sql`:
   ```sql
   ALTER TABLE domains
     ADD COLUMN listen_ipv4_id BIGINT UNSIGNED NULL,
     ADD COLUMN listen_ipv6_id BIGINT UNSIGNED NULL,
     ADD CONSTRAINT fk_domains_listen_ipv4 FOREIGN KEY (listen_ipv4_id) REFERENCES managed_ips(id) ON DELETE RESTRICT,
     ADD CONSTRAINT fk_domains_listen_ipv6 FOREIGN KEY (listen_ipv6_id) REFERENCES managed_ips(id) ON DELETE RESTRICT;
   ```
4. Down migration drops the constraints + columns.
5. Add `panel-api/internal/models/managed_ip.go` — `ManagedIP` struct with GORM tags.
6. Extend `panel-api/internal/models/domain.go` — add `ListenIPv4ID *uint64`, `ListenIPv6ID *uint64` (pointers for nullable).
7. Add `panel-api/internal/repository/managed_ip_repository.go` — CRUD + `FindByAddress`, `FindUnbound`, `CountDomainsUsingIP(id)`.
8. Unit tests: model validation (family matches address); repo tests against testcontainers MariaDB (already used by other repos).

**Exit criteria:**
- `go test ./panel-api/internal/models/... ./panel-api/internal/repository/...` passes.
- Fresh install of jabali with a configured `public_ipv4` ends with one row in `managed_ips`, `is_default=TRUE`, `is_bound=FALSE`.
- Re-running migrations on an existing DB preserves the seeded row (INSERT IGNORE).

**Verification commands:**
```bash
cd panel-api && go test ./internal/models/... ./internal/repository/... -run 'ManagedIP|Domain_ListenIP'
./install.sh --debug | tee /tmp/install.log && mariadb -u root jabali_panel -e "SELECT * FROM managed_ips;"
```

**Rollback:** `migrate -database mysql://... -path panel-api/internal/db/migrations down 2` then revert the step commit.

---

## Step 2 — Admin IP CRUD API (Wave B — parallel with Step 3)

**Dependencies:** Step 1. **Model tier:** default. **Branch:** `m24/step-2-ip-api`.

**Context brief:**
panel-api routes live in `panel-api/internal/api/`. Admin routes register under `/api/v1/admin/*` (see `server_settings.go:37` for the pattern: `adminGroup.GET("/settings", h.GetSettings)`). All admin routes require `RequireKratosSession` + `RequireAdmin`.

**Tasks:**
1. Create `panel-api/internal/api/ips.go` with:
   - `GET /admin/ips` — list (no pagination needed, IP pool is small; 100-row cap).
   - `POST /admin/ips` — body `{address, label, is_user_selectable}` — family derived server-side; returns 409 if address already in pool.
   - `PATCH /admin/ips/:id` — body may include `label`, `is_user_selectable`, `is_default` (last one: only allowed per-family; promoting a new default demotes the old).
   - `DELETE /admin/ips/:id` — 409 if `count_domains_using > 0` (returns list); 409 if `is_default=TRUE`; else row is deleted AND if `is_bound=TRUE` the controller calls agent `ip.unbind` (agent command lands in Step 3 — until then, skip the unbind call behind a feature flag `M24_AGENT_IP_COMMANDS=1`).
2. Address validator: parse via `net.ParseIP`; reject if `IsLoopback() || IsLinkLocalUnicast() || IsMulticast() || IsUnspecified()` or if family doesn't match `To4()` result.
3. Handler unit tests with mocked repo; table-driven input validation; 409 scenarios covered.

**Exit criteria:** `go test ./panel-api/internal/api/...` passes; `curl -sS http://localhost:8080/api/v1/admin/ips` round-trip works against a live panel.

**Rollback:** revert the commit; feature flag off in config means routes 404.

---

## Step 3 — Agent `ip.list` / `ip.bind` / `ip.unbind` (Wave B — parallel with Step 2)

**Dependencies:** Step 1. **Model tier:** strongest (R1 is CRITICAL). **Branch:** `m24/step-3-agent-ip`.

**Context brief:**
Agent commands live in `panel-agent/internal/commands/`. Each command is a file registered in `panel-agent/internal/commands/registry.go`. Commands take a JSON payload and return JSON. Example: `domain_create.go`. Agent talks to the kernel via exec of `ip` (already used implicitly — `ip.addr` via dhcp probe in install.sh; not yet in agent binary).

**Tasks:**
1. `panel-agent/internal/commands/ip_list.go`: exec `ip -j addr show`, parse JSON output (stdlib-only — the structure from iproute2 is `[{ifname, ifindex, addr_info: [{local, family, prefixlen, ...}]}]`), return `[{address, family, prefixlen, interface, scope}]`.
2. `panel-agent/internal/commands/ip_bind.go`:
   - Payload: `{address, prefixlen?}` — `prefixlen` is caller-supplied and optional (per review F-C-2). Defaults: `/32` for IPv4, `/128` for IPv6. Validate range: 1-32 for IPv4, 1-128 for IPv6.
   - Preflight: call `ip.list` internally (see task 1). If `ip.list` returns a malformed JSON error, propagate the error with exit code 1 and a structured `{error: "failed to parse ip addr list: <reason>"}` — callers must retry, not cache-busted (per review F-M-1).
   - Refuse if address already present on any interface.
   - Derive target interface: find the interface that owns the server's primary `public_ipv4` (from agent config) for IPv4 binds; primary `public_ipv6` for IPv6. If the primary isn't bound anywhere, return a hard error — operator must fix networking first.
   - Exec `ip addr add <addr>/<prefix> dev <iface>`. Stderr on non-zero exit surfaces to caller as structured error.
   - **Never touch default-route interface's primary address.** Reject if `<addr>` equals the primary.
3. `panel-agent/internal/commands/ip_unbind.go`: exec `ip addr del <addr>/<prefix> dev <iface>`. Idempotent treatment (per review F-L-4): exit 0 AND "Cannot assign requested address" (ENOADDR, exit 2) AND "Cannot find device" both map to success; any other non-zero exit propagates as error.
4. **Post-bind connectivity probe** (mitigates R9 firewall): after `ip addr add` succeeds, agent opens a transient TCP listener on `<new-ip>:0` (kernel-assigned ephemeral port), then performs a local TCP connect to `<new-ip>:<that-port>` with a 500ms timeout. If the connect fails, probe returns `{bound: true, reachable: false, suspected_cause: "firewall or ip_forward"}` — panel-api sets `degraded=true` on the row AND returns 201 (not 502) with `warnings: ["connectivity probe failed; verify host firewall allows inbound to this address"]`. Admin can still proceed to bind domains but is warned. Listener + connect use the standard library's `net` package — no extra dependencies. Probe is purely informational; it doesn't unbind on failure.
4. Integration test (`ip_bind_test.go`) with a dummy interface:
   ```bash
   ip link add dummy-jabali type dummy
   ip addr add 198.51.100.1/32 dev dummy-jabali  # simulate primary
   # test runs ip_bind({address:"198.51.100.2"}), asserts 198.51.100.2 present on dummy-jabali
   ```
   Tests skip when not run as root or `NET_ADMIN` capability unavailable (build tag `integration`).
5. Register in `registry.go`.

**Exit criteria:** commands round-trip against the dummy interface test; unit tests cover payload validation; agent binary builds.

**Verification commands:**
```bash
cd panel-agent && go test -tags integration ./internal/commands/... -run IPBind
```

**Rollback:** `ip.bind`/`ip.unbind` calls from panel-api go behind `M24_AGENT_IP_COMMANDS` flag; flipping flag off means agent never receives the command and CRUD stays DB-only.

---

## Step 4 — panel-api ↔ agent IP binding wire-up (Wave B — depends on 2+3)

**Dependencies:** Steps 2 and 3 both merged to `m24/ip-manager`. **Model tier:** default. **Branch:** `m24/step-4-wire-bind`.

**Context brief:**
panel-api calls agent via `agentClient` (see `panel-api/internal/agent/client.go`). Pattern: `client.Execute(ctx, "command.name", payload, &response)`.

**Tasks:**
1. On `POST /admin/ips`: after DB insert, if the address isn't in `ip.list` output, call `ip.bind`. If `ip.bind` succeeds, mark `is_bound=TRUE` in DB. If it fails, delete the DB row and return 502. If the address IS already in `ip.list`, leave `is_bound=FALSE` (admin pre-bound externally) and return 201.
2. On `DELETE /admin/ips/:id`: if `is_bound=TRUE`, call `ip.unbind` first; then delete. Partial-failure handling: if unbind fails, DO NOT delete the DB row — return 502 with a clear error "kernel binding removal failed: X; retry or unbind manually with `ip addr del`".
3. **Internal panel-api endpoint** `GET /internal/agent/managed-ips` (called by agent, not exposed to SPA) — returns `{ips: [{address, family, prefixlen, is_bound, degraded}]}` for all bound rows. No Kratos auth; gated on localhost-only middleware (`RequireLocalhost`) since the agent talks to panel-api over `127.0.0.1`. This closes review finding F-M-3: the agent has no DB access, so it needs an endpoint to fetch the desired-state set at startup. Fold this into Step 2's API work so it lands together.
4. On agent start, add a "reconcile bound IPs" pass: call the internal endpoint → compare to `ip.list` output. Any bound-in-DB-but-not-on-interface IPs are re-bound with exponential backoff (2s, 4s, 8s); failure after 3 attempts calls panel-api's `PATCH /internal/agent/managed-ips/:id {degraded: true}` to mark the row. Agent startup tolerates panel-api-not-ready with its own backoff (0.5s, 1s, 2s, 4s, capped 30s) — systemd `After=panel-api.service` is NOT sufficient because panel-api takes a moment to bind its socket after unit-active.
4. Integration test: POST IP, verify kernel has it + `is_bound=TRUE`; DELETE, verify kernel lost it.

**Exit criteria:** `curl -X POST /admin/ips -d '{"address":"198.51.100.42"}'` returns 201 AND `ip -j addr show` lists it AND the DB row has `is_bound=TRUE`.

**Rollback:** flip `M24_AGENT_IP_COMMANDS=0` — CRUD silently falls back to DB-only, `is_bound` always `FALSE`.

---

## Step 5 — Domain model `listen_ipv*_id` fields surfaced in API (Wave C)

**Dependencies:** Step 1. **Model tier:** default. **Branch:** `m24/step-5-domain-field`.

**Context brief:**
Domain API at `panel-api/internal/api/domains.go`. Admin PATCH accepts JSON body with partial domain fields. User PATCH at `panel-api/internal/api/user_domains.go` is more restrictive.

**Tasks:**
1. Admin PATCH `/api/v1/admin/domains/:id`: accept `listen_ipv4_id` and `listen_ipv6_id` in body. Validate FK: the referenced `managed_ips` row exists AND family matches the field. Null explicitly un-binds. Return updated domain.
2. User PATCH `/api/v1/user/domains/:id`: same fields, BUT restrict to `managed_ips.is_user_selectable=TRUE`. Return 403 + clear message if user tries to pick a non-user-selectable IP.
3. Expose the fields in `GET /admin/domains/:id` + `GET /user/domains/:id` responses — as `listen_ipv4: {id, address}` + `listen_ipv6: {id, address}` (denormalized for UI convenience). LEFT JOIN against `managed_ips`; if the IP row is somehow missing (shouldn't happen given FK RESTRICT but defensive), fall back to the server primary's address — never emit a null address. Per review F-H-2.
4. **Add `GET /user/ips`** endpoint (per review F-L-2) — returns only rows with `is_user_selectable=TRUE`, scoped to the authenticated user (shape mirrors `/admin/ips` but without the admin-only fields). Step 9's UI consumes this.
5. Handler tests for both shells + both IP families.
6. **Wire-contract verification** — before opening this step's PR, grep `panel-api/internal/api/packages.go` + `server_settings.go` for the canonical response envelope (`{data, total, page, page_size}` vs `{items, total}`) and match it. Per memory `feedback_verify_wire_contract.md`.

**Exit criteria:** `curl -X PATCH /api/v1/admin/domains/1 -d '{"listen_ipv4_id": 2}'` succeeds and subsequent GET reflects it.

**Rollback:** revert the commit; NULL FKs are the default, everything keeps working unchanged.

---

## Step 6 — nginx vhost template emits per-domain `listen <ip>:<port>` (Wave C)

**Dependencies:** Step 5. **Model tier:** default. **Branch:** `m24/step-6-nginx-listen`.

**Context brief:**
`panel-agent/internal/commands/domain_create.go:66-128` holds the inline `vhostTemplate` constant. `vhostData` struct (line 21-52) is the template context. panel-api resolves FK IDs to address strings before calling `domain.create` (see `reconciler.go:339`-ish). Writes are content-hash gated inside `writeVhost` — same bytes = no nginx reload.

**Tasks:**
1. Extend `domainCreateParams` struct with `ListenIPv4 string` + `ListenIPv6 string` (empty string = "all interfaces", current behavior).
2. Extend `vhostTemplate` — use **explicit if/else** (per review F-H-3; the earlier draft of `listen {{if .ListenIPv6}}[{{.ListenIPv6}}]:{{end}}80;` renders the invalid `listen :80;` when empty):
   - HTTP block:
     ```nginx
     {{if .ListenIPv4}}listen {{.ListenIPv4}}:80;{{else}}listen 80;{{end}}
     {{if .ListenIPv6}}listen [{{.ListenIPv6}}]:80;{{else}}listen [::]:80;{{end}}
     ```
   - HTTPS block mirror: `listen ... ssl http2;` variants.
   - Template unit tests MUST cover all four combinations (no-IP, ipv4-only, ipv6-only, both) and assert the rendered config parses cleanly via `nginx -t -c <rendered>`.
3. panel-api side: when preparing `domain.create` payload in `reconciler.go`, resolve `domain.ListenIPv4ID` via `managedIPRepo.FindByID` and pass the address string (or empty). Same for IPv6.
4. Template unit tests (`vhostTemplate_test.go`) for four combinations: no-IP, ipv4-only, ipv6-only, both.
5. Integration test: bind a dummy IP, create a domain, grep the rendered `/etc/nginx/sites-available/<domain>.conf` for exact `listen <ip>:80` string.

**Exit criteria:** domain with `listen_ipv4_id` set to a non-default IP generates nginx config listening on that specific IP; `curl --resolve host:80:<other-ip> http://host/` returns 404 (connection refused) while `curl --resolve host:80:<bound-ip> http://host/` works.

**Rollback:** revert; empty `ListenIPv4` in the template falls back to `listen 80` (catch-all) which is the current behavior.

---

## Step 7 — DNS zone A/AAAA records track domain IP binding (Wave C)

**Dependencies:** Step 6. **Model tier:** default. **Branch:** `m24/step-7-dns-a-records`.

**Context brief:**
`panel-api/internal/dnscompile/bootstrap.go:35-85` — `BootstrapRecords(zoneID, zoneName, srv, idNew)` creates `@`/`mail`/`www` records from `srv.PublicIPv4/IPv6`. Called from `reconcileDNSZone` (reconciler.go:320-ish) **once per zone create** — never re-run on domain change today.

**Tasks:**
1. Change `BootstrapRecords` signature to accept the domain (or the pair of effective IPs): `BootstrapRecords(zone, domain, serverPrimary, repo)`. Effective IPv4 = domain.ListenIPv4.Address ?? serverPrimary.IPv4. Same for v6.
2. Make `BootstrapRecords` **idempotent upsert**: for each of `@`/`mail`/`www`, UPDATE if exists and content differs (AND `managed='system'`), INSERT if missing. Never overwrite rows where `managed='user'` — user-edited records are sacrosanct. Use `managed` column added in migration `000055_dns_records_add_managed_by`.
3. Reconciler change: call `BootstrapRecords` for every enabled domain every pass (not just at zone create). Content-hash-equivalent work (no changes) = no writes.
4. Unit tests: domain without binding → records use server primary; domain with IPv4 binding → `@` A uses bound IP; user-edited record untouched.

**Exit criteria:** binding a domain to a non-default IP updates the zone's `@` A record content to that IP in ≤ one reconcile cycle (60s default); `dig @127.0.0.1 <domain> A` reflects the new IP.

**Rollback:** revert. A records re-pin to server primary on next reconcile pass.

---

## Step 8 — Admin UI `/jabali-admin/ips` list/create/edit (Wave D — parallel with 9)

**Dependencies:** Step 2 (API landed). **Model tier:** default. **Branch:** `m24/step-8-admin-ui`.

**Context brief:**
Post-M21, panel-ui uses native AntD + TanStack Query + `useTableURL`/`useListQuery`/`useCreate|Update|DeleteMutation` hooks (see `panel-ui/src/hooks/useQueries.ts` and reference page `panel-ui/src/shells/admin/packages/PackageList.tsx`). Routes registered in `panel-ui/src/App.tsx:121+`. Nav items in `panel-ui/src/nav.ts:52+`.

**Tasks:**
1. `panel-ui/src/shells/admin/ips/AdminIPList.tsx`: AntD `<Table>` with columns `address`, `family`, `label`, `is_default` (as tag), `is_bound` (as tag), `is_user_selectable` (as tag), `used_by_domains` (count), action buttons (edit, delete). Delete confirm Modal shows affected-domains list on 409.
2. `panel-ui/src/shells/admin/ips/AdminIPCreate.tsx`: form with `address` (required, regex-validated for IPv4/IPv6), `label`, `is_user_selectable` toggle. Two warning banners above the form:
   - **Persistence** (netplan out-of-scope): *"jabali binds this IP ephemerally via `ip addr add`. For the binding to survive reboot, add the address via your provider's network configuration (Hetzner robot, Vultr additional IP, netplan, or `/etc/network/interfaces.d/`)."*
   - **Firewall** (R9): *"After adding, ensure your host firewall allows inbound TCP 80 and 443 to this address. Run `iptables -L INPUT -v -n | grep <ip>` (or equivalent for nft/ufw/firewalld) to verify."*
   On submit, if the API response includes `warnings` (e.g., post-bind probe failed), show a yellow AntD `<Alert type="warning">` linking to the firewall-preflight runbook section. Don't block navigation — the IP is bound; the admin just needs to investigate.
3. `panel-ui/src/shells/admin/ips/AdminIPEdit.tsx`: `label`, `is_user_selectable`, `is_default` (per-family promotion only). Address is read-only (delete + re-add to change).
4. Register routes in `App.tsx` nested under admin shell, plus nav entry in `nav.ts` (`{ key: "ips", label: "IP Addresses", path: "/jabali-admin/ips", icon: <GlobalOutlined /> }`).
5. Vitest unit tests for each component (form validation, 409 handling).

**Exit criteria:** manual walkthrough: add, label-edit, promote-default, delete flows all work. 409 on delete-in-use shows affected domains inline.

---

## Step 9 — Per-domain IP picker on domain edit forms (Wave D — parallel with 8)

**Dependencies:** Step 5 (domain API landed). **Model tier:** default. **Branch:** `m24/step-9-domain-ip-picker`.

**Context brief:**
Domain edit pages at `panel-ui/src/shells/admin/domains/AdminDomainEdit.tsx` and `panel-ui/src/shells/user/domains/UserDomainEdit.tsx`. They already use AntD `<Form>` + `useOneQuery` for load + `useUpdateMutation` for save.

**Tasks:**
1. Add two `<Select>` fields to both pages:
   - "Listen IPv4" — populated from `GET /admin/ips?family=ipv4` (admin) or `GET /user/ips?family=ipv4` (user — new endpoint that returns only `is_user_selectable=TRUE` rows). Placeholder option `Use server default (<server_primary_v4>)` = submits `listen_ipv4_id: null`.
   - "Listen IPv6" — same, for ipv6.
2. **Scope-limiter tooltip** on the picker label (per R10): *"Applies to this domain's HTTP/HTTPS vhost only. Email (SMTP/IMAP), SFTP, and DNS continue to use the server's default addresses."*
3. **TTL-aware save warning** (per R4): on save, fetch the current zone's `@` A record TTL. If TTL > 300s, show a modal: *"The `@` A record for this zone has TTL {ttl}s. External DNS resolvers may serve the old IP for up to that long. Proceed, or lower TTL first?"* Buttons: `Proceed anyway` / `Open DNS tab to lower TTL`.
4. **Degraded IP warning**: if the selected IP has `degraded=true` (from post-bind probe or re-bind failure), the option shows a red dot + tooltip *"This IP has been flagged as unreachable. Check host firewall rules and network config."* Don't prevent selection — operator may know something we don't.
3. On-save handler passes `null` when user picks the default placeholder.
4. Vitest unit tests.

**Exit criteria:** admin/user can change a domain's IP binding through the UI and reload to see the picker reflect the stored choice.

---

## Step 10 — E2E test + runbook + ADR + BLUEPRINT update (Wave E)

**Dependencies:** all prior steps merged to `m24/ip-manager`. **Model tier:** default. **Branch:** `m24/step-10-ship`.

**Context brief:**
E2E Playwright specs live at `panel-ui/tests/e2e/`. Runbooks at `plans/m*-runbook.md`. ADRs at `docs/adr/`. Changelog at bottom of `docs/BLUEPRINT.md`.

**Tasks:**
1. `panel-ui/tests/e2e/ip-manager.spec.ts`: full flow against a live VM with a pre-existing secondary IP on the test interface:
   - admin logs in, opens `/jabali-admin/ips`, adds a second IP, picks it as the listen-ipv4 for an existing domain.
   - After ≤ 2 reconcile cycles, asserts `curl --resolve <domain>:80:<bound-ip> http://<domain>/` returns 200 AND `curl --resolve <domain>:80:<other-ip>` returns connection-refused.
   - Asserts `dig @<ns> <domain> A` returns the bound IP.
   - Unbinds (picker → default), asserts both IPs now respond on :80 again.
2. `plans/m24-ip-manager-runbook.md`: day-2 ops — how to manually bind an IP, how to recover from a degraded binding, how to persist IPs via netplan/systemd-networkd/ifupdown (link to distro docs; explicitly stated as not-our-job).
3. `docs/adr/0048-m24-ip-address-manager.md`: captures the 7 architecture decisions from this blueprint's "Architecture decisions" section + the netplan-out-of-scope rationale.
4. Update `docs/BLUEPRINT.md`:
   - Add `### 4.X IP Address Manager (M24)` section under 4.9 Reconciler area.
   - Add `### M24: IP Address Manager (SHIPPED)` milestone under M23.
   - Add changelog row `| M24: IP Address Manager | <date> | <final commit SHA> |`.
5. Update memory: new entry `project_m24_ip_manager.md` linked from `MEMORY.md`.

**Exit criteria:** E2E passes on the test VM end-to-end; merge `m24/ip-manager` → `main` is a clean fast-forward; CI green.

---

## Dispatch guidance

- **Wave A (Step 1)** — dispatcher executes inline (schema is small, contract-sensitive, and parallel agents have shipped invalid migration numbers before — see memory `feedback_merge_audit_migrations.md`).
- **Wave B (Steps 2/3/4)** — Steps 2 and 3 dispatch to two agents in parallel. Step 4 dispatches AFTER both are merged to `m24/ip-manager`. Agents commit to `m24/step-N-<slug>` feature branches — **never to `m24/ip-manager` directly**. Dispatcher FF-merges branches into `m24/ip-manager` after review.
- **Wave C (Steps 5/6/7)** — **serial** because they form the wire contract (domain field → nginx listen → DNS records). Each depends on the prior being on `m24/ip-manager`. Do NOT parallelize — see memory `feedback_subagent_contract_drift.md`.
- **Wave D (Steps 8/9)** — parallel. UI agents branch off the post-Wave-C `m24/ip-manager`.
- **Wave E (Step 10)** — dispatcher inline or single agent.

## Verification gate between every wave

Before merging a wave's branches into `m24/ip-manager`:
1. `go test ./...` in both panel-api and panel-agent.
2. `npm run build` + `npm run test` in panel-ui.
3. `go vet ./...` + `golangci-lint run` — must be clean.
4. Fresh install on a clean VM (192.168.100.13): `./install.sh --debug` must succeed end-to-end.
5. `mcp__gitnexus__detect_changes` to confirm the change set matches the step's declared scope.

## Open questions — decisions locked-in post-review

All open questions from the draft have been resolved by the adversarial review (2026-04-22):

1. **Seed when `public_ipv4` is empty** → **Fail the migration.** Folded into Step 1 (task 2, seed block with `nonexistent_table_m24_assert` trick). Rationale: install.sh requires IPv4 input; reaching migration with empty `public_ipv4` means bootstrap is already broken.
2. **User endpoint `GET /user/ips`** → **Folded into Step 5** as task 4.
3. **`degraded` column** → **Folded into Step 1 migration 000057** (not a separate 000059). Avoids cross-wave migration numbering races.
4. **Migration number freeze** → **Dispatcher verifies on `main` tip** at Wave A start; renumbers on the spot if another branch landed migrations between draft and dispatch.
5. **Rate-limit zone interaction** → no change needed (zones are global, referenced by `$binary_remote_addr`).
6. **Agent startup sync** → new internal endpoint `GET /internal/agent/managed-ips` (localhost-only) added to Step 2/4.

## Unresolved runbook items (for Step 10)

Deferred from the adversarial review as non-blocking runbook work (Step 10 must explicitly address each):

- **Runbook-1 (review F-H-1):** Orphan kernel-bound IPs after failed `ip.unbind` — add a "recovery" section to the runbook with the `ip addr del` one-liner and a note that operators can re-claim via the UI if the address shows up in `ip.list` but not `managed_ips`.
- **Runbook-2 (review F-H-4):** User-edited `@`/`mail` A records skip bootstrap-driven rewrites. Runbook clarifies that when a domain is bound to a non-default IP, the DNS records tab will show a warning: "This record is managed by IP binding. Manual edits will not take effect until the binding is removed."
- **Runbook-3 (review F-M-2, covered by R10 in risk register + Services table):** Webmail (Bulwark), SFTP (M12), mail (Stalwart), PowerDNS, panel-api do NOT follow per-domain IP bindings in v1 — they continue to listen on 0.0.0.0/[::] (or their configured addresses). Runbook repeats the Services-affected table with operational examples: "Customer X asked for an IP-isolated domain. Their HTTP/HTTPS are on 1.2.3.5, but their SMTP still receives on 1.2.3.4. This is expected." Include a deferred-work pointer to M24.1 if ever pursued.
- **Runbook-4 (DNS TTL dance, covered by R4):** Step-by-step for operators who want fast cutover:
  1. Before rebind: admin opens the target domain's DNS tab, temporarily lowers `@` A and AAAA TTL to 60s, saves.
  2. Wait ≥ (current-global-TTL) seconds so external resolvers pick up the 60s value. Default M4 TTL is 3600s → wait 1 hour.
  3. Change the IP binding in the picker. A record content updates in ≤ 60s (next reconcile pass).
  4. External resolvers refresh within 60s of their cache expiry.
  5. After 15 minutes of observed convergence, raise TTL back to 3600s.
  Runbook warns: some client-side stubs (Windows DNS Client default 15min, Chrome pre-fetch) cache beyond authoritative TTL. Full convergence can still take ~30 minutes on stragglers.
- **Runbook-5 (new, per R9 firewall):** Firewall preflight checklist. Before adding an IP in the UI, operator should run `iptables -L INPUT -v -n | grep <new-ip>` (or `nft list ruleset` / `ufw status`) to confirm no DROP rule shadows the new address. Runbook provides copy-paste templates for:
  - iptables: `iptables -I INPUT -d <ip> -p tcp -m multiport --dports 80,443 -j ACCEPT`
  - nftables: `nft add rule inet filter input ip daddr <ip> tcp dport {80,443} accept`
  - ufw: `ufw allow proto tcp from any to <ip> port 80,443`
  - firewalld: `firewall-cmd --permanent --add-rich-rule='rule family=ipv4 destination address=<ip> port port=80 protocol=tcp accept'`
  Clarify: jabali's post-bind probe (R9 mitigation) catches most firewall drops BEFORE claiming success, but admin-misconfigured rules that are stateful (e.g., conntrack-based) may still let the probe succeed and drop real external traffic. Preflight remains operator responsibility.

## Post-review status

**Review date:** 2026-04-22 (opus/architect adversarial pass).
**Review verdict:** GO-WITH-FIXES → all 3 CRITICAL + 5 HIGH folded in. Plan is now dispatch-ready.
