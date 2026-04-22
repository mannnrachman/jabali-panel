# M24 — IP address manager runbook

Day-2 operations for the M24 managed-IP pool.

See:
- `docs/adr/0049-m24-ip-address-manager.md` for design rationale
- `plans/m24-ip-manager.md` for the original construction plan

---

## Adding an IP to the pool

**Prerequisites (operator-owned, not panel-owned):**

1. **Bind it persistently at the OS layer** so the address survives a
   reboot. Pick the right tool for your distro / provider:

   - Hetzner (with netplan): edit `/etc/netplan/00-installer-config.yaml`,
     add the address under `addresses:`, then `netplan apply`.
   - Vultr / DigitalOcean: add the address via the provider control
     panel, then either let cloud-init reapply or add to netplan
     manually.
   - Bare-metal Debian/Ubuntu: drop a stanza in
     `/etc/network/interfaces.d/jabali-extra-ips`.
   - systemd-networkd hosts: edit the matching `.network` file under
     `/etc/systemd/network/`, then `networkctl reload`.

2. **Open the host firewall** so inbound TCP 80/443 reach the new
   address. Verify with whichever rule engine is in play:

   ```
   iptables -L INPUT -v -n | grep <ip>
   nft list ruleset | grep <ip>
   ufw status | grep <ip>
   firewall-cmd --list-all | grep <ip>
   ```

**Then in the panel:**

3. Go to **Admin → IP Addresses → Add IP**.
4. Enter the address; a label is recommended (provider, purpose, owning
   customer).
5. Tick **User-selectable** if customers should be able to bind their
   own domains to it.
6. Click **Add IP**.

The panel runs `ip addr add <ip>/32 dev <default-route-iface>`
immediately. Within 30s a connectivity probe verifies inbound on the
new address; if it fails you'll see a yellow warning banner with
remediation pointers (almost always firewall) — the IP stays bound
either way.

---

## Removing an IP from the pool

1. **Reassign every domain that uses it** first (the panel won't let
   you delete an IP referenced by `domains.listen_ipv*_id` — you'll
   get a 409 with the affected-domains list).
2. **Promote a different default** if this IP is the family default.
   The panel rejects deletion of a default with no replacement (would
   leave domains with NULL bindings unable to resolve).
3. Click **Delete** on the IP row.

The panel runs `ip addr del <ip>/32 dev <iface>` and removes the row.
The kernel binding persists in your netplan/network config — remove
it there too if you don't want the address coming back on reboot.

### Recovery: orphaned kernel binding after failed unbind

If `ip.unbind` fails (e.g. agent crash mid-call) the row may be gone
from `managed_ips` but the address still on the kernel. Symptom: the
address shows up in `ip -j addr show` but not in `Admin → IP Addresses`.

Recover with:

```
ip addr del <orphan-ip>/32 dev <iface>
```

Or re-add the IP via the panel UI to bring it back under management;
the create handler is idempotent against pre-existing kernel bindings
(the address is recognized as already-bound and the row is created
with `is_bound=TRUE` directly).

---

## Recovering from a degraded binding

`degraded=TRUE` on a row means the reconciler's rebind-on-tick loop
failed. The UI surfaces a red `degraded` tag in the IP list. Likely
causes:

- **Kernel binding lost**: someone ran `ip addr del` outside the
  panel. Reconciler will retry on the next tick (≤ 60s); if the
  rebind succeeds, `degraded` flips back to false automatically.
- **Connectivity probe fails after rebind**: firewall or routing.
  The reconciler can't fix this — operator must reconfigure the host
  firewall, then the next tick will probe-clean and clear the flag.
- **Address conflict**: another host on the L2 segment claimed the
  IP. Linux's ARP-collision detection will return EADDRNOTAVAIL on
  bind. Investigate via `arp-scan` or your provider's IP-management
  tooling.

To force a rebind attempt outside the reconciler tick:

```
# As root on the panel host:
ip addr add <ip>/32 dev <iface>
# Then PATCH /admin/ips/<id> with {"label":"…"} (any field) to bump
# updated_at and re-trigger the connectivity probe on the next tick.
```

---

## Per-domain IP binding

In the admin **Domains → Edit** page, the **Listen IPs** section has
two pickers:

- **Listen IPv4** / **Listen IPv6**: each defaults to "Use server
  default (`<address>`)" — selecting any other entry pins this domain
  to that specific IP.

After **Save listen IPs**, within ≤ 60s:

1. The reconciler regenerates the nginx vhost with explicit
   `listen <ip>:80` / `listen [<ipv6>]:80` directives instead of the
   all-interfaces fallback.
2. The DNS apex `@` A and `@` AAAA records get rewritten to the new
   binding (only if the rows are panel-managed; user-edited rows are
   left alone).
3. `nginx -t` + `systemctl reload nginx` happens automatically.

To verify a binding took effect:

```
# nginx is now listening on the bound IP:
ss -tlnp | grep ':80\|:443' | grep <bound-ip>

# DNS reflects it:
dig @127.0.0.1 <domain> A
dig @127.0.0.1 <domain> AAAA

# HTTP only responds on the bound IP:
curl --resolve <domain>:80:<bound-ip>  http://<domain>/   # 200 OK
curl --resolve <domain>:80:<other-ip>  http://<domain>/   # connection refused
```

---

## What's NOT IP-bound (v1 scope)

The following services keep their server-wide bindings regardless of
per-domain `listen_ipv*_id`. This is **expected** — see ADR-0049
decision 8 for rationale.

| Service        | Listens on                     | Per-domain IP applies? |
|----------------|--------------------------------|------------------------|
| nginx (HTTP)   | per-domain `listen <ip>:80`    | **YES**                |
| nginx (HTTPS)  | per-domain `listen <ip>:443`   | **YES**                |
| PowerDNS       | server primary `:53`           | NO (apex A/AAAA tracks)|
| Stalwart (SMTP)| `0.0.0.0:25, 587, 465`         | NO                     |
| Stalwart (IMAP)| `0.0.0.0:143, 993`             | NO                     |
| Bulwark webmail| `0.0.0.0:8443`                 | NO                     |
| Roundcube      | served from nginx default vhost| NO                     |
| SFTP (openssh) | `0.0.0.0:22`                   | NO                     |
| panel-api      | `0.0.0.0:8443`                 | NO                     |

**Operational example:**
> Customer X asked for an IP-isolated domain. Their HTTP/HTTPS are on
> 1.2.3.5, but their SMTP still receives on 1.2.3.4. This is expected.

If full per-domain isolation (mail, SFTP, etc.) is ever needed, that's
a future M24.1 — Stalwart's MTA model and certificate scoping make it
significantly more complex than the per-vhost listen change in v1.

---

## DNS record interaction

When a domain is bound to a non-default IP, the reconciler upserts
the apex `@` A / AAAA records on every pass. Rules:

- **Panel-managed rows** (`managed=true`, `managed_by IS NULL`) get
  their content replaced with the effective binding's address.
- **User-edited rows** (`managed=false`) are NEVER touched — the
  operator's edit wins. **The DNS records tab will show a warning
  for these:** *"This record is managed by IP binding. Manual edits
  will not take effect until the binding is removed."*
- **M6-managed rows** (`managed_by='m6'`, e.g. DKIM, autodiscover)
  are also left alone — they're owned by the email subsystem.

The `mail.<domain>` A/AAAA records always point at the **server
primary** (where Stalwart actually listens), not the per-domain
binding. This is by design: the MTA is shared across all domains.

---

## Capacity guidance

The pool's soft-cap is 100 IPs (no hard enforcement, but the
reconciler does a `ListAll` on every per-domain pass and the cost
scales linearly). If you need more than 100 IPs:

- File an issue tagged `m24` so we can switch the per-pass
  denormalization to per-domain `FindByID` (cached) before pool size
  becomes a hot spot.
- Until then, expect a small but observable bump in reconciler tick
  duration; nothing else degrades.

---

## Health checks

```
# How many IPs in the pool?
mariadb -u jabali_panel jabali_panel -e \
  "SELECT family, COUNT(*) FROM managed_ips GROUP BY family;"

# Any degraded?
mariadb -u jabali_panel jabali_panel -e \
  "SELECT id, address, family, label FROM managed_ips WHERE degraded=1;"

# Domains pinned to non-default IPs?
mariadb -u jabali_panel jabali_panel -e \
  "SELECT d.name, m4.address AS ipv4, m6.address AS ipv6
   FROM domains d
   LEFT JOIN managed_ips m4 ON d.listen_ipv4_id = m4.id
   LEFT JOIN managed_ips m6 ON d.listen_ipv6_id = m6.id
   WHERE d.listen_ipv4_id IS NOT NULL OR d.listen_ipv6_id IS NOT NULL;"

# Verify kernel matches DB:
ip -j addr show | jq '[.[].addr_info[] | select(.scope=="global") | .local] | sort'
mariadb -u jabali_panel jabali_panel -BNe \
  "SELECT address FROM managed_ips WHERE is_bound=1 ORDER BY address;"
# The two lists should match. Drift means rebind is overdue.
```
