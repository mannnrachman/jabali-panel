# Architecture Decision Records

This directory contains Architecture Decision Records (ADRs) documenting significant architectural decisions in the Jabali Panel project. Decisions are written in MADR (Markdown Any Decision Records) 3.0 format.

## Status Key
- **Accepted** — Decision is locked in and enforced
- **Proposed** — Under consideration
- **Deprecated** — No longer in use
- **Superseded by** — Replaced by a newer ADR

## ADR Index

| # | Title | Status |
|---|-------|--------|
| [0000](0000-control-plane-model.md) | Control plane model (overview) | Accepted |
| [0001](0001-go-agent-over-ndjson-unix-socket.md) | Go agent over NDJSON Unix socket | Accepted |
| [0002](0002-database-source-of-truth.md) | Database is the source of truth | Accepted |
| [0003](0003-one-write-path-the-api.md) | One write path: the API | Accepted |
| [0004](0004-reconciler-driven-convergence.md) | Reconciler-driven convergence | Accepted |
| [0005](0005-gorm-golang-migrate.md) | GORM for ORM, golang-migrate for schema | Accepted |
| [0006](0006-in-process-worker.md) | In-process worker, not separate daemon | Accepted |
| [0007](0007-english-only-no-i18n.md) | English-only UI, no i18n infrastructure | Accepted |
| [0008](0008-sibling-repos-out-of-scope.md) | Sibling repos are out-of-scope for panel | Accepted |
| [0009](0009-nginx-file-per-vhost.md) | Nginx file-per-vhost with force-regen path | Accepted |
| [0010](0010-install-via-curl-bash.md) | Install via `curl \| bash` only | Accepted |
| [0011](0011-powerdns-mysql-backend.md) | PowerDNS with MySQL backend | Accepted |
| [0012](0012-refine-antd-tanstack.md) | Refine + Ant Design + TanStack Query frontend | Accepted |
| [0013](0013-users-inline-best-effort.md) | Users inline best-effort (not reconciler-managed) | Accepted |
| [0014](0014-panel-port-8443-user-443.md) | PANEL_PORT 8443, user sites on 443 | Accepted |
| [0015](0015-admin-impersonation-jwt-claim.md) | Admin impersonation with `impersonated_by` JWT claim | Accepted |
| [0016](0016-break-glass-cli-admin-login.md) | Break-glass admin login via CLI with `purpose=cli_login` claim | Accepted |
| [0017](0017-ssl-try-acme-then-selfsigned-with-backoff.md) | SSL: try ACME first, fall back to self-signed, retry with backoff | Accepted |
| [0018](0018-m7-mariadb-first-postgres-deferred.md) | M7 Databases — MariaDB first, Postgres deferred | Accepted |
| [0019](0019-m7-per-database-grants-only.md) | M7 Databases — Per-database grants only (rw/ro), defer per-table | Accepted |
| [0020](0020-m7-phpmyadmin-sso-signon-proxy.md) | M7 phpMyAdmin SSO via server-side signon proxy + single-use token | Accepted (partially superseded by ADR-0022) |
| [0021](0021-m7-database-entity-lifecycle.md) | M7 Databases — Entity lifecycle (naming, quota, cascade, password) | Accepted |
| [0022](0022-m7-phpmyadmin-sso-shadow-account-and-uds.md) | M7 phpMyAdmin SSO — shadow admin account + UDS validate transport | Accepted — Parked pending M9 (2026-04-17) |
| [0023](0023-m9-php-fpm-pool-manager.md) | M9 PHP/FPM pool manager | Accepted |
| [0025](0025-per-user-systemd-slices.md) | Per-user systemd slices | Accepted |
| [0026](0026-m10-wordpress-installs.md) | M10 WordPress installs — schema + lifecycle | Accepted |
| [0027](0027-m11-filebrowser-integration.md) | M11 File manager via filebrowser + proxy auth | Accepted |
| [0028](0028-m12-sftp-integration.md) | M12 SFTP via openssh group-based Match (no chroot) | Accepted |
| [0029](0029-m8-cron-systemd-user-timers.md) | M8 Cron via systemd-user timers with closed-set allowlist | Accepted |
| [0053](0053-crowdsec-over-fail2ban.md) | CrowdSec over fail2ban for behaviour-based IP blocking | Accepted |
| [0054](0054-ufw-over-iptables.md) | UFW over raw iptables/nftables for the host firewall | Accepted |
| [0055](0055-modsecurity-per-domain.md) | ModSecurity-nginx + OWASP CRS, per-domain toggle | SUPERSEDED (2026-04-26) by ADR-0060 + M27 AppSec |
| [0056](0056-notification-dispatcher-redis-streams.md) | M14 Notification dispatcher via Redis Streams + consumer group | Accepted |
| [0057](0057-webpush-vapid.md) | M14 Web Push via VAPID, keypair in server_settings | Accepted |
| [0058](0058-ntfy-channel.md) | M14 ntfy.sh channel: plain HTTP POST + optional bearer + priority + tags | Accepted |
| [0059](0059-redis-shared-cache.md) | Redis as shared local cache/queue (unix socket, jabali-sockets group) | Accepted |
| [0060](0060-appsec-geoblock.md) | AppSec geoblock (server-wide country filter) — opt-in | Accepted |
| [0061](0061-allowlists-lapi-truth.md) | CrowdSec allowlists — LAPI is truth, no DB mirror | Accepted |
| [0062](0062-console-enrollment-machine-scope.md) | CrowdSec Console enrollment — operator-driven, disenroll wipes online_api_credentials.yaml | Accepted (amended 2026-04-26) |
| [0063](0063-profiles-yaml-for-remediation-override.md) | Per-scenario remediation override via `/etc/crowdsec/profiles.yaml` | Accepted |
| [0064](0064-diagnostic-report-enclosed-mail.md) | Diagnostic report — enclosed.cc upload + email delivery | Accepted |
| [0065](0065-server-status.md) | Server Status aggregator | Accepted |
| [0066](0066-le-cert-panel-hostname.md) | Let's Encrypt cert for the panel hostname | Accepted |
| [0067](0067-lazy-service-alert-suppression.md) | Suppress critical alert on inactive + disabled services (lazy-started units) | Accepted |
| [0068](0068-per-user-cgroup-slice-metrics.md) | Per-user slice metrics via direct cgroup v2 read | Accepted |
| [0069](0069-server-status-masonry-layout.md) | AntD `<Masonry>` for Server Status page layout | Accepted |

## Decision Categories

### Architecture & Data
- [0000](0000-control-plane-model.md) — Control plane model (overview; superseded in scope by 0002/0003/0004)
- [0002](0002-database-source-of-truth.md) — Database is the source of truth
- [0004](0004-reconciler-driven-convergence.md) — Reconciler-driven convergence
- [0005](0005-gorm-golang-migrate.md) — GORM for ORM, golang-migrate for schema

### API & Communication
- [0001](0001-go-agent-over-ndjson-unix-socket.md) — Go agent over NDJSON Unix socket
- [0003](0003-one-write-path-the-api.md) — One write path: the API

### Deployment & Operations
- [0006](0006-in-process-worker.md) — In-process worker, not separate daemon
- [0010](0010-install-via-curl-bash.md) — Install via `curl | bash` only
- [0014](0014-panel-port-8443-user-443.md) — PANEL_PORT 8443, user sites on 443
- [0015](0015-admin-impersonation-jwt-claim.md) — Admin impersonation with impersonated_by JWT claim
- [0016](0016-break-glass-cli-admin-login.md) — Break-glass admin login via CLI with purpose=cli_login claim
- [0017](0017-ssl-try-acme-then-selfsigned-with-backoff.md) — SSL: try ACME first, fall back to self-signed, retry with backoff

### Infrastructure & Services
- [0009](0009-nginx-file-per-vhost.md) — Nginx file-per-vhost with force-regen path
- [0011](0011-powerdns-mysql-backend.md) — PowerDNS with MySQL backend

### Frontend & UX
- [0007](0007-english-only-no-i18n.md) — English-only UI, no i18n infrastructure
- [0012](0012-refine-antd-tanstack.md) — Refine + Ant Design + TanStack Query frontend

### Scope & Integration
- [0008](0008-sibling-repos-out-of-scope.md) — Sibling repos are out-of-scope for panel
- [0013](0013-users-inline-best-effort.md) — Users inline best-effort (not reconciler-managed)

## How to Use This Document

### When Making Changes
- Before implementing a feature, check which ADRs apply
- If your change violates an accepted ADR, raise it for discussion first
- Reference the relevant ADRs in PR descriptions and commit messages

### When Adding a New ADR
1. Assign the next number (starting from 0001)
2. Use kebab-case for the filename: `NNNN-kebab-case-title.md`
3. Include these sections: Status, Context, Decision, Consequences (positive/negative), Alternatives considered
4. Update this README with a link to the new ADR

### Related Documents
- `docs/runbooks/dns-secondary-nameserver.md` — Secondary nameserver setup (references ADR-0011)
- `BLUEPRINT.md` — Feature roadmap and milestones
