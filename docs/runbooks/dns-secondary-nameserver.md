# Setting up a secondary nameserver (ns2)

Jabali Panel runs the primary authoritative nameserver (ns1) on the
panel host. A secondary nameserver (ns2) is strongly recommended — most
registrars require at least two NS records with different IPs before
they'll accept NS delegation.

## Option A — another plain PowerDNS box

Any Debian/Ubuntu server with public DNS ports (53 UDP+TCP) open.

1. Install PowerDNS:
   ```bash
   apt install pdns-server pdns-backend-bind
   ```

2. Create a minimal `/etc/powerdns/pdns.d/slave.conf`:
   ```
   launch=bind
   slave=yes
   superslave=yes
   trusted-notification-proxy=<NS1_IPV4>
   supermasters=/etc/powerdns/supermasters
   ```

3. Create `/etc/powerdns/supermasters`:
   ```
   <NS1_IPV4>  ns1.<yourhostname>  admin@example.com
   ```

4. Start: `systemctl enable --now pdns`.

5. Back in the Jabali admin panel, visit Server Settings and set:
   - `ns2 hostname` = `ns2.<yourhostname>`
   - `ns2 IPv4` = this box's public IPv4

   Saving triggers a zone re-push across every hosted domain. The
   secondary will receive NOTIFY for each zone and pull it via AXFR.

6. At your registrar, add a second NS glue record pointing to ns2.

## Option B — third-party secondary DNS

Services like Hurricane Electric's free DNS, buddyns.com, or nsupdate
offer secondary-only DNS. Configure them to slave from `<NS1_IPV4>` and
enter their IP as ns2 in Server Settings. Same effect.

## Verifying it works

From the ns2 host:

```bash
dig AXFR <hosted-domain> @127.0.0.1
```

Should return the full zone. If it returns `REFUSED` or empty, check:

- Did you save Server Settings in the admin panel after installing ns2?
  (That's what pushes the `ALLOW-AXFR-FROM` metadata to ns1.)
- Is `ns2_ipv4` in Server Settings exactly the IP ns2 will source AXFR
  requests from? (No NAT or source-address translation in the path.)
- On ns1, check `pdns_control notify <zone>` exits zero and no error
  shows in `journalctl -u pdns`.

## How the pieces fit

- Panel DB is the source of truth for every zone's records.
- On any change to a domain or its records, the panel reconciler calls
  the agent's `dns.zone.upsert` command.
- The agent rewrites PowerDNS's native `records` table inside a
  transaction and updates the per-zone `domainmetadata` rows for
  `ALLOW-AXFR-FROM` (= ns2 IPv4) and `ALSO-NOTIFY` (= ns2 IPv4).
- PowerDNS sends a NOTIFY packet to ns2; ns2's daemon wakes up and
  pulls the fresh zone via AXFR.
