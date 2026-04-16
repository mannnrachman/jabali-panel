# Jabali Panel — Feature Blueprint

A complete catalogue of what Jabali Panel does, how the pieces fit together, and
which code owns each capability. Use this as a map when onboarding, scoping new
features, or deciding where existing logic already lives.

> **Sources of truth.** This blueprint reflects what's shipped on `main` as of
> 2026-04-17. Architecture rules and conventions live in `docs/adr/`. Cross-repo
> coordination with sibling projects lives in `~/projects/jabali-shared/CONTEXT.md`.

---

## 1. What Jabali Panel is

Jabali Panel is a web hosting control panel for Linux servers, built with a
**React + Refine + Ant Design** frontend and a **Go (Gin)** backend. It runs on
a single Debian/Ubuntu host and manages domains, users, hosting packages, DNS,
and (planned) email, databases, PHP pools, WordPress, and SSL certificates.

The Go backend (`panel-api/`) speaks to a privileged Go agent (`panel-agent/`)
over a Unix socket at `/run/jabali/agent.sock` (NDJSON protocol). The backend
serves the React SPA on port `8443` and exposes a REST API. A reconciler
goroutine converges database state to agent operations (domains, DNS zones,
services). The database is the single source of truth.

### Key components

- **panel-api/** — Go HTTP server (Gin) + WebSocket hub + reconciler + agent RPC client
- **panel-agent/** — Go binary running as root; executes privileged commands from panel-api
- **panel-ui/** — React SPA (Refine + Ant Design + TanStack Query)
- **install.sh** — single install path; bootstraps database, panel-api, panel-agent, PowerDNS, nginx
- **docs/adr/** — architectural decision records justifying Go agent, DB-as-truth, one-write-path, etc.

### Two personas, two route namespaces

| Role    | URL mount        | Audience                     | JWT claim |
|---------|------------------|------------------------------|-----------|
| `admin` | `/jabali-admin/` | Server operator              | `is_admin=true` |
| `user`  | `/jabali-panel/` | Hosting users (per-domain)   | `is_admin=false` |

Both authenticate via `/api/v1/auth/login` (JWT access + refresh tokens). User
records are never elevated via public API — `is_admin` is only set directly in
the database.

---

## 2. Architecture (locked-in)

Reference the ADRs for rationale; this section locks in the decisions:

1. **Go agent** — NDJSON over Unix socket, root-only, executes privileged ops.
   No PHP agent. Ever. (ADR #0001-go-agent-over-ndjson-unix-socket.md)

2. **Database is source of truth** — one flow: `Database → Generator → Config files → service reload`.
   Never describe nginx.conf, pdns.conf, etc. as truth. (ADR #0002-database-source-of-truth.md)

3. **One write path = the API** — admin actions, CLI commands, manager integrations
   all go through panel-api handlers. No direct DB writes from CLI/agent. (ADR #0003-one-write-path-the-api.md)

4. **Reconciler pattern** — in-process goroutine inside panel-api converges
   DB state to agent operations. No separate worker service. (ADR #0004-reconciler-driven-convergence.md)

5. **English-only UI** — no Refine i18n provider, no translation files.
   (ADR #0007-english-only-no-i18n.md)

6. **Sibling repos out-of-scope** — jabali-manager, jabali-security, jabali-isolator,
   jabali-tunnel, Bulwark webmail live in separate repos. (ADR #0008-sibling-repos-out-of-scope.md)

7. **Nginx file-per-vhost** — each domain has its own `/etc/nginx/sites-available/<domain>.conf`,
   regenerated from DB by reconciler + `jabali nginx regenerate --force` CLI. (ADR #0009-nginx-file-per-vhost.md)

8. **PANEL_PORT 8443** — panel listens on `0.0.0.0:8443` (configurable via `JABALI_PANEL_ADDR`).
   User sites on nginx port `443`. (ADR #0014-panel-port-8443-user-443.md)

9. **PowerDNS with MySQL backend** — separate `jabali_pdns` database, zone/record CRUD via REST API.
   (ADR #0011-powerdns-mysql-backend.md)

### Architecture diagram

```
┌──────────────────────────────────────────────────────────────┐
│  Browser (React SPA: Refine + Ant Design + TanStack Query)   │
│   ├── Admin routes: /jabali-admin/*                          │
│   └── User routes: /jabali-panel/*                           │
└──────────────────────────────────────────────────────────────┘
                 │ HTTPS :8443 (JWT in Authorization header)
                 ▼
┌──────────────────────────────────────────────────────────────┐
│  Go backend (Gin router) - panel-api/                        │
│   ├── /api/v1/*       — REST API (users, domains, dns, etc.) │
│   ├── /ws/*           — WebSocket hub (logs, stats, tasks)   │
│   ├── /reconciler/*   — admin reconcile trigger endpoints    │
│   ├── static          — compiled React build (/)             │
│   └── [Reconciler goroutine running in background]           │
└──────────────────────────────────────────────────────────────┘
                 │ every privileged op
                 ▼
┌──────────────────────────────────────────────────────────────┐
│  AgentClient → /run/jabali/agent.sock (Unix socket, root)    │
│  NDJSON RPC protocol, UTF-8 sanitization, timeout guards     │
└──────────────────────────────────────────────────────────────┘
                 │ AgentClient.Send(cmd, args)
                 ▼
┌──────────────────────────────────────────────────────────────┐
│  bin/jabali-agent (Go, root, panel-agent/)                   │
│   Handler registry: 14 commands (domain.*, dns.*, user.*,    │
│   nginx, service.*, system.*) with argument sanitization     │
└──────────────────────────────────────────────────────────────┘
                 │ exec + shell escaping
                 ▼
┌──────────────────────────────────────────────────────────────┐
│  System services: nginx, php-fpm, mariadb, postgresql,       │
│  stalwart-mail (planned), powerdns, certbot, goaccess,       │
│  redis, (optional) jabali-security, jabali-isolator          │
└──────────────────────────────────────────────────────────────┘
```

**Golden rules:**

1. React frontend never touches shell or protected files; all privileged ops flow API → agent.
2. Agent socket never exposed over network.
3. All agent command arguments use safe escaping.
4. Go API enforces JWT auth + RBAC middleware on every route.
5. React SPA holds no secrets (tokens stored in memory/sessionStorage only).

---

## 3. Repository layout

| Path | Purpose |
|------|---------|
| `panel-api/` | Go HTTP server (Gin), database models (GORM), reconciler, agent RPC client, WebSocket hub, API handlers |
| `panel-agent/` | Go binary running as root via systemd socket activation; command handler registry (14 commands) |
| `panel-ui/` | React SPA (Refine + Ant Design + TanStack Query); admin + user shells and pages |
| `install.sh` | Single supported install path; prompts + idempotency, bootstraps all services |
| `docs/adr/` | 14 architectural decision records explaining locked-in choices |
| `Makefile` | Build targets: `make server`, `make agent`, `make ui`, `make install` |
| `go.mod` / `go.sum` | Go dependencies |
| `package.json` | Node dependencies (React build) |

---

## 4. What's shipped (as of 2026-04-16)

Organized by domain. All paths verified; all commits available via `git log`.

### 4.1 Authentication & Authorization

**Shipped:** JWT access + refresh tokens, bcrypt password hashing, auth middleware,
RBAC guards (`RequireAdmin`, `RequireOwner`), bootstrap seed (admin user + package).

- **Files:** `panel-api/internal/auth/`, `panel-api/internal/middleware/`
- **Migrations:** `000001_init_users.sql`, `000002_init_refresh_tokens.sql`
- **API:** `/api/v1/auth/login`, `/api/v1/auth/refresh`, `/api/v1/auth/logout`
- **Evidence:** Commits `f7464a2`, `177bc3a` (recent); earlier: `6b5dea4`, `fb3abea`

### 4.2 Users & Hosting Packages

**Shipped:** User CRUD (create/read/update/delete), password reset, hosting packages
(CRUD), package assignment to users. Username field on users.

- **Files:** `panel-api/internal/api/users.go`, `panel-api/internal/api/packages.go`
- **Migrations:** `000003_init_hosting_packages.sql`, `000004_add_package_id_to_users.sql`,
  `000007_add_username_to_users.sql`
- **Agent commands:** `user.create`, `user.delete`, `user.password`
- **API:** `/api/v1/users`, `/api/v1/packages`, `/api/v1/me`
- **Evidence:** Commits `fb3abea`, `6b5dea4`

### 4.3 Domains & Nginx

**Shipped:** Domain CRUD, document root per domain, host-level redirects, page-level
redirects (HTTP → redirect or frame inject), nginx custom rules (6 types: allow-deny,
auth-basic, gzip, headers, rewrite, proxy-pass), enable/disable toggle, index file
priority, vhost regeneration.

- **Files:** `panel-api/internal/api/domains.go`, `panel-api/internal/redirects/`,
  `panel-api/internal/nginxrules/`, `panel-agent/internal/commands/domain_*.go`,
  `panel-agent/internal/commands/nginx.go`
- **Migrations:** `000005_create_domains.sql`, `000006_hard_delete_domains_users_packages.sql`,
  `000008_rewrite_domain_docroots.sql`, `000009_add_redirects_to_domains.sql`,
  `000010_add_index_priority_to_domains.sql`, `000011_add_nginx_rules_to_domains.sql`
- **Agent commands:** `domain.create`, `domain.delete`, `domain.list`, `domain.toggle`, `nginx`
- **API:** `/api/v1/domains`, `/api/v1/domains/{id}/redirects`, `/api/v1/domains/{id}/nginx-rules`
- **UI pages:** `AdminLayout` (domain list stub), `ServerSettingsPage` (hostname field only),
  `UserLayout`, user domain list/create pages (React routes wired but minimal)
- **Evidence:** Commits `fb3abea` (nginx rules), `0e92a04`, earlier commits down to `21e4834`

### 4.4 DNS (PowerDNS integration)

**Shipped:** DNS zones CRUD (create/read/update/delete/purge), DNS records CRUD
(type-aware form builder: A, AAAA, CNAME, MX, TXT, NS, SOA, etc.), per-zone AXFR
allow-list, per-zone NOTIFY target for secondary NS, PowerDNS MySQL backend compiler,
reconciler auto-bootstrap zones on domain create.

- **Files:** `panel-api/internal/api/dns.go`, `panel-api/internal/dnscompile/`,
  `panel-api/internal/reconciler/reconciler.go` (DNS zone reconciliation),
  `panel-agent/internal/commands/dns_zone_*.go`
- **Migrations:** `000013_create_dns_tables.sql` (zones + records + axfr_allow_list)
- **Agent commands:** `dns.zone.upsert`, `dns.zone.delete`
- **API:** `/api/v1/dns/zones`, `/api/v1/dns/zones/{id}/records`, `/api/v1/dns/zones/{id}/axfr-allow-list`,
  `/api/v1/dns/zones/{id}/notify-target`
- **UI pages:** `DNSRecordsPage` (type-aware add/edit form with managed-record safeguards)
- **Evidence:** Commits `f7464a2`, `4b87395`, `0ae7213`, `3037db1`, `cb4ae39`

### 4.5 Server Settings & System

**Shipped:** Hostname, NS1/NS2 nameservers (for zone SOA), public IPv4/IPv6 (auto-detected
during install), server-info endpoint (system uptime, Go version, agent version), agent
command to set hostname.

- **Files:** `panel-api/internal/api/system.go`, `panel-agent/internal/commands/system_*.go`,
  `panel-ui/src/shells/admin/settings/ServerSettingsPage.tsx`
- **Migrations:** `000012_create_server_settings.sql`
- **Agent commands:** `system.info`, `system.set_hostname`, `agent.version`
- **API:** `/api/v1/system/settings`, `/api/v1/system/info`, `/api/v1/system/health`
- **UI pages:** `ServerSettingsPage` (hostname, ns1/ns2, public IPs read-only)
- **Evidence:** Commits `6b5dea4`, `0e92a04`, `ab47b7d`, `bcc5f93`

### 4.6 Agent & RPC

**Shipped:** NDJSON over Unix socket (`/run/jabali/agent.sock`), UTF-8 sanitization,
configurable timeout (5s default), MockAgentClient for testing, 14 command handlers
registered, error propagation.

- **Files:** `panel-api/internal/agent/` (AgentClient, AgentError, MockAgentClient)
- **Agent registry:** `panel-agent/internal/commands/registry.go` + 14 command files
  (agent_version, domain_create, domain_delete, domain_list, domain_toggle, dns_zone_upsert,
  dns_zone_delete, nginx, service_list, system_info, system_set_hostname, user_create,
  user_delete, user_password)
- **Evidence:** Commits throughout; registry `panel-agent/internal/commands/registry.go`

### 4.7 SSL / Let's Encrypt (M5a/b/c)

**Shipped (Phase 2):** Per-domain SSL toggle, Let's Encrypt issuance with try-ACME-first
strategy, fallback to self-signed certificates on ACME failure, exponential backoff retry
scheduling (5m → 15m → 1h → 4h, capped), manual retry endpoint, SSL Manager UI pages (admin + user),
and stopgap `ssl.self_sign` agent command for emergency fallback.

- **Files:** `panel-api/internal/api/ssl.go`, `panel-agent/internal/commands/ssl_*.go`
- **Migrations:** `000014_add_ssl_enabled_to_domains.sql`, `000015_create_ssl_certificates.sql`,
  `000017_ssl_enabled_default_true.sql`, `000018_add_next_retry_at_to_ssl_certificates.sql`
- **Agent commands:** `ssl.issue`, `ssl.renew`, `ssl.revoke`, `ssl.self_sign`
- **API:** `/api/v1/domains/{id}/ssl` (GET status, POST issue, DELETE revoke), `/api/v1/domains/:id/ssl/retry` (admin-only manual retry)
- **UI pages:** SSL Manager pages for both admin and user shells; per-domain toggle + status display
- **Evidence:** Commits `ba54273`, `66ae6d2`, `34379db`, `786079a`, `fc8ff7d`
- **Related ADRs:** ADR-0017 (try-ACME-then-selfsigned-with-backoff)

### 4.8 Authentication & Authorization — Phase 2 (M5a/b)

**Shipped:** Admin impersonation (log in as a specific user without their password),
break-glass CLI login (emergency admin access via `/auth/cli-login` with `purpose=cli_login` claim
and configurable one-time token lifetime), impersonation banner in UI, graceful exit flow.

- **Files:** `panel-api/internal/api/auth.go`, auth middleware updates
- **Migrations:** `000016_add_impersonated_by_to_refresh_tokens.sql`
- **API:** `/admin/users/{id}/impersonate` (POST), `/auth/cli-login` (POST with secret), (no dedicated exit endpoint — UI calls `/auth/logout` then redirects to re-login)
- **UI:** Impersonation banner (red bar showing "Logged in as user X, impersonated by admin Y") with exit button
- **Evidence:** Commits `7bc292f`, `c587144`, `5b14b4c`
- **Related ADRs:** ADR-0015 (admin-impersonation-jwt-claim), ADR-0016 (break-glass-cli-admin-login)

### 4.9 Reconciler

**Shipped:** In-process goroutine inside panel-api that listens for domain DB changes,
issues agent commands to create/update/delete domains on agent, reconciles DNS zones
to PowerDNS MySQL backend, reconciles SSL certificates (try-ACME-first, fallback to self-signed),
runs every 30s by default (configurable), manual trigger endpoints for admin.

- **Files:** `panel-api/internal/reconciler/reconciler.go`
- **API:** `/api/v1/reconcile/all` (admin-only), `/api/v1/reconcile/{domainID}` (admin-only)
- **Evidence:** Commits `cb4ae39`, `0ae7213`, `3037db1`, `ba54273`

### 4.8 Install script

**Shipped:** Prompts for hostname, ns1/ns2, public IPv4/IPv6 (via `/dev/tty` for curl|bash),
auto-detects main interface IP, creates /etc/jabali-panel, database setup, panel-api
binary install, panel-agent binary + systemd socket activation, PowerDNS MySQL backend
setup (merges local-address + local-ipv6 into pdns.conf), nginx reload, idempotency
guards (checks for existing config files before overwriting).

- **Files:** `/home/shuki/projects/jabali2/install.sh` (single entry point)
- **Evidence:** Commits `f8b02a5`, `98e9bb6`, `4a8487c`, `7490b4f`, `113f639`, `177bc3a`

### 4.9 Database schema (GORM migrations)

18 migrations:
1. `000001_init_users.sql` — users table
2. `000002_init_refresh_tokens.sql` — refresh_tokens table
3. `000003_init_hosting_packages.sql` — hosting_packages table
4. `000004_add_package_id_to_users.sql` — FK to packages
5. `000005_create_domains.sql` — domains table (doc_root, enabled, etc.)
6. `000006_hard_delete_*` — removes soft-delete triggers
7. `000007_add_username_to_users.sql` — username field
8. `000008_rewrite_domain_docroots.sql` — schema change for doc root paths
9. `000009_add_redirects_to_domains.sql` — redirect fields
10. `000010_add_index_priority_to_domains.sql` — index file priority
11. `000011_add_nginx_rules_to_domains.sql` — nginx_rules JSONB field
12. `000012_create_server_settings.sql` — server_settings table (hostname, ns1, ns2, public_ipv4, public_ipv6)
13. `000013_create_dns_tables.sql` — dns_zones, dns_records, dns_axfr_allow_list tables
14. `000014_add_ssl_enabled_to_domains.sql` — ssl_enabled boolean field on domains
15. `000015_create_ssl_certificates.sql` — ssl_certificates table (cert, key, issued_at, expires_at, status, failure_reason, retry_count)
16. `000016_add_impersonated_by_to_refresh_tokens.sql` — impersonated_by FK (tracks admin impersonation trail)
17. `000017_ssl_enabled_default_true.sql` — SSL enabled by default (=1) on new domains
18. `000018_add_next_retry_at_to_ssl_certificates.sql` — next_retry_at timestamp for exponential backoff retry scheduling

---

## 5. What's out-of-scope

These live in separate git repositories. `install.sh` may install them as optional
addons, but this blueprint covers only panel-local code:

- **jabali-manager** — multi-node central control plane (separate repo)
- **jabali-security** — WAF, GeoIP, CrowdSec plugin (separate repo)
- **jabali-isolator** — nspawn container manager for SSH shell access (separate repo)
- **jabali-tunnel** — WSS sidecar for manager↔node encrypted tunnel (separate repo)
- **Bulwark webmail** — separate Next.js app (separate repo)

See `~/projects/jabali-shared/CONTEXT.md` for cross-repo coordination.

---

## 6. Milestone roadmap

Milestones describe locked-in delivery order. Status: Shipped, In-flight, or Planned.

### M1: Foundations (SHIPPED)

**Goal:** Bootstrap auth, users, and hosting packages so admins can log in and assign
  hosting users to resource tiers.

**Deliverables:**
- JWT access + refresh tokens
- Admin bootstrap seed (one admin user, one default package)
- User CRUD (create/read/update/delete/password-reset)
- Hosting package CRUD
- RBAC guards (RequireAdmin, RequireOwner)

**Status:** Shipped (commits `6b5dea4` and earlier)

**Depends on:** —

### M2: Domain lifecycle + Nginx rules + Redirects (SHIPPED)

**Goal:** Admins and users can create/manage domains with custom nginx rules,
  host-level + page-level redirects, and index file priority.

**Deliverables:**
- Domain CRUD (create/read/update/delete)
- Document root per domain
- Host-level redirects (HTTP 301/frame inject)
- Page-level redirects (regex pattern matching)
- Nginx custom rules: allow-deny, auth-basic, gzip, headers, rewrite, proxy-pass (6 types)
- Domain enable/disable toggle
- Index file priority
- Nginx vhost regeneration (agent command + reconciler)
- Domain deletion cascade (nginx vhost cleanup)

**Status:** Shipped (commits `fb3abea`, `0e92a04`, earlier commits)

**Depends on:** M1

### M3: Server Settings + Hostname + Public IPs (SHIPPED)

**Goal:** Admins can view and configure server hostname, nameserver IPs (for DNS SOA),
  and auto-detected public IPv4/IPv6.

**Deliverables:**
- Server settings table (hostname, ns1, ns2, public_ipv4, public_ipv6)
- Bootstrap seed hostname from install prompt
- Public IP auto-detect from external service
- NS1/NS2 auto-derive from hostname
- Admin page to read/update hostname (agent command to persist)
- Agent command: `system.set_hostname`, `system.info`

**Status:** Shipped (commits `6b5dea4`, `0e92a04`, `ab47b7d`, `bcc5f93`)

**Depends on:** M1

### M4: DNS zones + records + Secondary NS support (SHIPPED)

**Goal:** Admins can create authoritative DNS zones, manage records (type-aware forms),
  and configure secondary nameserver AXFR + NOTIFY.

**Deliverables:**
- DNS zone CRUD (create/read/update/delete/purge)
- DNS record CRUD (A, AAAA, CNAME, MX, TXT, NS, SOA, etc.)
- Type-aware form builder (per-record-type validation)
- Per-zone AXFR allow-list (IP addresses allowed to AXFR from this zone)
- Per-zone NOTIFY target (secondary NS IP to notify on zone changes)
- PowerDNS MySQL backend compiler (zone+record→pdns DB)
- Reconciler auto-bootstrap zones on domain create
- Agent command: `dns.zone.upsert`, `dns.zone.delete`
- UI: DNSRecordsPage (type-aware add/edit, safeguards for managed records)
- Managed record detection (SOA, NS auto-created, user-editable MX/A/AAAA)

**Status:** Shipped (commits `f7464a2`, `4b87395`, `0ae7213`, `3037db1`, `cb4ae39`)

**Depends on:** M2 (domains must exist before zones)

### M5: SSL / Let's Encrypt (SHIPPED)

**Goal:** Admins and users can issue and renew SSL certificates via Let's Encrypt
  on a per-domain basis, with resilient ACME issuance and automatic fallback.

**Phase 1 (M5a): Core SSL + UI (Shipped)**
- Per-domain opt-in SSL toggle (ssl_enabled boolean; default=true on new domains)
- SSL CRUD (read-only for users; admin can manually issue/revoke)
- Certbot webroot issuer (agent command places acme-challenge on filesystem)
- Agent commands: `ssl.issue`, `ssl.renew`, `ssl.revoke`
- API: `/api/v1/domains/{id}/ssl` (GET status, POST issue, DELETE revoke)
- UI: SSL Manager pages (admin + user shells) with per-domain toggle + status display

**Phase 2 (M5b): Resilient ACME + Fallback (Shipped)**
- Try-ACME-first strategy with 60s timeout
- Fallback to self-signed if ACME fails (generates 30-day self-signed cert)
- Exponential backoff retry scheduling (5m → 15m → 1h → 4h, capped at 4h)
- next_retry_at timestamp + retry_count tracking in ssl_certificates table
- Max 20 retries; mark cert as permanently failed if exceeded
- Manual retry endpoint: `/api/v1/domains/:id/ssl/retry` (admin-only)
- Agent command: `ssl.self_sign` (stopgap emergency fallback)
- Reconciler: syncs SSL state for all domains every 30s, respects retry backoff

**Status:** Shipped (commits `ba54273`, `66ae6d2`, `34379db`, `786079a`, `fc8ff7d`)

**Depends on:** M2 (domains must exist)

### M5a: Admin Impersonation (SHIPPED)

**Goal:** Admins can log in as a specific user to debug or support without knowing their password.

**Deliverables:**
- `/admin/users/{id}/impersonate` endpoint (POST, returns new JWT with `impersonated_by` claim)
- `impersonated_by` FK column on refresh_tokens (tracks which admin is impersonating)
- Impersonation banner in UI (red bar: "Logged in as user X, impersonated by admin Y")
- Exit flow reuses `/auth/logout` — no separate endpoint. UI clears in-memory token + redirects to login
- Support for nested impersonation: admin X impersonates user Y's session (audit trail via impersonated_by chain)

**Status:** Shipped (commit `7bc292f`)

**Depends on:** M1 (auth infrastructure)

### M5b: Break-Glass CLI Login (SHIPPED)

**Goal:** Provide emergency admin access when primary auth (GUI) is unavailable or compromised.

**Deliverables:**
- `/auth/cli-login` endpoint (POST, requires `secret` query param matching `CLI_LOGIN_SECRET` env var)
- Returns JWT with `purpose=cli_login` claim (identifies as emergency login, not normal auth)
- CLI login token lifetime configurable via `CLI_LOGIN_SECRET_TTL` env var (default 1 hour)
- Audit logging (records all CLI logins in application logs with timestamp + IP)
- No UI exposure: CLI login endpoint for tooling / shell scripts only

**Status:** Shipped (commit `c587144`)

**Depends on:** M1 (auth infrastructure)

**Related ADR:** ADR-0016 (break-glass-cli-admin-login.md)

### M6: Email (Stalwart integration) (PLANNED)

**Goal:** Admins and users can create mailboxes, configure forwarders, and set per-domain
  DKIM keys.

**Deliverables:**
- New migrations: `create_mailboxes.sql`, `create_forwarders.sql`, `create_dkim_keys.sql`
- Mailbox CRUD (email, password, quota)
- Forwarder CRUD (source email, destination list)
- Catch-all per domain (fallback to mailbox or forwarder)
- Per-domain DKIM key generation + rotation
- Stalwart SQL config generation from DB (virtualusers, virtual_domains, etc.)
- Agent commands: `email.mailbox.create`, `email.mailbox.delete`, `email.mailbox.password`,
  `email.forwarder.create`, `email.forwarder.delete`, `email.dkim.regenerate`
- API: `/api/v1/mailboxes`, `/api/v1/forwarders`, `/api/v1/dkim`
- UI: user mailbox list/create, forwarder list/create; admin view across users

**Status:** Planned

**Depends on:** M4 (DNS MX records)

### M7: Databases (MariaDB + optional Postgres) (PLANNED)

**Goal:** Users can create and manage databases + database users with grant tables.

**Deliverables:**
- New migrations: `create_databases.sql`, `create_database_users.sql`, `create_database_user_grants.sql`
- Database CRUD (name, engine: mariadb|postgres)
- Database user CRUD (username, password)
- Grant management (SELECT, INSERT, UPDATE, DELETE, CREATE, ALTER, DROP, etc.)
- phpMyAdmin SSO link (per-user session token for admin panel access)
- Agent commands: `db.create`, `db.drop`, `db_user.create`, `db_user.drop`, `db_user.grant`, `db_user.revoke`
- API: `/api/v1/databases`, `/api/v1/database-users`, `/api/v1/sso/phpmyadmin`
- UI: user database list/create, user list/create, grant table UI; admin view

**Status:** Planned

**Depends on:** M1 (users exist)

### M8: Cron (PLANNED)

**Goal:** Users can create allow-listed cron jobs without security risk.

**Deliverables:**
- New migration: `create_cron_jobs.sql` (command, schedule via cron expression)
- Cron CRUD (per-user allow-list of safe commands)
- Cron command validator (allowlist: WordPress wp-cron, custom PHP scripts in doc root, etc.)
- Agent command: `cron.apply` (generates crontab file, reloads cron service)
- API: `/api/v1/cron`, `/api/v1/cron/{id}`
- UI: user cron list/create with command validator

**Status:** Planned

**Depends on:** M2 (domains/docroots exist)

### M9: PHP/FPM pool manager (PLANNED)

**Goal:** Admins can create per-user PHP-FPM pools with configurable runtime settings.

**Deliverables:**
- New migration: `create_php_pools.sql` (pool name, pm mode, max_children, php_version)
- PHP pool CRUD (per user or per domain)
- php.ini overrides (memory_limit, max_execution_time, upload_max_filesize, etc.)
- Agent command: `php.apply_pool` (generates /etc/php/*/fpm/pool.d/pool.conf, reloads fpm)
- API: `/api/v1/php-pools`, `/api/v1/php-pools/{id}/ini-overrides`
- UI: admin PHP pool list/create; user PHP override request form (admin approves)

**Status:** Planned

**Depends on:** M1 (users exist)

### M10: WordPress (PLANNED)

**Goal:** Admins and users can install, delete, and clone WordPress instances.

**Deliverables:**
- New migration: `create_wordpress_installs.sql` (domain_id, db_id, status, version)
- WordPress CRUD (install, delete, clone from existing)
- Automated install script (WordPress core + wp-cli, database setup, admin user, theme+plugin config)
- Clone operation (copy docroot, backup/restore database, new domain binding)
- Health check (HTTP 200 on domain, wp-cli site health)
- Agent command: `wordpress.install`, `wordpress.delete`, `wordpress.clone`
- API: `/api/v1/wordpress`, `/api/v1/wordpress/{id}/clone`
- UI: user WordPress install/delete/clone buttons; admin WordPress list across users

**Status:** Planned

**Depends on:** M2 (domains), M7 (databases)

### M11: FileBrowser (PLANNED)

**Goal:** Users can browse and manage files via a web UI (upload, delete, rename, edit text).

**Deliverables:**
- New migration: `create_file_operations_log.sql` (user, file, action, timestamp)
- FileBrowser adapter pattern (SFTP, local filesystem, S3, etc.)
- Path sanitizer (prevent directory traversal)
- Soft-delete for trash (recover within 30 days)
- Agent command: `fs.list`, `fs.upload`, `fs.delete`, `fs.rename`, `fs.read`, `fs.write`, `fs.trash.list`, `fs.trash.restore`
- WebSocket log stream (tail -f for file uploads/downloads)
- API: `/api/v1/files`, `/api/v1/files/{id}/content`, `/api/v1/trash`
- UI: user file browser with drag-drop upload, context menu (delete/rename/edit)

**Status:** Planned

**Depends on:** M1 (users)

### M12: Stats & monitoring (PLANNED)

**Goal:** Admins can view server resource usage (CPU, memory, disk, bandwidth) and per-domain
  traffic stats in real-time.

**Deliverables:**
- New migration: `create_metrics_snapshots.sql` (timestamp, cpu_usage, memory_usage, disk_usage)
- GoAccess WebSocket tail (parse nginx logs in real-time, emit JSON to frontend)
- Bandwidth meter (parse nginx access log, sum bytes_sent per domain)
- Server metrics (CPU, memory, disk) via agent.system_info
- In-process worker goroutine (pushes metrics every 10s to WebSocket hub)
- API: `/ws/server-stats` (WebSocket), `/api/v1/metrics` (historical), `/api/v1/domains/{id}/traffic`
- UI: admin dashboard with real-time charts (Chart.js or similar)

**Status:** Planned

**Depends on:** M2 (nginx vhosts)

### M13: Notifications (PLANNED)

**Goal:** Admins can configure and receive alerts via email, Slack, Discord, webhook.

**Deliverables:**
- New migrations: `create_notification_channels.sql`, `create_webhook_endpoints.sql`, `create_notification_history.sql`
- Notification channel CRUD (email, Slack, Discord, webhook)
- Admin broadcast notification (system events: domain expiry, certificate renewal, disk full)
- Per-channel template customization
- Notification history log (audit trail)
- Agent command: `notifications.send` (if needed for system-level alerts)
- API: `/api/v1/notification-channels`, `/api/v1/notifications/broadcast`
- UI: admin notification channel config; notification center

**Status:** Planned

**Depends on:** M6 (email channel requires Stalwart)

### M14: Migration importers (PLANNED)

**Goal:** Admins can import domains, users, and DNS from cPanel, DirectAdmin, HestiaCP, WHM.

**Deliverables:**
- New migrations: `create_migration_jobs.sql` (source, status, progress, error_log)
- Per-source backup parser (cPanel tar.gz, DirectAdmin backup.json, HestiaCP, WHM SQL dump)
- Analyze stage (extract users, domains, databases, DNS zones)
- Fix-permissions stage (regenerate nginx vhosts, reset database privileges)
- Validate stage (syntax check DNS, test HTTP on domains)
- Restore stage (insert into Jabali schema, call agent commands to create vhosts)
- Rollback on error (cascade-delete imported data, preserve rollback export)
- Agent command: `migration.import`, `migration.rollback`
- API: `/api/v1/migrations/import`, `/api/v1/migrations/{id}/status`, `/api/v1/migrations/{id}/rollback`
- UI: admin migration import wizard (upload backup, preview, confirm, monitor progress)

**Status:** Planned

**Depends on:** M2 (domains), M7 (databases), M10 (WordPress)

### M15: Automation API (PLANNED)

**Goal:** External tools (e.g., Terraform, Ansible) can provision resources via scoped HMAC
  tokens without sharing the admin password.

**Deliverables:**
- New migration: `create_api_tokens.sql` (name, secret, resource_scopes, rate_limit, expires_at)
- Token CRUD (admin can create/revoke, token lists own permissions)
- HMAC-SHA256 signing scheme (request body → signature in Authorization header)
- Rate limiter per token (configurable burst)
- Scoped resources (e.g., token can only manage domain X, or only read DNS)
- Agent command: none (RPC is internal; tokens are HTTP-only)
- API: `/api/v1/api-tokens`, `/api/v1/automation/*` (scoped endpoints)
- UI: admin API token management (copy-once secret display, audit log)

**Status:** Planned

**Depends on:** M2

### M16: Diagnostic reports (PLANNED)

**Goal:** Admins can export server state as an RSA-encrypted tarball for support.

**Deliverables:**
- New migration: `create_diagnostic_reports.sql` (created_at, expires_at, file_hash)
- Report generator (collect: database schema, nginx vhosts, DNS zones, service logs, agent version, Go version)
- RSA encryption (public key in panel, support team decrypts with private key)
- Time-limited downloads (report auto-expires after 7 days)
- Agent command: `diagnostics.collect` (logs, config, status)
- API: `/api/v1/diagnostics/generate`, `/api/v1/diagnostics/{id}/download`
- UI: admin diagnostics page with one-click export button

**Status:** Planned

**Depends on:** M1 (admin access)

---

## 7. Configuration

### Environment variables (`.env`)

```bash
DATABASE_URL=mysql://root:password@127.0.0.1:3306/jabali_panel
AGENT_SOCKET=/run/jabali/agent.sock
AGENT_TIMEOUT=5s
PANEL_ADDR=0.0.0.0:8443
JWT_SECRET=<base64-encoded 32-byte key>
RECONCILER_INTERVAL=30s
```

### Config file (`config.toml` or via env)

```toml
[panel]
port = 8443
addr = "0.0.0.0:8443"
jwt_secret = "..."

[agent]
socket = "/run/jabali/agent.sock"
timeout = "5s"

[reconciler]
interval = "30s"
enabled = true

[database]
url = "mysql://..."
```

### Server settings (DB-backed)

All read/write via `/api/v1/system/settings`:
- `hostname` — server's FQDN
- `ns1` — primary nameserver IP (for DNS SOA)
- `ns2` — secondary nameserver IP
- `public_ipv4` — auto-detected public IPv4
- `public_ipv6` — auto-detected public IPv6

---

## 8. Security posture

**Key principles** (see `~/.claude/rules/security.md` for global standards):

1. **No hardcoded secrets** — all via environment variables or config files (not in code)
2. **Input validation** — every user input validated at API boundaries
3. **SQL injection prevention** — GORM parameterized queries throughout
4. **Authentication** — JWT tokens (access + refresh), bcrypt passwords
5. **Authorization** — RBAC middleware (RequireAdmin, RequireOwner) on protected routes
6. **Agent argument sanitization** — all agent command arguments escaped before passing to shell
7. **Rate limiting** — per-IP on auth endpoints, per-token on automation API (planned M15)
8. **Error messages** — never leak sensitive data (database names, file paths, internal IPs)

See ADRs for architectural security decisions:
- ADR #0001 (agent socket never over network)
- ADR #0002 (DB is truth, not config files)
- ADR #0003 (one write path = API only)

---

## 9. Testing

**Go backend:**
- Unit tests: `go test ./...` (GORM models, auth, reconciler)
- Integration tests: sqlite in-memory DB for agent mocks
- Coverage: `go test -cover ./...` (target 80%+)
- Linter: `golangci-lint run`

**React frontend:**
- Unit tests: Vitest (component logic, hooks)
- E2E tests: Playwright (user flows: login, domain create, DNS records)
- Build: `npm run build`

**CI/CD:**
- GitHub Actions (if available; push to main runs tests + build)
- Pre-commit hooks (local lint, type check)

---

## 10. Where to add new features

Use this table to navigate the codebase when adding a new capability:

| Capability | Database | Agent Command | API Handler | UI Page | CLI Subcommand |
|------------|----------|---|---|---|---|
| New entity (e.g., SSL certs) | Migration `000NNN_*.sql` | `ssl.issue`, `ssl.revoke` | `panel-api/internal/api/ssl.go` | `panel-ui/src/shells/SslPage.tsx` | `jabali ssl` (future) |
| New system config | `server_settings` table | `system.set_*` | `/api/v1/system/settings` | `ServerSettingsPage` | `jabali config` (future) |
| New service integration | New table + relations | `service.start`, `service.stop` | `/api/v1/services/{name}` | `ServicesPage` (planned) | `jabali service` (future) |
| New reconciler concern | Domain/zone/record FK | `domain.configure`, etc. | Auto-triggered via `/api/v1/reconcile/*` | — | — |

**Pattern for new entity:**

1. Create migration: `panel-api/internal/db/migrations/000NNN_*.sql`
2. Create GORM model: `panel-api/internal/models/*.go`
3. Create repository: `panel-api/internal/repository/*.go`
4. Create API handler: `panel-api/internal/api/myentity.go` (CRUD routes)
5. Register routes in Gin: `panel-api/internal/server/routes.go`
6. Create agent command (if needed): `panel-agent/internal/commands/myentity_*.go`
7. Register command in agent: `panel-agent/internal/commands/registry.go`
8. Create UI page/component: `panel-ui/src/shells/MyEntityPage.tsx`
9. Add Refine resource: `panel-ui/src/resources/` (if CRUD via REST)
10. Add WebSocket broadcast (if real-time updates needed): `panel-api/internal/websocket/hub.go`

---

## 11. Changelog

| Milestone | Shipped | Anchor commit |
|-----------|---------|---|
| M1: Foundations | 2026-01-XX | Earlier commits (not enumerated) |
| M2: Domain lifecycle + Nginx + Redirects | 2026-02-XX | `fb3abea`, `0e92a04` |
| M3: Server Settings + Hostname + IPs | 2026-03-XX | `6b5dea4`, `ab47b7d` |
| M4: DNS zones + records + Secondary NS | 2026-04-16 | `f7464a2`, `4b87395` |
| M5: SSL / Let's Encrypt (core + resilient ACME) | 2026-04-17 | `ba54273`, `66ae6d2`, `34379db`, `786079a`, `fc8ff7d` |
| M5a: Admin Impersonation | 2026-04-17 | `7bc292f`, `5b14b4c` |
| M5b: Break-Glass CLI Login | 2026-04-17 | `c587144` |
| M6: Email (Stalwart) | Planned | — |
| M7: Databases | Planned | — |
| M8: Cron | Planned | — |
| M9: PHP/FPM pools | Planned | — |
| M10: WordPress | Planned | — |
| M11: FileBrowser | Planned | — |
| M12: Stats & monitoring | Planned | — |
| M13: Notifications | Planned | — |
| M14: Migration importers | Planned | — |
| M15: Automation API | Planned | — |
| M16: Diagnostic reports | Planned | — |

---

**Last updated:** 2026-04-17
