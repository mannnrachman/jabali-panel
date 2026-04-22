# Jabali Panel — Feature Blueprint

A complete catalogue of what Jabali Panel does, how the pieces fit together, and
which code owns each capability. Use this as a map when onboarding, scoping new
features, or deciding where existing logic already lives.

> **Sources of truth.** This blueprint reflects what's shipped on `main` as of
> 2026-04-20 (M16 Wave E). Architecture rules and conventions live in `docs/adr/`. Cross-repo
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

### M6: Email (Stalwart integration) (SHIPPED)

**Goal:** Admins and users can enable email per-domain, create mailboxes
  with per-mailbox quotas, and open Bulwark webmail already logged in
  with one click. Automatic DKIM key generation; MX/SPF/DMARC/DKIM/
  autoconfig DNS records injected on enable.

**Deliverables (v1 scope — ADR-0041 through ADR-0045):**

- Storage: Stalwart v0.16 with RocksDB mail storage (ADR-0041) + MariaDB
  SqlDirectory (ADR-0042) — panel owns `jabali_panel.mailboxes`,
  Stalwart re-reads on every auth (ADR-0045, no cache TTL).
- Migrations:
  - `000054_create_mailboxes` — `mailboxes` table + BEFORE INSERT/UPDATE
    trigger maintaining `email_cached = local_part || '@' || domain.name`.
  - `000055_dns_records_add_managed_by` — `dns_records.managed_by` column
    so `domain.email_disable` only tears down M6-owned rows, leaving
    M4 bootstrap + user-edited records alone.
  - `000056_mailbox_sso` — `mailboxes.password_enc` VARBINARY(512) +
    `mailbox_sso_tokens` table for one-shot webmail SSO.
- Per-domain lifecycle: `jabali domain email-enable <domain>` (Ed25519
  DKIM keypair under `/etc/jabali-panel/dkim/`, DKIM+autoconfig+
  autodiscover DNS rows, Stalwart reload) and `jabali domain email-disable`
  (reload Stalwart, delete M6 DNS rows, keep DKIM key per ADR-0043).
- Mailbox CRUD: `jabali mailbox {list,create,delete,set-quota,passwd}`
  — bcrypt hash for Stalwart auth + ssokey-sealed plaintext for webmail
  SSO, both written atomically.
- Panel-API: `GET/POST/DELETE /domains/:id/email`, `GET/POST /domains/:id/mailboxes`,
  `PATCH/DELETE /mailboxes/:id`, `POST /mailboxes/:id/rotate-password`,
  `POST /mailboxes/:id/sso` (mint) + `GET /sso/webmail?token=…` (landing).
- Agent commands: `mailbox.{create,delete,set_quota,set_password,usage}`,
  `domain.email_{enable,disable}`, `webmail.vhost_{apply,remove}`.
- Webmail: Bulwark v1.4.14 (Next.js standalone, JMAP-native) behind
  per-domain `mail.<domain>` nginx vhost. Reconciler toggles the
  vhost based on `domains.email_enabled`. Browser-perspective JMAP
  travels `/jmap` same-origin; Bulwark proxies Stalwart admin via
  `STALWART_API_URL=http://127.0.0.1:8446` internally.
- Per-mailbox SSO: "Webmail" action on every mailbox row mints a
  single-use SHA-256-hashed token (5-minute TTL), landing endpoint
  on `mail.<domain>/sso/webmail` decrypts `password_enc` via
  `sso.key`, POSTs Bulwark `/api/auth/session`, forwards the session
  cookie onto its own 303 to `/`.
- UI: Email tab on DomainEdit with live DNS-record status tags (ok /
  missing / conflict), Mailboxes tab under DomainEdit (admin) +
  dedicated `/jabali-panel/mailboxes` page (user shell).

**Out of scope (deferred):**
- Per-user Sieve rules UI (M6.1)
- CalDAV/CardDAV (separate milestone)
- Catch-all + forwarder management (no `mail_forwarders` table yet)
- Cluster mode / secondary MX failover
- Automatic DKIM rotation (M6.1)
- ACME `mail.<domain>` SAN expansion (M6.1; v1 reuses main domain cert)
- IMAP migration importer (deferred to M15 per ADR-0044)

**Status:** Shipped — ADRs 0041-0045 on `m6/email-stalwart` branch.

**Depends on:** M4 (DNS zones for autoconfig), M7 (shadow-password
pattern + `sso.key` reused for mailbox SSO), M20 (Kratos for panel
auth flow).

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

### M16: Identity Federation + Automation API (ROLLED BACK 2026-04-21)

**Status:** Rolled back due to PKCE incompatibility with the auto-installed WordPress OpenID Connect Generic plugin (v3.11.3), which lacks PKCE support despite the OAuth 2 RFC 6749 requirement. Instead of relaxing Hydra's security posture or forking the plugin, the panel now uses **M22 magic-link** for WordPress SSO — a simpler token-exchange pattern that doesn't require standards federation. Automation API (Wave F) is deferred to a fresh decision when the need is clear. Full rationale and rollback steps: `docs/adr/0038-m16-rollback.md`.


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

### M18: Per-user resource limits (SHIPPED 2026-04-21)

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

**Status:** Code complete; 7 waves shipped (A-G) 2026-04-19. **Host validation** — the OS-level smoke tests (cgroups drop-in matches kernel state, OOM-kill, CPU throttling, nginx 503 on burst) executed against the test VM 2026-04-21 and all pass. Disk-quota enforcement is disabled on /home-on-root hosts (install.sh deliberately skips usrquota there — quota-on-root is unsafe). Four bugs surfaced during validation and were fixed in the same pass: (1) `jabali limits check` nginx-module probe false-negated on stock Debian (false "not compiled in" report; default-on modules aren't listed by `nginx -V`); (2) `serve.go` + `jabali limits {status,apply}` CLI passed `QuotaMount=/` on /home-on-root hosts, aborting the whole apply pipeline before cgroups drop-ins got written; (3) runbook §3 used `su - testuser` for memory/CPU tests — wrong, `su` spawns children in the root session scope not the user slice, so the enforcement never applies. Replaced with `systemd-run --slice=...`. (4) reconciler wrote vhosts referencing `limit_req zone=rl_<id>` before the `00-jabali-ratelimits.conf` fragment declared the zone → `nginx -t` failed with "unknown zone" and domain provisioning aborted for any domain with `rate_limit_rps > 0`. Fixed in `45a0ea6`: `ReconcileNginxRateLimits` now runs BEFORE the domain loop in every entry point (with the existing post-loop call preserved for the reverse N→0 transition); regression test asserts ordering.

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

**Step 5a (UI dynamic field renderer, shipped 2026-04-19):** The install modal now switches its per-app field block based on the picked descriptor's `InstallParamSchema` — `string`/`email`/`password`/`enum`/`bool` types render as the matching AntD control, with `locale`/`language` getting the curated dropdown. Adding an app type with new params (e.g. DokuWiki's `license` enum) requires zero UI code.

**Step 6 (DokuWiki, shipped 2026-04-19):** Second app in the catalog — flat-file PHP wiki, no database. Validates the framework's `RequiresDB=false` short-circuit end-to-end + the `enum` `ParamSpec` (license dropdown). Descriptor at `panel-api/internal/apps/dokuwiki.go`; agent installer + deleter at `panel-agent/internal/commands/dokuwiki_{install,delete}.go`. The installer downloads the upstream stable tarball, verifies SHA-256 against `dokuwikiTarballSHA256 = 1d10e8dc…d090f3` (verified against upstream 2026-04-21), extracts under `systemd-run --uid=<user>`, writes `conf/local.php` + `conf/users.auth.php` (bcrypt admin via `php password_hash`) + `conf/acl.auth.php`, drops `install.lock`, and normalises perms. The `m19/06-app-dokuwiki` branch shipped the panel + agent code.

**Step 7 (MediaWiki, shipped 2026-04-19):** Third app in the catalog — the wiki engine behind Wikipedia. Validates the per-app CLI installer pattern via MediaWiki's `php maintenance/install.php` so the user never sees the web installer wizard. Descriptor at `panel-api/internal/apps/mediawiki.go`; agent installer + deleter at `panel-agent/internal/commands/mediawiki_{install,delete}.go`. The installer pins `MEDIAWIKI_VERSION=1.41.2` (LTS series), downloads from `releases.wikimedia.org/mediawiki/1.41/mediawiki-1.41.2.tar.gz`, verifies SHA-256 against `mediawikiTarballSHA256 = 52bb42c3…b1ce5` (verified against upstream 2026-04-21), extracts under `systemd-run --uid=<user>`, then drives the CLI installer with `--dbtype=mysql --server=<origin> --scriptpath=<subdir> --lang=<lang> --pass=<wikipw>`. The CLI generates `LocalSettings.php` containing the DB credentials in the install root. Bump `mediawikiTarballSHA256` whenever the `MEDIAWIKI_VERSION` constant moves.

**Status:** API, agent, panel, UI, and three apps (WordPress + DokuWiki + MediaWiki) shipped end-to-end on the `m19/*` branch stack. The framework is now exercised by `RequiresDB=true` (WordPress, MediaWiki) AND `RequiresDB=false` (DokuWiki) AND a per-app CLI installer (MediaWiki) AND a config-file generator (DokuWiki) — each axis the plan called out is covered. The catalog grows from here per ad-hoc operator demand without further framework work.

**Depends on:** M10 (WordPress install plumbing — generalised here), M9 (PHP-FPM pools — required so non-WordPress PHP apps land on the right pool), M7 (databases — required for `RequiresDB=true` apps).

### M20: Kratos identity migration (SHIPPED — legacy fully removed)

**Goal:** Replace the hand-rolled JWT + HttpOnly-refresh auth stack with self-hosted Ory Kratos as the identity provider. panel-api is a session-cookie validator; zero custom JWT code in the tree.

**Deliverables (all 9 steps + legacy removal shipped 2026-04-20):**

- `install_kratos()` in `install.sh`: downloads Kratos v26.2.0 (bumped from v1.3.1 in `4c44ef4`; SHA-256 pinned in `install/kratos.sha256`), provisions `jabali_kratos` MariaDB schema, renders `/etc/jabali-panel/kratos.yml` from `install/kratos.yml.tmpl`, runs `kratos migrate sql`, writes `/etc/systemd/system/jabali-kratos.service` under `jabali.slice`, enables + starts. Idempotent.
- nginx `/.ory/` proxy snippet in the panel vhost: same-origin fronting so browsers attach `ory_kratos_session` to every panel request.
- `panel-api/internal/kratosclient/`: `Client` with Whoami (10s LRU cache keyed by SHA-256(cookie)), admin-identity CRUD, bcrypt-passthrough canary, keyset-pagination identity scan.
- Auth middleware: `RequireKratosSession(kratosClient, users)` is the only `/api/v1/*` gate. Resolves Kratos identity UUID → panel ULID via `users.kratos_identity_id` so every downstream ownership check (`db.UserID == claims.UserID`) keeps working. DB is authoritative for `is_admin` (Kratos trait is advisory only).
- API user-create inline hook: `POST /api/v1/admin/users` creates the Kratos identity atomically with the panel row (compensating transaction — ADR-0003 preserved).
- `BootstrapAdmin` Kratos extension: same atomic semantics at boot — first-boot admin gets a Kratos identity in the same call. `auth.KratosIdentityWriter` interface lets tests exercise the rollback paths.
- SPA rewrite: `authProvider.ts` cookie-only, no access token in JS, no 401 refresh interceptor. `pages/Login.tsx` fetches Kratos flow on mount, renders `ui.nodes` as AntD form fields, submits to `flow.ui.action` with CSRF token round-tripped. `kratos.ts` thin typed wrapper over the self-service API. `MyProfile` Security card links out to `/.ory/self-service/settings/browser` for password + 2FA.
- Migration 000046: adds `users.kratos_identity_id VARCHAR(64) UNIQUE` (nullable, MariaDB treats multiple NULLs as distinct).
- Migration 000049: drops `refresh_tokens`, `totp_backup_codes`, and the `totp_*` columns on `users` — no legacy storage remains post-removal.
- Playwright E2E spec: `panel-ui/tests/e2e/kratos-login.spec.ts` covers login + role-based landing + CSRF token round-trip + cold-load whoami-401. `fixtures.ts` gained shared `/.ory/*` mocks so the existing tests keep working.
- ADR-0034 + runbook at `plans/m20-kratos-runbook.md`: design rationale, cutover procedures, day-2 operations (identity list/disable/delete, session revoke, MFA reset, recovery-code generation, Kratos DB loss recovery, split-host TLS).

**Dropped (intentionally):**

- **M5a admin impersonation** — Kratos 1.3.1 OSS doesn't expose an admin-session-minting endpoint. Replaced by Kratos recovery-code flow: admin generates a recovery URL via `/admin/recovery/code`, sends to user, user resets password, admin signs in as them with the temp password, helps, user resets to permanent.
- **M5b break-glass CLI (`jabali admin-login`)** — Operators use `kratos identities list/patch` + `/admin/recovery/code` directly. If Kratos itself is down, restore `jabali_kratos` from `mysqldump`.
- **M5c panel-side 2FA (`/auth/2fa/*`, TOTP secret storage in panel DB, backup codes)** — 2FA now lives entirely in Kratos; users manage TOTP via `/.ory/self-service/settings/browser`.
- **Legacy JWT auth (`/api/v1/auth/{login,refresh,logout}`, `auth.JWTIssuer`, `middleware.RequireAuth`, refresh-token table)** — removed wholesale. No `auth.provider` flag, no `jwt_secret` config, no dual-mode middleware.
- **`jabali kratos-migrate`** — one-shot migration tool dropped after use; fresh installs have no legacy rows to backfill.

**Status:** Cutover 2026-04-20. Custom JWT surface fully removed the same day. No rollback window — ADR-0034 records the decision to compress it once the VM validated green.

**Depends on:** M1 (users table), M7 (MariaDB, reused for Kratos's own schema).
**Blocks:** M16 (Automation API tokens via Hydra — Hydra integrates with Kratos via the login-consent flow).

### M21: Drop Refine (SHIPPED 2026-04-21)

**Goal:** Remove the Refine framework from the panel SPA. After M20 made identity Kratos-native, Refine's `authProvider`/`dataProvider`/`resources`/`<ThemedLayoutV2>` shrank to indirection around calls panel-api already served natively. Wrappers (`<List>`/`<Create>`/`<Edit>`) were injecting chrome (auto headings, breadcrumbs, sticky save bars, card wrappings) the operator had spent a day trying to strip at the call-site — the framework itself was the source.

**Deliverables (all five waves shipped on `m21/drop-refine` 2026-04-21):**

- **Wave A (foundation):** `src/query.ts` (singleton `QueryClient`), `src/hooks/useQueries.ts` (`useListQuery` / `useOneQuery` / `useCreate|Update|DeleteMutation`), `src/hooks/useTableURL.ts` (URL-backed page/sort/q/order state via `useSearchParams`), `src/hooks/useSelectQuery.ts`, `src/auth/{AuthContext,RequireAuth,RequireAdmin,RequireUser}.tsx`. Additive — existing pages untouched.
- **Wave B (shell):** `src/App.tsx` rewritten to `QueryClientProvider > AuthProvider > BrowserRouter > ConfigProvider > Routes`. Drop `<Refine>`, `routerProvider`, `resources[]`, `<ThemedLayoutV2>`, `<ThemedSiderV2>`. `AdminLayout`/`UserLayout` become plain AntD `<Layout>` + `<Sider>` + filtered `<Menu>` + `<Header>` + `<Content>` + `<Footer>`. Single source of menu items at `src/nav.ts`. `JabaliHeader` uses `useAuth().logout` (hard-nav to `/login` after) instead of Refine's `useLogout`.
- **Wave C (admin pages):** mechanical rewrite of every page under `src/shells/admin/*` — users (list/create/edit), packages, domains, DNS, SSL, server settings, PHP pools, databases, database-users. `useTable` → `useTableURL`, `useForm` → `Form.useForm + useCreate|UpdateMutation + useOneQuery`, `<List>/<Create>/<Edit>` wrappers deleted. Also folded a Wave A wire-contract fix: panel-api returns `{data, total, page, page_size}` (not `{items, total}` as the blueprint had assumed), so `useListQuery` now projects `data → items` and serializes camelCase `pageSize` as wire-side `page_size`. This cleared the four pre-existing `users.spec.ts` failures that had been red on `main` since before Wave A.
- **Wave D (user pages + shared chrome):** same mechanical rewrite across `src/shells/user/*` — domains, databases (with Quick Setup + phpMyAdmin SSO), DNS, applications (with transitional-state polling via a plain `setInterval` effect instead of Refine's `refetchInterval`). Shared `Domain*` buttons and `dns/DNSRecordsPage` moved from `useInvalidate` + `useNotification` to `useQueryClient` + AntD `notification`. Dead `shellSider.tsx` and `RoleGate.tsx` deleted.
- **Wave E (cleanup):** `@refinedev/{core,antd,react-router,simple-rest}` removed from `package.json`; `@ant-design/icons` promoted to a direct dep (Refine used to pull it in transitively). `authProvider.ts`, `dataProvider.ts`, `searchableTableUtils.ts` deleted. `SearchableTable.tsx` trimmed to its string-`q` variant. Login/Consent test stubs dropped their `<Refine>` wrapper. `main.tsx`'s `@refinedev/antd/dist/reset.css` → `antd/dist/reset.css`. `admin/applications/AdminApplicationList.tsx` rewritten (it was scope-fenced for wt-a but its `useTable` usage would have blocked `@refinedev/*` removal).

**Measured results:**

- `grep -r "@refinedev" panel-ui/src panel-ui/tests panel-ui/package.json`: 0 hits
- Production JS bundle: 2,192 kB → **1,586 kB** (−27.6%), gzip 700.7 kB → **507.4 kB** (−27.6%) — beats the ≥50 kB gzipped target by ~4×.
- `node_modules` installed packages: 358 → 239 (−119).
- `npx tsc -b` clean; `npm test` 28/28 vitest; `npm run build` clean; `npm run test:e2e` 22/22.

**Depends on:** M20 (Kratos identity — supersedes Refine's `authProvider` role so removing it doesn't lose functionality).
**Related:** ADR-0037 (design rationale, rollback note, outcome), `plans/m21-drop-refine.md` (five-wave blueprint).

### M22: Magic-link → self-deleting SSO file (REWORK SHIPPED 2026-04-21)

**Status:** Rework shipped (all 8 steps merged to main 2026-04-21 in 4 waves). ADR-0040 accepted; ADR-0039 superseded. Operator runbook at `plans/m22-sso-file-runbook.md`; existing test VM (10.0.3.13) cleaned up via `plans/m22-rework-vm-teardown.md`.

**Goal:** One-click admin login from the panel to any managed WordPress install. Operator clicks "Log in to admin" on an Applications row → new tab opens → lands signed in to `/wp-admin` as the install's admin user.

**Why the rework:** The original magic-link design (ADR-0039) shipped end-to-end and was verified on test VM 10.0.3.13 the same day. Verification surfaced 5 connectivity / lifecycle gaps all caused by the same root pattern: persistent panel-side WordPress mu-plugin + HTTPS callback from WP back to the panel. (1) Plugin's "did sed run?" guard self-mutates and silently no-ops; (2) `update.go` doesn't sync the canonical mu-plugin source; (3) existing pre-M22 installs never get the per-install plugin copy; (4) nginx default vhost has no `/applications/.../validate` proxy → 444 silent drop; (5) panel self-signed cert isn't in OS CA bundle → `wp_remote_post sslverify=true` fails. All five disappear if there's no persistent WP-side code and no callback.

**New design (ADR-0040):** Self-deleting `jabali-sso-<43chars>.php` file written per login to the WP webroot — the Installatron / Softaculous pattern that has run at scale for ~15 years. Filename embeds a 256-bit `crypto/rand` nonce (the filename **is** the capability — no HMAC, no signing key, no DB row). Single-use via `flock(LOCK_EX|LOCK_NB)` + `unlink(__FILE__)`. TTL via systemd reaper sweeping every 30s. Wire contract `{url, expires_in}` preserved — only the URL shape changes from `?jabali_admin_login=<token>` to `/jabali-sso-<nonce>.php`. Eliminates the entire 5-item M22 follow-up list (placeholder guard fix, update.go sync, reconciler ensure, nginx proxy, OS CA trust) — none apply once there's no panel-side WP code or callback.

**Depends on:** M16R (OIDC rollback already landed).
**Related:** ADR-0038 (M16 rollback), ADR-0039 (M22 magic-link — superseded), ADR-0040 (M22 sso-file design + threat model), `plans/m22-rework-sso-file.md` (8-step rework blueprint, opus-reviewed).

---

## 7. Configuration

### Environment variables (`.env`)

```bash
DATABASE_URL=mysql://root:password@127.0.0.1:3306/jabali_panel
AGENT_SOCKET=/run/jabali/agent.sock
AGENT_TIMEOUT=5s
PANEL_ADDR=0.0.0.0:8443
KRATOS_PUBLIC_URL=http://127.0.0.1:4433
KRATOS_ADMIN_URL=http://127.0.0.1:4434
RECONCILER_INTERVAL=30s
```

### Config file (`config.toml` or via env)

```toml
[panel]
port = 8443
addr = "0.0.0.0:8443"

[auth.kratos]
public_url = "http://127.0.0.1:4433"   # panel proxies /.ory/* here
admin_url  = "http://127.0.0.1:4434"   # loopback-only, never exposed

[agent]
socket = "/run/jabali/agent.sock"
timeout = "5s"

[reconciler]
interval = "30s"
enabled = true

[database]
url = "mysql://..."
```

See `config.example.toml` for the full annotated shape (including `[cors]`).

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
4. **Authentication** — Ory Kratos session cookies are the only credential. Kratos stores passwords with bcrypt cost-12 (argon2id rehash disabled). Panel-api validates every request via `/sessions/whoami` and never signs tokens.
5. **Authorization** — RBAC middleware (RequireAdmin, RequireOwner) on protected routes. DB is authoritative for `is_admin`; Kratos identity traits are advisory only.
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
- **Gitea Actions** pipeline at `.gitea/workflows/ci.yml` runs on every push +
  PR to `main`. Three parallel jobs on a self-hosted `act_runner` (host-mode,
  no Docker): `Go tests + vet` (runs `make vet` + `make test -race`),
  `panel-ui unit tests (vitest)`, and `Playwright E2E` (builds SPA, installs
  Chromium, runs `@playwright/test --project=chromium`).
- Concurrency group `ci-${{ github.ref }}` with `cancel-in-progress: true`
  auto-cancels stale runs on rapid pushes so the runner queue stays shallow.
- **Branch protection on `main`** (loose): PR merges blocked until all 3 CI
  checks green, direct-push whitelisted for `shukivaknin` only, force-push
  disabled, admin merge override enabled as escape hatch for genuinely-broken
  CI. Configured via `POST /repos/:owner/:repo/branch_protections`.
- Pre-commit hook `/.claude/hooks/block-agent-commit-main.sh` blocks direct
  `git commit` on `main` to prevent parallel-session stomping — workflow is
  branch + merge, enforced locally before the server-side protection kicks in.

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
| M6: Email (Stalwart + Bulwark webmail + mailbox SSO) | 2026-04-22 | `caa6e16` through `fc73da4` on `m6/email-stalwart` (ADRs 0041-0045) |
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
| M18: Per-user resource limits | 2026-04-19 | Waves A-G on `main` (`caebe7b` → `aaa6bd0`); ADR-0032; runbook at `plans/m18-resource-limits-runbook.md`; host validation complete 2026-04-21 (4 bugs fixed incl. rate-limit zone ordering in `45a0ea6`) |
| M19: Applications Framework (all 8 steps) | 2026-04-19 | `m19/*` branch stack `733d6b8` → MediaWiki commit; ADR-0033; runbook at `docs/runbooks/applications.md`; DokuWiki + MediaWiki SHA-256 tarball pins captured 2026-04-21 |
| M5a: Admin Impersonation (DROPPED) | 2026-04-20 | Removed by M20 step 6 — replacement via Kratos `/admin/recovery/code` |
| M5b: Break-Glass CLI Login (DROPPED) | 2026-04-20 | Removed by M20 step 7 — replacement via `kratos identities` + `/admin/recovery/code` |
| M20: Kratos identity migration (all 9 steps + legacy removal) | 2026-04-20 | ADR-0034; runbook at `plans/m20-kratos-runbook.md`; Waves A–E on `main`; legacy JWT stack + M5c panel-side 2FA + `kratos-migrate` tool all deleted in the same batch — no dual-mode, no rollback flag |
| Infra: Gitea CI + branch protection | 2026-04-20 | `.gitea/workflows/ci.yml` (3 parallel jobs), self-hosted `act_runner` in host-mode (no Docker/Podman), loose branch protection on `main`. Also fixed pre-existing data race in `TestApplications_CreateWordPress_HappyPath` (`applications_service.go`) that CI's `-race` flag caught. Commits `b181c74` (workflow), `5d1f9a7` (race fix). |
| M21: Drop Refine (all 5 waves) | 2026-04-21 | ADR-0037; blueprint at `plans/m21-drop-refine.md`; Waves A–E on `m21/drop-refine`; 4 `@refinedev/*` packages removed; production JS −606 kB (−27.6%) / gzip −193 kB; `@ant-design/icons` promoted to direct dep; `useTableURL` + `useQueries` + `AuthContext` now power every list/form/whoami call |

---

**Last updated:** 2026-04-20
