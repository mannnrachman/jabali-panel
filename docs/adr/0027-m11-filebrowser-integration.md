# ADR-0027: M11 File Manager via filebrowser + proxy auth

**Date:** 2026-04-18
**Status:** Accepted
**Deciders:** Shuki

## Context

The Jabali Panel enables users to manage domains, databases, and PHP applications,
but offers no file browser for users to upload, edit, and organize website files.
Manual SFTP or SSH access is cumbersome; a web-based file manager is table-stakes
for hosting control panels.

M11 adds a first-class file manager integrated into the panel. The decision is
whether to build from scratch, adopt an existing open-source project, or fork
an active fork.

After evaluation, **filebrowser/filebrowser** (v2.63.2, MIT licensed) is a mature,
feature-complete file manager that serves our scope without maintenance burden.
It is currently in "maintenance-only mode" (bug and security fixes only, no new
features), which is acceptable because the product is feature-complete for our use case.

## Decision

We are integrating **filebrowser/filebrowser v2.63.2** (released 2026-04-11)
via a **reverse-proxy with header-based authentication** pattern. The key design
decisions are:

1. Use upstream filebrowser (v2.63.2) in maintenance mode; do not fork.
2. Single shared filebrowser process per system (not one-per-user).
3. Per-user file scoping via filebrowser's built-in `scope=/home/<username>` feature.
4. Authentication via proxy auth: nginx validates the panel session, strips any
   inbound `X-Forwarded-User` header, and injects the authenticated username.
5. Filebrowser listens on a UNIX socket, never TCP.
6. Reuse the same SSO key HMAC + UDS-validator pattern established in ADR-0022
   (phpMyAdmin SSO) for consistency.

This approach mirrors the phpMyAdmin SSO integration (M7) and leverages existing
security infrastructure.

---

## Design Decisions

### 1. Upstream filebrowser (v2.63.2) in maintenance mode

**Decision:** Integrate the official `filebrowser/filebrowser` GitHub repository
at v2.63.2, released 2026-04-11. The upstream project is in "maintenance-only mode":
bug fixes and security patches only, no new features planned.

**Rationale:**
- Filebrowser is feature-complete for our scope: upload, download, rename, delete,
  create directories, move files, view images.
- Maintenance-only is acceptable for a mature product; it signals stability and
  security focus.
- Avoids the maintenance burden of building a file browser UI from scratch
  (100+ hours for a production-ready implementation).

**Alternatives considered:**
- **Fork `gtsteffaniak/filebrowser` (active, with OIDC/LDAP/JWT support).**
  Rejected for MVP: those auth modes are out of scope; upstream's proxy-auth
  pattern is sufficient. Revisit if customers demand LDAP integration.
- **Build from scratch.** Rejected: file-manager UIs are a solved problem.
  Our differentiation is hosting control, not file browsing. Rebuilding
  duplicates effort and delays launch.

**Consequences:**
- No custom features beyond upstream; feature requests require upstreaming to
  filebrowser (slow) or local patches (maintenance debt).
- Security updates track upstream releases; v2.63.2 + backports are frozen.
  If a critical CVE is found, we evaluate backporting or bumping to a patched
  release (if upstream releases one).
- The product is well-tested and battle-hardened in production hosters.

---

### 2. Single shared process, per-user scoping

**Decision:** Run **one filebrowser process per Jabali host** (not one-per-user).
Each user has a separate filebrowser user record (created via `filebrowser users add`
by the reconciler), with `scope=/home/<username>` configured in the filebrowser
user profile. Users can only see and manipulate files under their scoped directory.

**Rationale:**
- Filebrowser's user + scope model is built for multi-user shared hosting.
- One process per user would require spawning N processes on a system with
  100 users, consuming excessive memory and file descriptors.
- Per-user scoping enforces filesystem-level isolation: even if filebrowser
  has a bug, the scope ACL prevents cross-user access.

**Alternatives considered:**
- **Per-user processes.** Isolates one user's crash/DoS from others but scales
  poorly. Deferred to M11+ if needed.
- **Global process with root scope, rely on filebrowser ACLs only.** Weaker
  isolation; a single ACL bypass exposes all user data. Rejected.

**Consequences:**
- Filebrowser process lifecycle is independent of user lifecycle (process runs
  from boot; users are added/removed via CLI or DB).
- Reconciler must ensure every jabali user has a corresponding filebrowser
  user (Step 6 reconciliation task).
- User deletion requires cleaning up the filebrowser user record (reconciler
  handles in Step 6).
- Single process limits concurrency per-user but is sufficient for MVP
  (file browsing is low-traffic).

---

### 3. Proxy auth + X-Forwarded-User header

**Decision:** Configure filebrowser with `auth.method=proxy` and
`auth.header=X-Forwarded-User`. Nginx acts as an authentication gateway:
1. Panel session subrequest validates the user's JWT and session state.
2. Nginx strips any inbound `X-Forwarded-User` header (to prevent spoofing).
3. Nginx injects `X-Forwarded-User: <authenticated_username>`.
4. Filebrowser trusts the header and logs the user in as that username.

**Rationale:**
- Filebrowser's proxy-auth mode is designed for reverse-proxy scenarios.
- Reuses the existing panel session validation subrequest (same pattern as
  phpMyAdmin).
- Avoids duplicating session state or syncing passwords between panel and
  filebrowser.
- Header injection at the reverse-proxy boundary is a standard pattern.

**CRITICAL SECURITY INVARIANT:**
Nginx **MUST strip any inbound X-Forwarded-User header** before adding its own.
If an attacker sends `X-Forwarded-User: admin` directly to filebrowser, they
will be logged in as admin without authentication. Nginx config must enforce:
```nginx
proxy_set_header X-Forwarded-User "";  # strip inbound
proxy_set_header X-Forwarded-User $user_from_subrequest;  # inject validated
```

**Alternatives considered:**
- **Custom auth plugin (Go/WASM).** Powerful but adds maintenance; filebrowser
  extensions are not stable. Rejected.
- **Session cookie sync.** Duplicate session state between panel and filebrowser;
  complex to keep in sync. Rejected.

**Consequences:**
- Authentication strength depends on nginx config correctness; misconfiguration
  breaks security. Document carefully in runbooks.
- Filebrowser logs show `X-Forwarded-User` as the username; audit trails reflect
  the authenticated user correctly.

---

### 4. UNIX socket, never TCP

**Decision:** Filebrowser listens on a UNIX socket at
`/run/filebrowser/filebrowser.sock` (or similar), not a TCP port.
Nginx connects to the socket via `proxy_pass unix:/run/filebrowser/filebrowser.sock;`.

**Rationale:**
- UNIX sockets are faster and more secure than TCP loopback (no network stack,
  socket permissions enforce ACLs).
- Filebrowser is an internal service, not exposed directly to the internet;
  socket is cleaner than a private TCP port.
- Matches the pattern used in ADR-0022 (phpMyAdmin UDS).

**Consequences:**
- Filebrowser systemd unit must create and own the socket directory
  (`/run/filebrowser/` with appropriate permissions).
- Socket permissions must allow nginx (www-data group) to connect.
- Debugging requires `nc -U /run/filebrowser/filebrowser.sock` or similar;
  standard `curl -v http://localhost:8080` won't work.

---

### 5. Reuse M7 SSO pattern: HMAC + UDS validator

**Decision:** Reuse the ssokey HMAC token + UDS validator pattern established
in ADR-0022 (phpMyAdmin SSO). The panel generates a single-use HMAC token
for the filebrowser launch, nginx validates it via a subrequest, and the token
is consumed.

**Rationale:**
- Proven pattern from M7; reduces risk by reusing tested code.
- HMAC token ensures that only the panel can initiate filebrowser sessions.
- UDS validator prevents token leakage via HTTP logs (validation happens
  out-of-band).

**Consequences:**
- Filebrowser integration code is nearly identical to phpMyAdmin code
  (ADR-0022). Review for copy-paste bugs.
- Token generation, validation, and consumption follow the same lifecycle:
  user clicks "File Manager" → panel generates token → nginx validates →
  filebrowser consumes token.
- The ssokey rotation policy (if any) applies to both phpMyAdmin and filebrowser.

---

### 6. Reconciler ensures filebrowser users exist

**Decision:** The reconciler (in Step 6) ensures every jabali user has a
corresponding filebrowser user record. It runs `filebrowser users add` for
new users and `filebrowser users delete` for removed users. The filebrowser
database is separate from the panel's; the reconciler is the bridge.

**Rationale:**
- Filebrowser stores user records in its own embedded database (SQLite or JSON).
- The reconciler's job is to converge system state toward the DB; this includes
  filebrowser users.
- Reconciler already manages similar one-way sync for other services (e.g.,
  nginx vhosts, PHP pools).

**Consequences:**
- Filebrowser must be initialized with an admin user (bootstrap step in
  `install.sh`). Subsequent users are added by the reconciler.
- User deletion is synchronous from the reconciler's perspective but may leave
  filebrowser data stranded if the delete CLI fails. Reconciler logs the error
  and retries on next cycle.
- Cross-service sync adds complexity; bugs in the reconciler can orphan users
  or leave them in an inconsistent state.

---

## Schema & Configuration

Filebrowser is a standalone binary; it does not use the panel's database.
Configuration lives in filebrowser's own storage (usually a SQLite DB or
JSON file bundled with the binary).

### Panel side: No new table

The panel does not track filebrowser users in its own schema. The reconciler
reads the panel's `users` table and ensures filebrowser is in sync by calling
the filebrowser CLI or API.

### Filebrowser side: User database

Filebrowser maintains its own user database (embedded). Fields include:
- Username (matches panel username)
- Password hash (not used; proxy auth disables password login)
- Scope (path like `/home/<username>`)
- Permissions (enable/disable upload, delete, etc.)

---

## Consequences

### Positive

- **No reimplementation:** Filebrowser is production-ready; saves 100+ hours of
  development and testing.
- **Proven integration pattern:** Mirrors ADR-0022 (phpMyAdmin), reducing
  surprise and leveraging existing code.
- **Isolation via scope:** Filesystem-level isolation prevents cross-user
  access even if the app has bugs.
- **Low operational overhead:** Single process scales to 100+ users with
  reasonable resource consumption.
- **Security focus:** Upstream is in maintenance mode, prioritizing stability
  and CVE response.

### Negative

- **Maintenance-only upstream:** No new features. If users request custom features
  (e.g., compression, archiving), we fork or patch locally.
- **Cross-service complexity:** Filebrowser state must be synced by the reconciler.
  Bugs in reconciliation can orphan users or leave stale records.
- **Header-auth security depends on nginx:** A misconfigured reverse proxy breaks
  the entire authentication model. Runbook must be clear and tested.
- **Filebrowser database coupling:** If filebrowser's storage format changes
  (e.g., SQLite → JSON), migrations are manual.

### Risks

- **Upstream EOL:** If filebrowser upstream stops security releases, we must fork
  or migrate. Acceptable risk for MVP.
- **Scope ACL bypass:** A hypothetical bug in filebrowser's scope enforcement
  would expose files outside the user's directory. Mitigation: run in a container
  or systemd namespace (deferred to M11+).
- **Single-process DoS:** A user uploading a huge file could saturate filebrowser
  and affect others. Mitigation: add per-user quotas and rate limits (deferred).

---

## Open Questions [OPEN]

**[OPEN] #1: Who does filebrowser run as?**

Three options:
- **(a) Run as root.** Simple deployment but violates the principle of minimal
  privilege. If filebrowser has a bug, attackers get root access.
- **(b) Dedicated filebrowser user in every per-user group (e.g., jabali-fpm as
  user, www-data group).** Matches the M9.5 per-user-slice pattern; requires
  filebrowser to be deployed with group membership.
- **(c) One process per user.** Full isolation but scales poorly.

**Default proposal:** Option (b) — filebrowser runs as a dedicated system user
(e.g., `_filebrowser`) with supplementary group membership in user groups to
read/write files. This matches the pattern used for PHP-FPM processes.
**Final answer deferred to Step 3 spike.**

---

**[OPEN] #2: Binary provisioning.**

Two approaches:
- **Pin GitHub release SHA-256.** Download precompiled filebrowser binary from
  GitHub releases, validate SHA-256 (mirrors the wp-cli pattern from M10
  `install.sh`). Fast, reproducible, no build dependencies.
- **Build from source in update.go.** Fetch Go source, compile, embed in the
  agent. Slower but ensures we're always building from source. Risks: build
  time, Go version mismatch, compilation failures.

**Default proposal:** Pin GitHub release SHA-256 in `install.sh` for consistency
with wp-cli. **Final answer deferred to Step 3 spike.**

---

**[OPEN] #3: User deletion cleanup.**

When a jabali user is deleted, their filebrowser user must also be deleted.

**Question:** Should this happen synchronously in the API delete handler, or
asynchronously in the reconciler?

**Default proposal:** Reconciler owns deletion (Step 6). Rationale: matches the
pattern for databases and other entities; API returns 202 immediately, reconciler
drives the teardown.

**Details to finalize:** What if `filebrowser users delete` fails? Does the
reconciler retry, or does the user row get stuck in a "deleting" state?
**Final answer deferred to Step 6 implementation.**

---

## Cross-References

- **`plans/m11-filebrowser.md`** — Full implementation blueprint with 9 steps.
- **ADR-0022** — M7 phpMyAdmin SSO (proxy auth + UDS validator pattern).
- **ADR-0023** — M9 PHP-FPM pool manager (filebrowser will run in similar context).
- **ADR-0025** — Per-user systemd slices (filebrowser process placement TBD).
- **`docs/BLUEPRINT.md` §6.11** — M11 scope and dependencies.
- **`docs/runbooks/filebrowser.md`** — Operational guide (TBD Step 9).

---

## Related Artifacts

*(TBD after implementation starts)*
- `install.sh` — Add filebrowser binary download + SHA-256 validation
- `panel-agent/internal/commands/filebrowser*.go` — Commands for user CRUD
- `panel-api/internal/reconciler/reconciler.go` — Reconciliation logic for users
- `panel-ui/pages/filebrowser.tsx` — Launch page (iframe + HMAC token)
- `docs/runbooks/filebrowser.md` — Operational runbook
