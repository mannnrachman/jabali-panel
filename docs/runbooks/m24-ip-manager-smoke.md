# M24 IP Manager — smoke runbook

Validates the per-domain `listen_ip` workflow end-to-end on a real
VM with at least two routable IPv4 addresses.

## Prerequisites

- VM with NIC on at least two routable IPs (or a /29 + ifupdown
  alias on the same NIC).
- `jabali update --force` deployed.
- Two existing tenant accounts with at least one domain each.

## Setup — register pool

```
ssh root@<vm>
ip -4 addr show | grep -E '^\s+inet'
# Note the two addresses, e.g. 192.168.100.150 and 192.168.100.151.
```

Admin shell → IP Addresses → Add IPv4
- Address: 192.168.100.151
- Family: ipv4
- Enabled: yes

Expect:
- New row in `managed_ips` table.
- `panel-api` reconciler binds the address within 60 s if `is_bound`
  starts true (or stays unbound if you left it false).

```
ip -4 addr show | grep 192.168.100.151
# expect: inet 192.168.100.151/<prefix> scope global secondary <dev>
```

## Bind a domain to the secondary IP

Admin shell → Domains → pick existing → Edit → Listen IP →
192.168.100.151 → Save.

Expect:
- `domains.listen_ip_id` updated.
- Reconciler renders the vhost with `listen 192.168.100.151:443
  ssl;` instead of `listen 0.0.0.0:443 ssl;`.
- nginx reload succeeds.

```
ssh root@<vm> "grep -nH 'listen ' /etc/nginx/sites-enabled/<domain>.conf"
# expect: listen 192.168.100.151:443 ssl;
```

## Apex DNS auto-update

For domains backed by panel-managed DNS (PowerDNS):
```
sudo -u jabali pdnsutil list-zone <domain> | grep -E '^\s*<domain>\.'
# A record should match the bound address.
```

## Failover — drop the secondary IP

Simulate a netplan glitch:
```
ssh root@<vm> "ip -4 addr del 192.168.100.151/<prefix> dev <iface>"
sleep 70   # wait for next ReconcileManagedIPs pass
ip -4 addr show | grep 192.168.100.151
# expect: re-bound
```

Reconciler logs (`journalctl -u jabali-panel`) should show:
```
managed-ips: rebind kernel address ... ip=192.168.100.151
```

## Cleanup

Admin shell → Domains → revert listen IP to "default" → Save.
Admin shell → IP Addresses → delete 192.168.100.151 row.
Reconciler unbinds the kernel address on next pass (or `ip addr del`
manually if `is_bound=false` was the only state change).

## Failure escalation

| Symptom | Probable cause | Fix |
|---------|----------------|-----|
| Reconciler doesn't rebind after `ip addr del` | `managed_ips.is_bound = false` | Toggle Enabled in UI; reconciler rebinds within 60 s |
| nginx reload fails | colliding listener already on :443 with same IP | Check `nginx -T \| grep "listen 192.168"` for duplicates |
| pdnsutil shows old A record | reconciler hasn't reached domain in current pass | Wait one full ReconcileAll cycle (~60 s) |
| Domain page Listen IP dropdown empty | no enabled rows in `managed_ips` | Add at least one IPv4 in IP Addresses page |

## CI hook

Playwright spec at `panel-ui/tests/e2e/ip-manager.spec.ts` already
covers the UI happy path against a mocked backend. The above runbook
is the live-VM counterpart — kernel + nginx + DNS layers Playwright
can't touch.
