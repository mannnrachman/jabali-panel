# Dashboard

`/jabali-panel/dashboard`. The first page after a tenant logs in.

## Cards

- **Account summary** — display name, primary domain, package name.
- **Disk usage** — used / quota in MiB plus a progress bar; turns amber at 80%, red at 95%.
- **Bandwidth this month** — used / quota in GiB plus the current billing month's reset date.
- **Mailboxes** — count / limit; click to jump to [Mailboxes](./mailboxes.md).
- **Databases** — count / limit; click to jump to [Databases](./databases.md).
- **Domains** — count / limit; click to jump to [Domains](./domains.md).
- **Recent activity** — last 10 audit rows scoped to the account (mailbox passwords reset, cron jobs created, app installs, etc.).
- **Service health (tenant view)** — green / amber / red rollup of services that affect the tenant: nginx, php-fpm for the tenant's PHP version, mariadb, stalwart, redis. Tenants do not see service-level controls.

## Quick links

Top of the page: New Domain · New Mailbox · New Database · Open Files · Open Webmail.

## What is *not* shown

- Server-level vitals (CPU, load, disk free) — those are operator surfaces, see [Server Status](../admin/server-status.md).
- Other tenants' state.

## Source

`panel-ui/src/shells/user/UserDashboard.tsx`. Data via `/api/v1/my/dashboard/summary`, cached 30 seconds.
