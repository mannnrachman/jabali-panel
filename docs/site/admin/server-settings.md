# Server Settings

`/jabali-admin/settings`. Global settings that apply to the whole installation, organized into sections.

## General

- **Panel hostname** — the FQDN the panel and SPA serve on. Changing it triggers reissuance of the [Panel Certificate](./panel-certificate.md).
- **Primary mail domain** — the FQDN Stalwart presents in `HELO`, the `Message-ID` host, and the default `From:` for system mail.
- **Default IP pool** — the IP returned by zone creation when no per-domain override is set.
- **Default package** — the package assigned to new users if the form is left blank.
- **Locale** — default UI locale for new users; users may override per-account.

## Database

- **Root password** — rotate the MariaDB root password ([M46](../databases.md#admin-db-ops-m46)). Stored encrypted in `db_admin_secrets`.
- **Curated tunables** — apply changes to the whitelisted set of MariaDB / PostgreSQL config keys. Reconciler-converged through `db_tuning_reconcile.go`.
- **Maintenance** — schedule `OPTIMIZE` / `ANALYZE` / `CHECK` / `REPAIR` against selected databases.
- **Processlist + Kill** — live processlist with one-click `KILL` against a query.

See [Database Tuning](./database-tuning.md) for the per-key reference.

## Mail

- **Recovery sender** — the `From:` for Kratos password-recovery email.
- **Outbound throttles** — defaults (see [Mail Throttles](./mail-throttles.md)).
- **MTA-STS policy mode** — `enforce` or `testing`.
- **Stalwart expression filters** — admin-defined routing / drop / quarantine expressions (M47 Wave 3v2).

## Security

- **CrowdSec console enrolment** — enrol once to sync decisions, alerts, and allowlists with the central console.
- **AppSec policy** — log only / block (default).
- **Default per-user egress policy** — `default-restricted` / `unrestricted`.
- **AIDE schedule** — daily / weekly / off.
- **Snuffleupagus rule pack** — enabled / disabled.

## Notifications

- **VAPID keys** — Web Push public/private. Auto-generated on first install; rotation here invalidates all current Web Push subscriptions.
- **Default channel routing** — server-wide defaults; per-event overrides live on [Notifications Routing](./notifications-routing.md).

## Updates

- **Update source** — `origin/main` (default) or a pinned branch.
- **Update window** — optional cron expression; outside the window, `jabali update --auto` refuses to run.

## Support

- **Recipient public key** — overrides the maintainers' public key for the encrypted diag bundle.
- **Recipient email** — overrides `webmaster@jabali-panel.com`.

## Convergence

Server settings persist to the `server_settings` table. Most changes take effect immediately; the few that require host-side work (panel hostname, certificate, MariaDB tuning) are scheduled with the reconciler and converge within 60 seconds.

## Audit

Every change writes a structured-diff audit row.
