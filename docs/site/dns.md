# DNS

Jabali ships **two** PowerDNS processes:

- **pdns-server** (authoritative) — answers for hosted zones, MariaDB backend, listens on the server's public IPs `:53`.
- **pdns-recursor** — handles local `.` recursion for the server itself, listens on loopback `127.0.0.1:53`.

Split-port setup (ADR-0047): the recursor binds the loopback so local processes (the panel, certbot, Stalwart, etc.) can resolve external names without going through an upstream resolver, while the authoritative binds public IPs so the world can query hosted zones.

## Zones

Each hosted domain gets an authoritative zone in PowerDNS. Default records:

- `@` A → server primary IP (or domain's listen IP if set)
- `@` AAAA → server primary IPv6 (if configured)
- `www` CNAME → `@`
- `@` MX 10 `mail.<panel-hostname>.` (if domain has mail enabled)
- `mail` A → server primary IP (if mail enabled)
- `_dmarc` TXT (if mail enabled)
- DKIM TXT (if mail enabled)
- SPF TXT (if mail enabled)
- MTA-STS TXT (if mail enabled, ADR-0109)

Add / edit / delete custom records under DNS → `<domain>` → Records.

## DNSSEC

Per-domain toggle. When enabled:

1. Agent calls `pdnsutil secure-zone <domain>` → KSK + ZSK generated and rectified.
2. Panel persists key metadata in the DB so it survives PDNS rebuilds.
3. The DNSSEC page displays the **DS record** to publish at the parent registrar.

Until the DS is published at the registrar, the zone is signed but not part of the chain of trust — that's fine for testing; not fine for production.

CLI:

```bash
jabali pdns dnssec enable <domain>
jabali pdns dnssec status <domain>
jabali pdns dnssec ds <domain>      # prints DS for parent registrar
jabali pdns dnssec disable <domain>
```

See [platform/dnssec.md](./platform/dnssec.md) for the architecture details.

## Cache invalidation

PowerDNS auth caches answers for `cache-ttl` (default 60 s). After any panel-side mutation (record add/update/delete, zone create/delete) the agent runs `pdns_control purge <zone>$` so callers don't see stale data for up to 60 s. This is the "PDNS cache after backend write" rule — without it, fresh records appear stuck.

## Recursor forwarders

The recursor uses `/etc/powerdns/recursor.forwards`, populated by `jabali pdns backfill` from the panel DB. Use this if you want to forward specific zones to an internal resolver (e.g. for split-horizon DNS).

## DNS-related agent commands

- `dns.zone.upsert` — create or update a zone.
- `dns.zone.delete` — drop a zone.
- `dns.dnssec.enable` / `dns.dnssec.disable` — per-domain DNSSEC.

All zone mutations go through the agent so the cache purge is automatic and atomic.
