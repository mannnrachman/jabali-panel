# Domains (Admin)

`/jabali-admin/domains`. The cross-user view of every hosted domain on the panel.

## List

Columns: domain name, owner (username), package, primary / alias, PHP version, SSL state, DNSSEC state, listen IP, suspended flag, created.

Filters: by owner, by package, by SSL state (issued / pending / failed / off), by DNSSEC state, by listen IP, by free-text on the domain name.

## Actions per row

- **Edit** — opens the domain edit page (the same component the user sees, with additional admin-only fields).
- **Disable** — sets `is_disabled=1`; the reconciler returns 503 on every request to the domain.
- **Delete** — destructive; removes the vhost, revokes the SSL certificate, drops the Stalwart domain entry (if mail was enabled), removes DNS zone records.

## Create

The admin create flow is identical to the user create flow except the admin chooses the owning user. Use this to provision a domain on behalf of a user during migration or onboarding.

## Per-domain settings (admin-only fields)

In addition to the fields a user sees:

- **Force PHP version** — pin a PHP version even when the user's package would allow others.
- **Force listen IP** — pin a listen IP from the [IP pool](./ip-addresses.md) regardless of the user's pick.
- **Pin SSL on** — prevent the user from disabling SSL.
- **Quarantine** — soft-suspend; vhost serves a quarantine page citing the operator's note. Used during incident response.

## Convergence

Every change writes to the `domains` table and schedules `Reconciler.Schedule(<domain-id>)`. The reconciler:

1. Re-renders `/etc/nginx/sites-available/<domain>` from the template, atomically swaps the file, and reloads nginx.
2. Requests or revokes the certificate via the agent, as appropriate.
3. Updates the apex DNS `A` and `AAAA` records to reflect the listen IP.
4. Toggles DNSSEC signing.

Convergence latency is typically under 60 seconds.

## CLI

```bash
jabali domain list
jabali domain enable  <name|id>
jabali domain disable <name|id>
jabali domain delete  <name|id>
```
