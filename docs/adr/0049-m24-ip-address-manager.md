# ADR-0049 — M24: IP address manager

Status: **ACCEPTED**
Date: 2026-04-22
Milestone: M24 — IP address manager

## Context

Single-address hosting is the baseline: `server_settings.public_ipv4` /
`public_ipv6` are seeded by `install.sh` and every panel-managed nginx vhost
listens on `0.0.0.0:80` / `[::]:80`. PowerDNS bootstrap records pin every
domain to the same pair of IPs.

This works until an operator needs:

- **Per-customer IP** for SEO isolation, mail-reputation independence, or a
  customer who paid extra for a dedicated address.
- **A second IP block** acquired from the provider (Hetzner failover IP,
  Vultr extra IP, second `/29` subnet) for capacity overflow.
- **HTTPS-isolated subset** where one tenant's TLS fingerprint shouldn't
  leak through SNI on a shared IP.

There is no first-class concept of "IP" in the panel today; each handler
that needs one reads `server_settings.public_ipv4` directly.

## Decision

Introduce a **managed IP pool** as the new source of truth, with the
following architecture decisions (locked in by adversarial review
2026-04-22):

1. **Managed IPs live in a dedicated table** (`managed_ips`), keyed by
   address (UNIQUE). Family is derived server-side via `models.DeriveFamily`
   — the API never trusts a client-supplied family. Migration `000057`
   creates the table; `000058` adds nullable `domains.listen_ipv4_id` /
   `listen_ipv6_id` foreign keys with `ON DELETE RESTRICT`.

2. **Persistence is operator-owned, NOT panel-owned.** jabali binds
   addresses ephemerally via `ip addr add` — it doesn't write netplan,
   `/etc/network/interfaces.d/`, or anything that survives reboot. The
   admin UI surfaces a persistence-warning banner and links to the
   provider-side configuration (Hetzner robot, Vultr panel, netplan).
   Rationale: jabali shouldn't own the host's network stack — that's
   distro/provider territory. Reboot-survival via netplan is a one-time
   admin action, not a per-IP recurring task.

3. **Agent-side rebind is best-effort.** A reconciler pass (`ReconcileManagedIPs`)
   runs every tick: for each row with `is_bound=TRUE`, if the address is
   missing from `ip -j addr show`, call `ip.bind` again. Two failed
   rebinds flip `degraded=TRUE`; the admin UI surfaces the warning.
   Rationale: the kernel binding can vanish across reboots (no netplan)
   or after manual `ip addr del`; the reconciler is the recovery path.

4. **A post-bind connectivity probe** runs on every successful `ip.bind`:
   the agent listens on a random high port on the new address, dials it
   from `127.0.0.1`, and warns on failure. Surfaces firewall
   misconfiguration (UFW rule missing, security group blocking) before
   the operator deploys a domain to that IP. Probe failures yield a
   non-fatal warning in the create response — the IP stays bound;
   the admin sees a yellow banner explaining the next step.

5. **Per-domain binding is fully nullable.** `domains.listen_ipv4_id` /
   `listen_ipv6_id` are NULL by default; NULL means "use the family
   default", i.e. `managed_ips.is_default=TRUE` for that family. The
   server-primary IPs are seeded as the default via migration `000057`.
   This lets the operator swap the server primary (e.g. Vultr migration)
   without re-touching every domain.

6. **`is_user_selectable` gates the user-shell picker.** Default false
   for the seeded server primary; admin opts each additional IP in via
   PATCH `/admin/ips/:id`. Non-admin PATCH of `domains.listen_ipv*_id`
   to a non-selectable IP is rejected with 403. Rationale: the admin
   shouldn't have to give a tenant veto power over which IP an entire
   server uses; the user-pickable subset is curated.

7. **Atomic reassignment via single DB tx + single reconcile pass.**
   The PATCH is one row, one column, one transaction. The reconciler's
   per-domain pass converges nginx (Step 6: `listen <ip>:80`) and DNS
   (Step 7: apex `@` A/AAAA upsert) in the same `ReconcileOne` call.
   No multi-step orchestration, no partial-state window — admin sees
   the change reflected ≤ 60s later.

8. **Out-of-scope services keep their existing bindings.** v1 only
   touches the per-domain HTTP/HTTPS listen + the apex DNS records.
   Stalwart (mail), Bulwark (webmail), SFTP (M12 openssh), PowerDNS,
   panel-api itself, and Roundcube continue listening on `0.0.0.0` /
   `[::]:` (or whatever they were configured for). A future M24.1
   could pursue per-domain mail-IP isolation, but Stalwart's MTA model
   and certificate ownership make this a much larger change.

## Threat model

- **Adoption of operator-bound addresses**: an IP added to the host
  via netplan but never to `managed_ips` stays untouched — the
  reconciler only converges rows with `is_bound=TRUE`. Operator-only
  addresses (e.g. an SSH-management IP) are not silently swallowed.

- **FK on delete**: `domains.listen_ipv*_id` uses `ON DELETE RESTRICT`,
  so MariaDB blocks an IP delete while any domain references it. The
  delete handler short-circuits with 409 + the affected-domains list
  before the DB raises, so admins see a useful error instead of a
  raw FK violation.

- **Family confusion attack**: client sends `listen_ipv4_id` pointing
  at an IPv6 row. Server validates `row.Family == "ipv4"` and rejects
  with 400 — protects the agent from receiving an IPv6 address in a
  context where it would render `listen 2001:db8::1:80;` (invalid).

- **Default downgrade**: PATCH `is_default=false` on the only default
  for a family is rejected with 400 — the operator must promote
  another row first. Without this, a momentary "no default for v4"
  window would have NULL bindings rendering `listen 80;` (correct
  fallback) but the per-pass DNS A reconcile would skip without a
  default to fall back to.

## Consequences

**Positive:**

- Per-customer IP isolation possible without a code change.
- Drop-in compatible with single-IP profiles: the seeded default makes
  every existing domain map to the prior server primary, no migration
  pain.
- Reconciler-driven convergence: the same loop that handles vhost +
  DNS handles IP-binding state, so adding an IP doesn't need a new
  cron or background worker.

**Negative:**

- **Reboot-survival is operator's job** — easy to forget, and a
  surprise on first reboot. The persistence-warning banner mitigates
  but doesn't eliminate the surprise.
- **Mail / webmail / SFTP keep their server-wide bindings** — this
  is documented in the runbook (`plans/m24-ip-manager-runbook.md`)
  but is a footgun for operators expecting full per-domain isolation.
- **Pool capped at 100 (validation soft limit)** — beyond that, the
  list-all path on every reconcile starts costing real DB time. If we
  ever need >100, switch the per-pass denormalization to a per-domain
  FindByID (cached).

## Alternatives considered

- **netplan-write from the agent**: rejected. Distro-specific YAML
  (netplan vs ifupdown vs systemd-networkd vs NetworkManager) and the
  blast radius of a malformed write is taking the host offline. Not
  worth owning.
- **Bind every IP at install time, regardless of pool**: rejected.
  The pool needs to be fluid (add/remove without an install
  rerun); every-IP-at-install pre-supposes the operator pre-declared
  every IP they'd ever use.
- **Use Linux `ip rule` + `ip route` for per-domain source routing**:
  rejected. Adds a kernel-state surface the agent has to reconcile
  perfectly or risk traffic vanishing into the wrong table. The
  per-vhost `listen` directive is a much smaller blast radius.

## References

- Plan: `plans/m24-ip-manager.md`
- Runbook: `plans/m24-ip-manager-runbook.md`
- Migrations: `panel-api/internal/db/migrations/000057_create_managed_ips.up.sql`,
  `000058_add_listen_ip_ids_to_domains.up.sql`
- Agent commands: `panel-agent/internal/commands/ip_{list,bind,unbind}.go`
- Reconciler: `panel-api/internal/reconciler/managed_ips.go`,
  `panel-api/internal/reconciler/reconciler.go` (`convergeApexAddrRecords`,
  `resolveListenIPAddress`)
- API: `panel-api/internal/api/ips.go`, `panel-api/internal/api/domains.go`
  (`resolveListenIPUpdate`, `enrichDomainResponse`)
- UI: `panel-ui/src/shells/admin/ips/`,
  `panel-ui/src/shells/admin/domains/DomainListenIPSection.tsx`
