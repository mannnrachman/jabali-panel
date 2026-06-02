# ADR-0113: Multi-Runtime Domain Hosting

**Date:** 2026-05-29
**Status:** Accepted (Phase 1 — foundation layer shipped)
**Deciders:** Operator

## Context

Jabali Panel was designed and shipped as a PHP-only hosting panel (ADR-0033
Decision #7 explicitly deferred Node.js and Python). The campus use-case
(university IT department) has ~40% non-PHP systems (Node.js portals, Go
API gateways, Python ML services, Docker-containerised Java apps).

The M19 Applications Framework (ADR-0033) generalised per-CMS install
plumbing but kept every app on the PHP-FPM runtime. The framework's
`App` descriptor had no `Runtime` field — Decision #7 said "add it
when the first non-PHP app proposal lands".

This ADR fulfils that deferred decision.

## Design Decisions

### 1. `runtime_type` column on `domains` (migration 000148)

**Decision:** Add `runtime_type VARCHAR(16) NOT NULL DEFAULT 'php'` to
the `domains` table. The default preserves backward compatibility — every
existing domain keeps its PHP-FPM behaviour unchanged. No migration
populates existing rows; the DEFAULT clause handles it.

Accepted values: `php`, `nodejs`, `python`, `go`, `docker`, `static`.

The reconciler and nginx vhost renderer branch on this field:
- `php` → existing `fastcgi_pass` block (unchanged path)
- `nodejs`/`python`/`go` → `proxy_pass` to a managed systemd service
- `docker` → `proxy_pass` to a Docker container's published port
- `static` → no backend block (files only)

### 2. `runtime_services` table (migration 000149)

**Decision:** A new table stores the per-domain process metadata for
non-PHP runtimes: runtime type, version, entry point, allocated listen
port, environment variables (JSON), systemd unit name, and status.

One row per domain (UNIQUE on `domain_id`). PHP domains have no row —
the existing `php_pools` table handles that. The two tables don't
overlap; the discriminator is `domains.runtime_type`.

Port allocation: UNIQUE on `listen_port` prevents DB-level collisions;
a `PortAllocator` service also probes at runtime before assigning.

### 3. `Runtime` field on the App descriptor

**Decision:** The `apps.App` struct gains `Runtime string` and
`DefaultPort int`. Existing PHP descriptors (WordPress, Drupal, Joomla,
MediaWiki, phpBB, OpenCart, PrestaShop) continue to work with
`Runtime=""` — the framework treats empty as `"php"` for backward compat.

Future non-PHP apps (Ghost, Strapi, FastAPI templates) will set Runtime
to the appropriate value and the install flow will create a
`runtime_services` row instead of binding a PHP pool.

### 4. Nginx vhost template branches (Phase 2, not yet shipped)

**Decision:** The vhost template in `domain_create.go` will gain an
`{{ else }}` branch that emits `proxy_pass http://127.0.0.1:{{.ProxyPort}}`
instead of `fastcgi_pass`. The existing PHP block is untouched.

New `vhostData` fields: `RuntimeType`, `ProxyPort`, `ProxySocket`,
`ProxyProtocol`. These are zero-valued (and therefore no-op) for PHP
domains.

### 5. Port allocator (services/port_allocator.go)

**Decision:** A thread-safe port allocator assigns ports from the range
10000–60000, checking both the DB (via `IsPortInUse`) and an in-flight
reservation set. Random probing with linear-scan fallback keeps average
allocation O(1) even as the table grows.

### 6. Backward compatibility is non-negotiable

**Decision:** Every change is additive. Existing PHP domains, PHP pools,
vhost configs, reconciler behaviour, and API responses are unchanged.
The `runtime_type DEFAULT 'php'` ensures zero impact on running systems.
No existing migration, model, or API endpoint is modified in a breaking way.

## Consequences

- Adding a non-PHP domain requires setting `runtime_type` at creation
  time. The API will validate the value against `models.ValidRuntimeTypes`.
- The reconciler will need `WithRuntimeServices(repo)` wiring (Phase 5).
- The UI domain create form will show a runtime picker (Phase 6).
- Docker support introduces a new security surface (rootless mode,
  AppArmor profiles, per-user namespace isolation).

## References

- ADR-0033 Decision #7 (PHP-only for v1, deferred extension)
- ADR-0004 (reconciler-driven convergence)
- ADR-0009 (nginx file-per-vhost)
- ADR-0025 (per-user systemd slices)
