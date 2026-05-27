# Domains

A **Domain** is a hosted vhost owned by exactly one user. Each domain row in the panel DB drives a real nginx vhost — the reconciler converges them on every tick.

## Per-domain settings

Edit a domain via `/jabali-admin/domains/edit/:id` (admin) or `/jabali-panel/domains/edit/:id` (owner).

| Setting | What it does |
|---|---|
| **Domain name** | The vhost server_name (set at create time; rename = delete + recreate). |
| **PHP version** | Which Sury PHP-FPM pool the vhost passes `.php` to. Selected from the versions enabled on the host. |
| **PHP settings** | Per-user (not per-domain) overrides; see PHP Settings. |
| **SSL** | Enable Let's Encrypt for this domain (HTTP-01). Reconciler issues within ≤60 s and reloads nginx. |
| **DNSSEC** | Enable DNSSEC signing for the zone. Generates KSK + ZSK; displays the DS record to publish at the parent registrar. |
| **Listen IP** | Pick from the admin's managed IP pool. Default: server's primary IP. Apex DNS records auto-update. |
| **Redirects** | HTTP → HTTPS (always emitted when SSL is on); per-path redirects (planned). |
| **Aliases** | Additional `server_name` values that resolve to the same vhost + same docroot. |
| **Cache** | Per-domain FastCGI micro-cache (planned, ADR-0108). |
| **Email** | Toggle whether this domain runs mail (adds DKIM keys + MX hint to DNS + Stalwart Domain entry). |

## Domain lifecycle

```
create  → DB row inserted → reconciler builds vhost, requests cert if SSL=on
suspend → DB flag set       → reconciler returns 503 page
delete  → DB row removed    → reconciler tears down vhost, revokes cert, drops Stalwart domain
```

The reconciler is the only thing that re-applies vhosts; never edit nginx site files by hand — they'll be overwritten on the next tick.

## What lives outside the vhost

- **DNS records** are managed under DNS (PowerDNS auth backend, MariaDB-backed).
- **Mailboxes** for this domain live under Mail.
- **Databases** belong to the user, not the domain.

## CLI

```bash
jabali domain list                 # all domains
jabali domain create <name> --user <id>
jabali domain enable <name|id>
jabali domain disable <name|id>
jabali domain delete <name|id>
```

See [platform/cli.md](./platform/cli.md) for full flag reference.
