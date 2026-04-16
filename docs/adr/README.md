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
- `docs/dns-secondary-nameserver.md` — Secondary nameserver setup (references ADR-0011)
- `BLUEPRINT.md` — Feature roadmap and milestones
