# Jabali Panel — Feature Blueprint

A complete catalogue of what Jabali Panel does, how the pieces fit together, and
which code owns each capability. Use this as a map when onboarding, scoping new
features, or deciding where existing logic already lives.

> **Sources of truth.** This blueprint reflects what's shipped on `main` as of
> 2026-04-19. Architecture rules and conventions live in `docs/adr/`. Cross-repo
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
strategy, fallback to 365-day self-signed certificates on ACME failure, exponential backoff retry
scheduling (5m → 15m → 45m → 135m, capped at 4h), manual retry endpoint, SSL Manager UI pages (admin + user),
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

**Shipped:** Admin impersonation (one-shot login URL model — admin generates a temporary login link that opens in a new tab with no persistent session), 
break-glass CLI login (`jabali-panel admin login` and `jabali-panel user login` CLI subcommands that mint short-lived login URLs), 
sessionStorage-based impersonation (survives page reload within the tab but dies when browser session closes).

**M5a: Admin Impersonation (refactored)**
- `POST /admin/users/:id/impersonate` returns `{ "login_url": "<url>" }` with 5-minute JWT (purpose=cli_login, impersonated_by set)
- Admin's browser opens the login URL in a new tab via `window.open(login_url, '_blank')`
- New tab auto-redeems via `POST /auth/cli-login`, sets `sessionStorage.no_refresh=1` and in-memory access token (1h TTL)
- Impersonation tab has no refresh cookie — session dies cleanly when access token expires
- No impersonation banner; impersonation tab looks like a normal user session
- Exit = close the tab. No separate exit endpoint.
- Nested impersonation not supported (cannot impersonate an admin, cannot impersonate while already impersonating)

**M5b: Break-Glass CLI Login (refactored)**
- `jabali-panel admin login [--email <e>]` mints 15-minute JWT with purpose="cli_login" (no impersonated_by)
- `jabali-panel user login <email-or-id>` mints 5-minute JWT with purpose="cli_login" and impersonated_by="cli" (rejects admins)
- Both print redeemable login URLs with embedded JWT as `?cli_token=...`
- `POST /auth/cli-login` endpoint validates JWT: if impersonated_by is set, returns access-only (no refresh); otherwise returns access + refresh (full admin session)
- Audit logging for all CLI-initiated logins

- **Files:** `panel-api/internal/api/auth.go`, `panel-api/internal/api/auth_cli_login.go`, `panel-api/cmd/server/admin_login.go`, `panel-api/cmd/server/user_login.go`, `panel-api/internal/auth/service.go`
- **Migrations:** `000016_add_impersonated_by_to_refresh_tokens.sql`
- **API:** `/admin/users/{id}/impersonate` (POST, returns login_url), `/auth/cli-login` (POST, takes cli_token)
- **CLI commands:** `jabali-panel admin login [--email]`, `jabali-panel user login <email-or-id>`
- **UI:** UserImpersonateAction button in admin users list; calls impersonate endpoint and opens login_url in new tab
- **Frontend:** Login.tsx auto-redeems cli_token and sets sessionStorage.no_refresh flag for impersonation tabs
- **Evidence:** Commits `5324198`, `25435a7`, `623a99f`, `5241f02`, `a1817c1`
- **Related ADRs:** ADR-0015 (admin-impersonation-jwt-claim), ADR-0016 (break-glass-cli-admin-login)

### 4.9 Reconciler

**Shipped:** In-process goroutine inside panel-api that listens for domain DB changes,
issues agent commands to create/update/delete domains on agent, reconciles DNS zones
to PowerDNS MySQL backend, reconciles SSL certificates (try-ACME-first, fallback to self-signed),
runs every 30s by default (configurable), manual trigger endpoints for admin. 
Separate 1-minute ticker processes SSL ACME retries (certs with next_retry_at <= now).

- **Files:** `panel-api/internal/reconciler/reconciler.go`
- **API:** `/api/v1/reconcile/all` (admin-only), `/api/v1/reconcile/{domainID}` (admin-only)
- **Background tickers:** Main reconciler (30s), SSL retry ticker (1m)
- **Evidence:** Commits `cb4ae39`, `0ae7213`, `3037db1`, `ba54273`

### 4.9.1 Frontend UI Chrome Refactor

**Shipped:** Admin and user shells now use Refine's `<ThemedLayoutV2>` + `<ThemedSiderV2>` + `<ThemedTitleV2>` 
components out of the box (light-background default Refine theme). Per-shell sidebar filtering via 
`meta.shell: "admin" | "user"` on resources list in `App.tsx` — sidebar items render only for matching shell context.

- **Files:** `panel-ui/src/App.tsx` (resource meta), `panel-ui/src/shells/AdminLayout.tsx`, `panel-ui/src/shells/UserLayout.tsx`
- **Evidence:** Commit `e9fc63c`

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
15. `000015_create_ssl_certificates.sql` — ssl_certificates table (cert_path, key_path, issued_at, expires_at, status, last_error, renewal_count, staging, created_at, updated_at)
16. `000016_add_impersonated_by_to_refresh_tokens.sql` — impersonated_by FK (tracks admin impersonation trail)
17. `000017_ssl_enabled_default_true.sql` — SSL enabled by default (=1) on new domains
18. `000018_add_next_retry_at_to_ssl_certificates.sql` — next_retry_at timestamp + retry_count INT for exponential backoff retry scheduling
19. `000028_create_php_pools.sql`, `000029_create_php_pool_ini_overrides.sql`, `000030_add_php_pool_id_to_domains.sql` — M9 PHP/FPM pool manager tables + domain FK

### 4.10 PHP/FPM pools (M9)

**Shipped:** Per-user PHP-FPM pools via Sury multi-version install, pool CRUD (admin), per-domain PHP version binding (user), ini overrides (allowlisted directives), reconciler ensures default pool per user + applies pending changes, nginx vhost renders PHP block from `domain.php_pool_id`.

**Placement:** see ADR-0025 for per-user systemd slice design (in-flight via plans/per-user-systemd-slices.md).

**Files:**
- API: `panel-api/internal/api/php_pools.go`, `domain_php_pool.go`
- Models: `panel-api/internal/models/php_pool.go`, `php_pool_ini_override.go`
- Repository: `panel-api/internal/repository/php_pool_repository.go`, `php_pool_ini_override_repository.go`
- Agent: `panel-agent/internal/commands/php_pool_apply.go`, `php_pool_remove.go`, `php_version_list.go`
- Install: `install/php/jabali-php-pool.conf.tmpl`
- UI: `panel-ui/src/shells/admin/php-pools/PHPPoolsList.tsx`, `PHPPoolEdit.tsx`; `DomainSettingsButton.tsx` (PHP section)

**Migrations:** `000028_create_php_pools.sql`, `000029_create_php_pool_ini_overrides.sql`, `000030_add_php_pool_id_to_domains.sql`

**Agent commands:** `php.pool.apply`, `php.pool.remove`, `php.version.list`

**API endpoints:**
- `GET /api/v1/php-pools` (admin: all; user: self)
- `GET /api/v1/php-pools/:id`
- `POST /api/v1/php-pools` (admin: all; user: self)
- `PUT /api/v1/php-pools/:id` (admin: all; user: self)
- `DELETE /api/v1/php-pools/:id` (admin only)
- `GET /api/v1/php-pools/:id/ini-overrides`
- `POST /api/v1/php-pools/:id/ini-overrides`
- `PUT /api/v1/php-pools/:id/ini-overrides/:override_id`
- `DELETE /api/v1/php-pools/:id/ini-overrides/:override_id`
- `POST /api/v1/domains/:id/php-pool` (accepts `pool_id` or `php_version`)
- `DELETE /api/v1/domains/:id/php-pool`
- `GET /api/v1/php/versions`

**ADR:** ADR-0023 (13 decisions: per-user pools, reconciler default, admin-only deletion, domain binding, ini allowlist, version immutability, uid collision handling, etc.)

---

### 4.10.2 PHP extensions management (M9.6)

**Shipped:** Server-wide extension management per PHP version. Admin PHP page now has two tabs: the existing "PHP Versions" (install/reload/set-default) and a new "PHP Extensions" (pick a version → install/remove/enable/disable any of 63 allowlisted extensions). No per-user or per-pool scope — changes affect every `php<v>-fpm` master and every `jabali-fpm@<user>` pinned to `<v>`.

**State model:** live from dpkg + `/etc/php/<v>/fpm/conf.d/*.ini` on every list call. No migration, no DB persistence, zero drift risk.

**Allowlist:** 63 entries at `internal/phpext/phpext.go`, shared by panel-api + panel-agent. Built-ins (opcache, posix, pdo, mysqlnd, etc.) expose only enable/disable. Bundled packages (xml family → `php<v>-xml`) are collapsed by the resolver. The `mysql` entry is a meta-install for `php<v>-mysql` (mysqli + pdo_mysql); enable/disable rejected as ambiguous.

**Files:**
- Allowlist + resolver: `internal/phpext/phpext.go`
- Agent: `panel-agent/internal/commands/php_ext_list.go`, `php_ext_apply.go`, `php_ext_shell.go`
- API: `panel-api/internal/api/php_extensions.go`
- Contract lock: `panel-api/internal/agent/php_ext_contract_test.go` + `testdata/*.json` (6 golden fixtures, round-tripped)
- UI: `panel-ui/src/shells/admin/php/PHPVersionsPage.tsx` (Tabs container), `VersionsTab.tsx` (renamed from `PHPPoolsList.tsx`), `PHPExtensionsTab.tsx`

**Agent commands:** `php.ext.list {version} → {version, extensions: [{name, installed, enabled, built_in}]}`, `php.ext.apply {version, ext, action} → {version, ext, installed, enabled}` where action ∈ `{install, remove, enable, disable}`. Serialized by `aptMu sync.Mutex` around apt calls.

**API:**
- `GET  /api/v1/admin/php/versions/:version/extensions`
- `POST /api/v1/admin/php/versions/:version/extensions/:ext/apply` (body `{"action":"install|remove|enable|disable"}`)

**Wire addition:** `agentwire.CodeFailedPrecondition` + 409 Conflict mapping in `translateAgentError` — used for "version not installed", "ext not installed for enable", "shared-package remove conflict", "apt/phpenmod non-zero exit".

**ADR:** [ADR-0031](adr/0031-php-extensions-management.md)
**Runbook:** [docs/runbooks/php-extensions.md](runbooks/php-extensions.md)
**Plan:** [plans/php-extensions-tab.md](../plans/php-extensions-tab.md)

---

### 4.11 Per-user systemd slices (M9.5)

**Shipped:** Every hosting user now runs inside a nested systemd slice — PHP-FPM master, login shells, and systemd-user timers all land in the same cgroup. The distro's global `php<v>-fpm.service` is stopped, disabled, and masked after cutover.

**Hierarchy:**
```
jabali.slice
└─ jabali-user.slice              (MemoryHigh=80% of host RAM)
   └─ jabali-user-<user>.slice    (per-user container)
      ├─ jabali-fpm@<user>.service (PHP-FPM master as <user>)
      └─ user@<uid>.service       (login manager for shells + timers)
```

**Files:**
- Systemd units: `install/systemd/jabali.slice`, `jabali-user.slice`, `jabali-fpm@.service`
- Shims: `install/systemd/fpm-pre-start`, `fpm-exec` (run as root via `+` prefix for setup, FPM itself as `<user>`)
- Agent: `panel-agent/internal/commands/user_slice_ensure.go`, `user_slice_remove.go`, `user_slice_status.go`
- Reconciler: `panel-api/internal/reconciler/reconciler.go` (writeHealthcheckForDomain backfill, user-slice provisioning)
- Admin CLI: `panel-api/cmd/server/admin_slice_cutover.go` (`jabali-panel admin slice-cutover [--dry-run]`)
- UI: `panel-ui/src/shells/admin/users/UserSliceStatus.tsx`

**Agent commands:** `user.slice.ensure`, `user.slice.remove`, `user.slice.status`

**API endpoints:**
- `GET /api/v1/admin/users/:id/slice-status` (admin-only; returns memory/tasks/active state)

**Runtime paths:**
- Socket: `/run/php/jabali-<user>/fpm.sock` (owner=`<user>`, group=`www-data`, mode 0660)
- PID file: `/run/php/jabali-<user>/fpm.pid`
- Per-user FPM config: `/etc/jabali-panel/fpm/<user>.conf`
- Version pin: `/etc/jabali-panel/user-phpver/<user>`
- Healthcheck: `/home/<user>/<domain-docroot>/jabali-healthcheck.php` (probed by cutover)
- Runtime dir mode: `0750 <user>:www-data` (user writes, nginx traverses)

**Admin command:**
- `jabali-panel admin slice-cutover [--dry-run]` — preflight every user has an active FPM master, mask global `php<v>-fpm.service`, probe `jabali-healthcheck.php` through nginx, auto-rollback on probe failure.

**Linger:** `user.slice.ensure` calls `loginctl enable-linger <user>` so the user manager persists across logouts; without it, the `user@<uid>.service.d/jabali.conf` drop-in doesn't take effect until next login.

**ADR:** [ADR-0025](adr/0025-per-user-systemd-slices.md) — amends ADR-0023's placement decision.

**Runbook:** [docs/runbooks/per-user-slices.md](runbooks/per-user-slices.md) — what's captured, what's not (traditional crontabs), cron → systemd-user-timer migration recipe, troubleshooting.

**Plan:** [plans/per-user-systemd-slices.md](../plans/per-user-systemd-slices.md) (9 steps, all shipped).

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
- Fallback to 365-day self-signed if ACME fails
- Exponential backoff retry scheduling: retry_count 1→5m, 2→15m, 3→1h, 4+→4h (capped)
- next_retry_at timestamp + retry_count tracking in ssl_certificates table
- Max 20 retries; mark cert as permanently failed if exceeded
- Manual retry endpoint: `/api/v1/domains/:id/ssl/retry` (admin-only)
- Agent command: `ssl.self_sign` (stopgap emergency fallback)
- Reconciler: syncs SSL state for all domains every 30s, respects retry backoff

**Status:** Shipped (commits `ba54273`, `66ae6d2`, `34379db`, `786079a`, `fc8ff7d`)

**Depends on:** M2 (domains must exist)

### M5a: Admin Impersonation (SHIPPED)

**Goal:** Admins can generate temporary login links to access a user's panel without knowing their password.

**Deliverables:**
- `POST /admin/users/{id}/impersonate` endpoint — returns `{ "login_url": "<url>" }` with 5-minute JWT (purpose=cli_login, impersonated_by set)
- Admin's browser opens the login URL in new tab via `window.open(login_url, '_blank')` — original tab untouched
- Impersonation tab auto-redeems JWT via `POST /auth/cli-login`, sets `sessionStorage.no_refresh=1` for reload resilience
- Impersonation session is 1h access-token-only (no refresh cookie) — dies cleanly on expiry
- No UI banner; impersonation tab looks identical to a normal user session
- Exit = close the tab (no separate exit endpoint)
- Single impersonation per link; cannot nest further

**Status:** Shipped (commits `5324198`, `25435a7`, `5241f02`, `a1817c1`)

**Depends on:** M1 (auth infrastructure)

### M5b: Break-Glass CLI Login (SHIPPED)

**Goal:** Provide emergency admin access and secure user impersonation from the command line.

**Deliverables:**
- `jabali-panel admin login [--email <e>]` CLI subcommand — mints 15-minute JWT (purpose=cli_login, no impersonated_by), prints login URL
- `jabali-panel user login <email-or-id>` CLI subcommand — accepts email or ULID, rejects admins, mints 5-minute JWT (purpose=cli_login, impersonated_by=cli), prints login URL
- `POST /auth/cli-login` endpoint — validates JWT, branches on impersonated_by: if set → access-token-only (1h, no refresh); if empty → full session (access + refresh cookie)
- `Purpose` claim validation: middleware rejects any token with Purpose field set when used as normal Bearer token (prevents cli_login tokens from accessing protected routes directly)
- Audit logging for all CLI-initiated logins
- No environment variables (CLI_LOGIN_SECRET removed)

**Status:** Shipped (commits `c587144`, `25435a7`, `623a99f`, `5241f02`)

**Depends on:** M1 (auth infrastructure)

**Related ADR:** ADR-0016 (break-glass-cli-admin-login.md)

### M5c: Two-factor authentication (TOTP + backup codes) (SHIPPED)

**Goal:** Password + TOTP (RFC 6238) for every login, 10 single-use bcrypt-hashed backup codes per user, admin-only break-glass CLI escape hatch.

**Deliverables:**
- Migration `000041_add_2fa_totp`: `users.totp_{secret_encrypted,enabled,enabled_at}` + `totp_backup_codes` table
- `internal/twofa` service: enrolment via `github.com/pquerna/otp/totp`, 10×8-digit backup codes, bcrypt (cost 12), constant-time compare helper
- Secret at rest: AES-256-GCM via existing `internal/ssokey` (no new key material, same rotation surface)
- Login flow: when `totp_enabled=true`, `/auth/login` returns `{twofa_pending: true, twofa_pending_token}` with a 5-min JWT (`purpose="2fa_pending"`) instead of access+refresh; client exchanges it at `/auth/2fa/challenge` with a 6-digit TOTP or 8-digit backup code
- API: `POST /api/v1/auth/2fa/{enroll,verify,disable,regen-backup}` under RequireAuth; challenge rides the strict rate limiter
- UI: MyProfile 2FA card (enable/regen/disable modals, AntD `<QRCode>`, backup-codes-shown-once + save-confirmation gate); Login page two-stage state machine (password → challenge, swap to 8-digit backup-code input via "Use backup code" link)
- CLI escape hatch: `jabali admin disable-2fa --email <target>` — shell-access only, no API equivalent; wipes backup codes first, then `DisableTOTP` so a mid-failure still leaves the user unlocked
- Tests: 6 service unit tests, 4 handler tests, 4 login-flow integration tests (real `auth.Service` + real `JWTIssuer`, fakes only storage), 4 UI state tests + 3 Login stage-transition tests, 3 CLI unit tests

**Status:** Shipped (7 commits on `feat-2fa-totp`: `0b68048`, `afc3839`, `009ccf5`, `e2c1f62`, `65536ce`, `27059b1`, `7b1ac16`)

**Depends on:** M1 (auth), M5a (impersonation pattern reused for 2fa_pending JWT Purpose), M5b (CLI admin harness reused for disable-2fa command)

**Out-of-scope (phase 2):** WebAuthn, SMS, push-based 2FA. Impersonation and `jabali admin login` intentionally bypass 2FA — they are the escape valves.

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

### M7: Databases (MariaDB) (SHIPPED)

**Goal:** Users can create and manage databases + database users with grant tables.

**Deliverables:**
- Migrations: `000020_create_databases.sql`, `000021_create_database_users.sql`, `000022_create_database_user_grants.sql`, `000024_add_privileges_to_grants.sql`, `000025_widen_grant_level_enum.sql`, `000027_create_phpmyadmin_sso_tokens.sql`
- Database CRUD (name, engine: mariadb; Postgres deferred per ADR-0018)
- Database user CRUD (username, bcrypt + reveal-once password per ADR-0021)
- Grant management: per-database rw/ro levels (ADR-0019); custom privileges enum via migration 000024/000025
- phpMyAdmin SSO: signon proxy + per-user shadow account over UDS (ADR-0020, ADR-0022)
- Agent commands: `db.create`, `db.drop`, `db_user.create`, `db_user.drop`, `db_user.grant`, `db_user.revoke`, `db_user.rotate_password`, `db.backup`, `db.restore`, `db.size`
- API: `/api/v1/databases`, `/api/v1/database-users`, `/api/v1/sso/phpmyadmin`
- UI: user database list/create, user list/create, grant table UI; admin view with prefix-filtered non-admin list
- Cascade delete: `DELETE /databases/:id` cascades grants (revoke on agent, idempotent) and blocks on active `wordpress_installs` with 409

**Status:** Shipped (ADRs 0018-0022; recent fixes in `b723fe1`, `9ebaad8`, `762c7fe`)

**Depends on:** M1 (users exist)

### M8: Cron (SHIPPED)

**Goal:** Users can create allow-listed cron jobs without security risk, executed via per-user systemd-user timers.

**Deliverables:**
- Per-user systemd-user timers (ADR-0029): service + timer unit files under `/etc/jabali-panel/cron-units/<user>/`
- Cron CRUD (per-user allow-list of safe commands): migration `000026_create_cron_jobs.sql` (name, command, schedule, enabled, last_run_at, last_exit_code)
- Shared cron validator: `/internal/cronvalidate/` (allowlist: `wp`, `wp-cron.php` + PHP scripts in owned docroots; reject shell metacharacters, path traversal, disallowed binaries)
- Agent commands: `cron.apply` (renders unit files), `cron.remove`, `cron.run_now` (immediate execution), `cron.tail_log` (journalctl tail), `cron.list`
- API: `/api/v1/cron[/:id]`, `/api/v1/cron/:id/run-now`, `/api/v1/cron/:id/log`
- Reconciler: `ReconcileCronJobs` (30-second cadence polling systemctl for last_run_at, last_exit_code via ExecMainStatus)
- UI: AntD user cron list page (create, edit, run-now, view log, delete; form validation + server-side rejection)

**Status:** Shipped (ADR-0029; see plans/m8-cron-runbook.md for ops; key commits: `26649a9` migration+model+repo, `79164ea` validator, `1f3df4f` agent cmds, `c40ea01` reconciler+API, `6eefd5a` UI)

**Depends on:** M1 (users exist), M2 (docroots exist)

### M9: PHP/FPM pool manager (SHIPPED)

**Goal:** Admins can create per-user PHP-FPM pools with configurable runtime settings. Users bind domains to pools. Reconciler ensures one default pool per user.

**Deliverables:**
- Migrations: `000028_create_php_pools.sql`, `000029_create_php_pool_ini_overrides.sql`, `000030_add_php_pool_id_to_domains.sql`
- PHP pool CRUD (per user; admin-only deletion per ADR-0023)
- Domain ↔ pool binding (admin explicit pool selection; user version-based lookup)
- php.ini overrides (memory_limit, max_execution_time, upload_max_filesize, etc.; allowlisted directives)
- Agent commands: `php.pool.apply`, `php.pool.remove`, `php.version.list`, `php.version.status`, `php.version.install`, `php.version.reload`
- API: `/api/v1/php-pools[/:id]`, `/api/v1/php-pools/:id/ini-overrides*`, `/api/v1/domains/:id/php-pool`, `/api/v1/php/versions`, `/api/v1/admin/php/versions/{status,/:version/install,/:version/reload}` (admin-only)
- UI: admin PHP versions management page (install/reload per version); admin PHP pool list/edit; domain binding via PHP section in settings
- Reconciler: ensures every user has ≥1 pool, applies pending changes, nginx renders pool's PHP block

**Status:** Shipped (commits `1aaa507` through final commit; admin versions UI shipped separately)

**Depends on:** M1 (users exist), Sury PHP multi-version install (install.sh)

### M10: WordPress (SHIPPED — generalised by M19 Applications Framework)

**Goal:** Admins and users can install, delete, and clone WordPress instances.

**Note:** Superseded as a single-app surface by **M19** (2026-04-19). The WordPress install flow now lives behind the generic `/applications` API and the per-app registry under `panel-api/internal/apps`. The M10 routes (`/wordpress-installs`) and agent commands (`wordpress.install`/`wordpress.delete`/`wordpress.clone`) remain registered through the M19 release window for one-release backwards compatibility; M19.1 deletes them. See ADR-0033.

**Deliverables:**
- New migration: `000033_create_wordpress_installs.sql` (domain_id, db_id, status, version, admin_username, admin_email, locale, last_error)
- WordPress CRUD (install, delete, clone from existing)
- Automated install script (WordPress core + wp-cli, database setup, admin user)
- Clone operation (copy docroot via rsync, backup/restore database, new domain binding, preserve admin credentials)
- Health check endpoint (`POST /wordpress/:id/health`) for status monitoring
- Agent commands: `wordpress.install`, `wordpress.delete`, `wordpress.clone` (with credential-handling invariant and placeholder rewrite for DB password)
- API: `/api/v1/wordpress` (list, create), `/api/v1/wordpress/:id` (get, delete), `/api/v1/wordpress/:id/clone` (POST)
- UI: user WordPress install/delete/clone buttons with live status polling; admin WordPress cross-user list; clone modal with destination domain selection
- Reconciler: WordPress install sweeper (drift detection for rows stuck in `installing`/`cloning`/`deleting` beyond 10min threshold)
- Playwright E2E test (install → clone → verify independent DB → login test → delete)
- Runbook for troubleshooting (stuck installs, failed clones, orphaned DBs, drift recovery)

**Status:** Shipped (commits `85ed8b4` through `main HEAD`)

**Depends on:** M2 (domains), M7 (databases), M9 (PHP-FPM pools for clone version compatibility)

### M11: File Manager (SHIPPED — superseded-and-rebuilt 2026-04-19)

**Goal:** Users can browse and manage files via a web UI scoped to their homedir.

**History:** First attempt integrated the third-party `filebrowser` project behind an SSO proxy. It burned ~a week on architectural mismatches (stateless proxy-auth, in-process user cache, CLI↔DB drift, POSIX ACL choreography). Decommissioned in Wave E. See ADR-0027 for the failed attempt and ADR-0030 for the replacement decision.

**Deliverables (current, AntD-native):**
- Shared `internal/filesafe/` path-safety validator consumed by both panel-api and panel-agent (same cross-boundary pattern as M8 cron's `/internal/cronvalidate/`)
- 7 agent commands: `files.list`, `files.read`, `files.write`, `files.delete`, `files.mkdir`, `files.rename`, `files.stat` — all scoped via filesafe
- 8 REST endpoints under `/api/v1/files`: `GET /`, `GET /tree`, `GET /home`, `GET /download`, `GET /preview`, `POST /upload`, `POST /mkdir`, `POST /rename`, `DELETE /`
- Wire-shape drift guard: `panel-api/internal/api/files_wire_test.go` asserts JSON tags match the agent
- UI: `FileManagerPage.tsx` at `/jabali-panel/files` — Breadcrumb + toolbar top, Tree left, Table right; Upload/NewFolder/Rename/Delete/Preview/Download
- Ownership model: operations run as root in the agent, results `chown`ed to `<user>:www-data` with mode 0640 (files) / 0750 (dirs) — matches deployed per-user FPM model verified on 10.0.3.13

**Phase-2 backlog (out of scope for v1):** drag-and-drop upload, multi-select, image preview, chmod UI, editor (Monaco), zip/unzip, binary-safe read/write (current content is UTF-8 string over JSON), domain-docroot scope (`/var/www/*` alongside `$HOME`), chunked upload above 100 MB.

**Status:** Shipped — Wave A–E committed between 2026-04-18 and 2026-04-19.

**Depends on:** M1 (users), M9.5 (per-user slices for FPM ownership model).

### M12: SFTP access via openssh (SHIPPED)

**Goal:** Users upload SSH public keys in the panel and SFTP into their homedir. Key-only auth, no shell, no chroot.

**Deliverables:**
- SSH key management UI: "SSH Keys" page in user shell (add/delete keys with fingerprint display)
- `ssh_keys` table: id, user_id, name, public_key, fingerprint, created_at
- API CRUD handlers: `POST /api/v1/ssh-keys`, `GET /api/v1/ssh-keys`, `DELETE /api/v1/ssh-keys/{id}`
- Agent commands: `ssh.authorized_keys.write`, `ssh.authorized_keys.delete`, `ssh.user.join_sftp_group`
- Reconciler: ensures all jabali users in `jabali-sftp` group; syncs authorized_keys from DB
- Systemd group: `jabali-sftp` (Match Group in sshd_config)
- Sshd drop-in: `/etc/ssh/sshd_config.d/jabali-sftp.conf` (ForceCommand internal-sftp, no TCP forwarding, no passwords)
- Playright E2E test: `tests/e2e/sftp.spec.ts` (add key → list with fingerprint → duplicate error → invalid error → delete)
- Runbook: `plans/m12-sftp-runbook.md` (manual SFTP verification, troubleshooting auth, group membership, key sync)

**Status:** Shipped (commits aaa0c82…0256773)

**Depends on:** M1 (users), M9 (per-user slices)

### M13: Stats & monitoring (PLANNED)

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

### M14: Notifications (PLANNED)

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

### M15: Migration importers (PLANNED)

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

### M16: Automation API (PLANNED)

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

### M17: Diagnostic reports (PLANNED)

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

### M18: Per-user resource limits (SHIPPED — pending host validation)

**Goal:** Hosting packages become *enforceable* bundles. Admins set disk, CPU, memory, I/O, task, and per-domain request/connection limits on a package; the reconciler converges the system so every hosting user is capped by kernel + filesystem + nginx. Per-user override rows allow one-off tuning without forking packages.

**Deliverables (all landed):**
- Migrations 000042-000044: new columns on `hosting_packages` (cpu/memory/io/tasks), new columns on `domains` (rate_limit_rps, connection_limit), new `user_limit_overrides` table
- POSIX user quota enforcement (ext4/xfs; btrfs/zfs fail loud in install.sh)
- cgroups v2 drop-ins at `/etc/systemd/system/jabali-user-<u>.slice.d/limits.conf` (CPUQuota, MemoryMax, MemoryHigh=90%, TasksMax, IOReadBandwidthMax, IOWriteBandwidthMax)
- nginx `00-jabali-ratelimits.conf` fragment generator + per-vhost `limit_req` / `limit_conn` directives
- Agent commands: `user.limits.apply|report|clear` + `nginx.ratelimits.apply`
- CLI: `jabali limits check|apply|status|package apply [--dry-run]`
- Admin UI: package editor extended with 5 new fields under "Resource limits" divider
- User shell: MyProfileUsageCard with 10s polling, disk/memory Progress bars, effective limits Descriptions
- install.sh: `usrquota` configuration + tmpfs `/tmp` + cgroups v2 probe (idempotent, fail-loud on unsupported FS)
- ADR-0032 + runbook at `plans/m18-resource-limits-runbook.md`

**Status:** Code complete; 7 waves shipped (A-G) 2026-04-19. **Host validation** — the OS-level tests (setquota EDQUOT, OOM-kill, nginx 503 on burst) cannot run from the dev host and are documented in runbook §3 for post-deploy execution on the test VM.

**Depends on:** M9.5 (per-user slices from ADR-0025)

### M19: Applications Framework (SHIPPED — partial)

**Goal:** Generalise the M10 WordPress-specific surface into a Softaculous-style applications framework so DokuWiki, MediaWiki, Joomla, Drupal, phpBB, PrestaShop, Moodle, Nextcloud, Matomo (etc.) can be installed alongside WordPress on a domain or subdirectory using the same plumbing — one model, one repository, one reconciler, one set of API + agent commands keyed by an `app_type` discriminator.

**Deliverables (steps 1–5 + 8 shipped on the `m19/*` branch stack 2026-04-19):**
- Migration `000046_rename_wordpress_installs_to_application_installs.up.sql` — RENAME TABLE + add `app_type VARCHAR(32) NOT NULL DEFAULT 'wordpress'` + flip composite UNIQUE from `(domain_id, subdirectory)` to `(domain_id, subdirectory, app_type)`. Down migration uses `SIGNAL SQLSTATE '45000'` to refuse rollback once any non-WordPress row exists.
- `panel-api/internal/apps/` package: `App` descriptor + `Registry` + `ParamSpec` (typed `string`/`email`/`password`/`enum`/`bool`); WordPress descriptor opting into the catalog; registry validated at registration time + race-clean.
- `panel-api/internal/api/applications.go` — generic `POST/GET /applications`, `GET/DELETE /applications/:id`, `POST /applications/:id/clone`, plus `GET /applications/registry` for the install picker. Validates per-app `params` against the descriptor's `InstallParamSchema`. `RequiresDB=false` short-circuit skips the entire panel-row + agent `db.create / db_user.create / db_user.grant` chain. The legacy `/wordpress-installs` routes stay mounted in parallel (one-release back-compat).
- `panel-agent/internal/commands/app_dispatch.go` — `app.install` / `app.delete` / `app.clone` dispatcher reads `app_type` off the body and forwards to the per-app handler registered via `RegisterAppInstaller(name, handler)`. Each app's handler file owns the registration in its `init()`. Legacy `wordpress.*` agent commands stay registered alongside through M19.1.
- Six cross-boundary contract fixtures + round-trip tests under `panel-api/internal/agent/testdata/app_*.json` per the `feedback_cross_boundary_contracts` lesson.
- UI rename: `panel-ui/src/shells/{user,admin}/wordpress/` → `applications/`, Refine resource `wordpress-installs` → `applications`, sidebar label "WordPress" → "Applications" (`AppstoreAddOutlined`), routes `/jabali-panel/wordpress` → `/jabali-panel/applications` (and same for `/jabali-admin`). Install modal pulls `GET /applications/registry` and renders an "App" Select defaulted to WordPress; lists add an "App" column at column 1.
- ADR-0033 + runbook at `docs/runbooks/applications.md` documenting the registry pattern, common failure modes, and "how to add a new app" walkthrough.

**Deferred (steps 6 + 7 — gated on Step 5 deploy + a UI follow-up that renders form fields from the descriptor's `InstallParamSchema`):**
- Step 6 — DokuWiki descriptor + agent installer (validates `RequiresDB=false` end-to-end; exercises the `enum` `ParamSpec` for the license dropdown).
- Step 7 — MediaWiki descriptor + agent installer (validates the per-app CLI installer pattern via `php maintenance/install.php`; exercises a heavier agent flow than WordPress's wp-cli).

**Status:** API, agent, panel and UI shipped end-to-end for the only registered app (WordPress); the generic surface now carries every WordPress install on the `m19/*` branch stack. The catalog grows to DokuWiki + MediaWiki once the stack lands on `main` and the install modal's per-app field renderer ships.

**Depends on:** M10 (WordPress install plumbing — generalised here), M9 (PHP-FPM pools — required so non-WordPress PHP apps land on the right pool), M7 (databases — required for `RequiresDB=true` apps).

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
| M5c: Two-factor authentication (TOTP + backup codes) | 2026-04-19 | `0b68048` through `7b1ac16` on `feat-2fa-totp` |
| M6: Email (Stalwart) | Planned | — |
| M7: Databases (MariaDB) | 2026-04-17 | ADRs 0018-0022 |
| M8: Cron (SHIPPED) | 2026-04-18 | ADR-0029 |
| M9: PHP/FPM pools | 2026-04-17 | 1aaa507 (ADR), 5dbf471 (shipped) |
| M9.6: PHP extensions tab | 2026-04-19 | ADR-0031; steps in `5e6b2ab`, `8c06612`, `f345cce`, `2c8b3a3` |
| M10: WordPress | 2026-04-18 | `85ed8b4` through `main HEAD` |
| M11: File Manager (AntD-native, superseded filebrowser) | 2026-04-19 | ADR-0030, Waves A–E in `main` |
| M12: SFTP via openssh | 2026-04-18 | `aaa0c82` through `0256773` |
| M13: Stats & monitoring | Planned | — |
| M14: Notifications | Planned | — |
| M15: Migration importers | Planned | — |
| M16: Automation API | Planned | — |
| M17: Diagnostic reports | Planned | — |
| M18: Per-user resource limits | 2026-04-19 | Waves A-G on `main` (`caebe7b` → `aaa6bd0`); ADR-0032; runbook at `plans/m18-resource-limits-runbook.md`; host-level validation pending on test VM |
| M19: Applications Framework (steps 1–5 + 8) | 2026-04-19 | `m19/*` branch stack `733d6b8` → docs commit; ADR-0033; runbook at `docs/runbooks/applications.md`; steps 6 (DokuWiki) + 7 (MediaWiki) deferred behind UI dynamic-field-renderer + deploy gate |

---

**Last updated:** 2026-04-19
