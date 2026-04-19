# ADR-0034: M20 Kratos identity migration — self-hosted Ory Kratos as identity provider

**Date**: 2026-04-19
**Status**: accepted
**Deciders**: shuki + Claude
**Related**: ADR-0003 (one write path), ADR-0013 (users inline best-effort), ADR-0015 (admin impersonation), ADR-0016 (break-glass CLI), M5c (TOTP), M7 (phpMyAdmin SSO)

## Context

The panel's authentication layer is built on hand-rolled JWT logic (custom `VerifyToken`, `RefreshToken`, axios interceptors in the SPA, session storage edge cases). This custom code has leaked security bugs repeatedly (050a725, 2a11073, e6edbfe) — not from intentional flaws, but from the *structural complexity* of client-side token lifecycle management in JavaScript (refresh timing races, stale token cache on page-refresh, ORB-replay of expired tokens, `<Authenticated>` pending-state sync issues).

Meanwhile, Ory Kratos (an open-source, Postgres/MySQL-compatible identity platform) offers:
- Session-cookie model (opaque `ory_kratos_session` cookie, per-request `/sessions/whoami` validation on loopback, no client-side token refresh dance)
- Built-in bcrypt + TOTP + backup codes (zero-trust by design)
- Admin API for identities + recovery + sessions (no need for a custom `admin-login` CLI)
- CSRF protection on forms (built-in, not reinvented)
- Upstream security review and battle-tested crypto

M20 replaces the hand-rolled JWT stack with Kratos while keeping all existing personas (admin/user), MFA (M5c TOTP), and integrations (M7 phpMyAdmin SSO). **Two features are dropped intentionally:** M5a admin impersonation and M5b break-glass CLI, both of which require Kratos admin-session-minting endpoints that don't exist in the self-hosted 1.3.1 OSS build. Operator workflows are replaced by Kratos-native primitives: recovery codes + the `kratos` CLI.

## Decision

### 1. Architecture: separate systemd service on the same host

**Kratos runs as `jabali-kratos.service`** under `jabali.slice`, not embedded in panel-api. Kratos is an upstream binary; keeping it separate allows idempotent install/upgrade from `install.sh` without tying Kratos release cadence to panel-api. It listens on loopback:
- **Public endpoints** (`127.0.0.1:4433`): `/self-service/*` flows (login, registration, recovery, settings)
- **Admin endpoints** (`127.0.0.1:4434`): identity/session management (never exposed beyond localhost)

Nginx reverse-proxies `/.ory/*` → `http://127.0.0.1:4433/` so the SPA sees a same-origin URL and cookies work transparently.

### 2. Database: MariaDB with dedicated schema `jabali_kratos`

Kratos upstream recommends Postgres, but MariaDB is tier-2 supported and we already run MariaDB (ADR-0018). A separate DB schema (`jabali_kratos` alongside `jabali_panel`) isolates Kratos tables from user-created migrations — a compromised panel-api migration can't corrupt identity state. Schema isolation is worth the operational cost.

**Spike finding K-1 (constraint):** Kratos OSS binary has no SQLite support. Install.sh MUST use MariaDB. Development environments needing SQLite must build Kratos from source with `-tags sqlite` (out of scope).

**Spike finding K-2 (constraint):** MariaDB's default `STRICT_TRANS_TABLES` breaks Kratos migrations (NULL in non-nullable fields). The DSN in `kratos.yml` must include `sql_mode=%27NO_ENGINE_SUBSTITUTION%27` (URL-encoded). Without this, `kratos migrate sql` fails on first install.

### 3. Session validation: per-request `/sessions/whoami` on loopback

Panel-api calls Kratos `GET /sessions/whoami` (with the browser's `ory_kratos_session` cookie) on every authenticated request. This round-trip takes <1ms on loopback and gives us:
- **Instant revocation:** Admin revokes a session via Kratos Admin API → next request fails
- **No JWT complexity:** No offline signature verification, no JWKS rotation, no token expiry tracking
- **Cache-friendly:** A 10-second LRU cache (keyed by SHA-256 of the cookie value, never the raw cookie) caps whoami traffic without losing revocation precision

Alternative (deferred): If whoami becomes a bottleneck at scale, add a 5-second in-memory per-process cache. Not needed in v1.

### 4. Identity schema: `{email, username, is_admin}` — minimum viable shape

Kratos identities have a JSON-Schema-validated `traits` object. Our schema is deliberately narrow:
- **`email`** (required, unique): primary identifier for login and recovery
- **`username`** (optional string): Linux shell username (omitted for admins, who have no OS account)
- **`is_admin`** (boolean, defaults false): settable only via Admin API, never through self-service UI

Any relationship to domains, packages, SSH keys, or cron stays in the panel DB, keyed by Kratos's identity UUID. The `users` table gains a `kratos_identity_id` FK column during migration; no data duplication.

### 5. Bcrypt passthrough: no forced password reset, automatic argon2id rehashing DISABLED

**Spike finding validated:** Kratos accepts `$2y$`, `$2a$`, `$2b$` bcrypt hashes in the Admin API `POST /admin/identities` as `credentials.password.config.hashed_password`. Our `users.password_hash` is bcrypt cost-12; we import it as-is.

**Critical constraint:** Kratos's `POST /admin/identities` by default auto-rehashes passwords to argon2id on login. If enabled, rollback would fail (legacy `VerifyPassword` in panel-api only handles bcrypt). **Config disables automatic rehashing:** `kratos.yml` pins `hashers.algorithm: bcrypt` with `hashers.bcrypt.cost: 12`. No forced resets, zero UX impact, safe rollback window.

**Canary on startup (step 3):** Panel-api startup calls Kratos Admin API with a disposable test identity (`email: "canary@test.local"`, password hash `$2a$12$...`) and verifies login via `/self-service/login/api`. If rejected, panel FATALs with a clear error — we do NOT proceed with a migration against a Kratos that silently rejects our hash format.

### 6. UI: headless Kratos flows, rendered by Refine SPA with AntD components

Kratos ships a reference UI, but we keep our AntD design. Instead, we render Kratos flows headless:

1. SPA calls `GET /.ory/self-service/login/browser` → Kratos responds with flow ID and `ui.nodes[]` array
2. SPA's flow renderer iterates `ui.nodes`, finds every node (especially hidden inputs like `csrf_token`)
3. On form submit, renderer echoes back all node values (including the CSRF token — **this is critical**)

**Spike finding T3a (validated):** Kratos's `/self-service/login/browser` returns:
```json
{
  "ui": {
    "nodes": [
      { "type": "input", "attributes": { "name": "csrf_token", "value": "…" }, ... },
      { "type": "input", "attributes": { "name": "identifier" }, ... },
      { "type": "input", "attributes": { "name": "password", "type": "password" }, ... }
    ],
    "action": "https://.../self-service/login/browser?flow=...",
    "method": "POST"
  }
}
```

Renderer code (step 5):
```tsx
const { ui: { nodes, action, method } } = flow;
const csrfNode = nodes.find(n => n.attributes?.name === 'csrf_token');
const formData = new FormData();
formData.append('csrf_token', csrfNode.attributes.value);
formData.append('identifier', email);
formData.append('password', password);
await fetch(action, { method, body: formData, credentials: 'include' });
```

Missing the CSRF token is the #1 Kratos integration bug worldwide. We close this with concrete code in the ADR and tests in step 5.

### 7. Impersonation (M5a) DROPPED

Live spike (T2) confirmed: `POST /admin/identities/{id}/sessions` does NOT exist in Kratos 1.3.1 OSS (Allow header shows only `DELETE, GET, OPTIONS`). This endpoint is Ory Network (SaaS) only, not available in self-hosted.

Rather than retain a custom JWT-minting codepath for one feature — defeating the "zero custom JWT" goal — M5a is removed entirely (step 6). Operator replacement workflows:

1. **Debug a user's panel state:** Admin runs `kratos identities get <user-email>` on the server shell to inspect traits/credentials, OR generates a recovery code via `curl -X POST http://127.0.0.1:4434/admin/recovery/code -d '{"identity_id":"<uuid>"}'` and sends the URL to the user, who pastes it and resets their password. Admin can now log in with the new temp password, help, then user resets to permanent.

2. **Screenshare-first:** Pragmatic replacement for 95% of "I need to see what they see" cases.

3. **Audit-only ops:** Panel already logs every admin action + agent command + per-user usage; impersonation was rarely needed for forensic work.

Historic `impersonated_by` audit rows are retained as read-only history (step 6).

### 8. Break-glass CLI (M5b) DROPPED

Live spike (T3b) confirmed: API-mode session tokens (e.g., `ory_st_...`) are not interchangeable with browser-mode session cookies. Kratos distinguishes between the two — the token alone cannot be converted into a valid `ory_kratos_session` cookie.

Rather than reinvent a one-shot session-exchange endpoint (which is just a smaller JWT), M5b is removed entirely (step 7). Operator replacement workflows:

1. **Forgot admin password:** On the server shell, run:
   ```bash
   curl -X POST http://127.0.0.1:4434/admin/recovery/code \
     -H 'Content-Type: application/json' \
     -d '{"identity_id":"<uuid>"}'
   ```
   Returns `{"recovery_code":"...", "recovery_link":"https://..."}`. Admin opens the URL in a browser, completes the recovery flow (password reset), is logged in.

2. **Panel UI broken, need to manage identities:** Use Kratos CLI directly from the server shell:
   ```bash
   kratos identities list|get|patch|delete  # covers create/disable/unlock/MFA-reset
   ```

3. **Kratos DB lost entirely:** Restore `jabali_kratos` schema from `mysqldump` backup. If no backup exists, the panel `users` table still holds `kratos_identity_id` references; re-run the migration tool with `--rebuild-kratos` (generates password-reset links for every user).

The `jabali admin-login` subcommand is deleted in step 7.

### 9. phpMyAdmin SSO (M7) rewrite: session-cookie validation over UDS

M7's existing SSO handler on `/run/jabali/sso.sock` (a Unix domain socket served by FPM) currently validates a JWT from the query string. With Kratos, it validates the browser's session cookie instead:

1. Nginx proxies phpMyAdmin's signon.php request to the UDS, but **MUST forward the `Cookie:` header** with `proxy_set_header Cookie $http_cookie;` in the nginx config (step 2)
2. Handler reads `Cookie:` header, extracts `ory_kratos_session` value
3. Handler calls `GET /sessions/whoami` at Kratos Admin API with that cookie value
4. On success, handler looks up the identity's shadow MariaDB password from the existing `phpmyadmin_shadow_users` table (keyed by `kratos_identity_id`)
5. Handler returns the shadow password to signon.php over the UDS; phpMyAdmin logs in to MariaDB as that user

**Nginx config change (step 2):** The location block proxying signon.php to the UDS must include `proxy_pass_request_headers on;` AND `proxy_set_header Cookie $http_cookie;`. Missing these is the single most common cause of "phpMyAdmin SSO broken after upgrade" issues.

`sso.key` for at-rest encryption of shadow passwords is unchanged. End-to-end test (step 7): user logs in via Kratos → navigates to `/phpmyadmin/` → phpMyAdmin initiates signon flow → handler validates Kratos cookie → shadow password returned → phpMyAdmin connects as the user.

### 10. 2FA migration (M5c): one-shot TOTP seed export + Admin API import

Our `user_totp_secrets` table stores the TOTP seed per `user_id`. Migration tool (step 8):
1. Iterates `user_totp_secrets` rows
2. For each, calls `PATCH /admin/identities/{id}/credentials` at Kratos Admin API with `totp` credential + the raw base32 seed
3. Kratos imports the seed; users keep their existing TOTP app secret (no re-enrollment)
4. Also migrates backup codes if present (Kratos's `lookup_secret` credential uses the same hashing as ours)

After migration, old tables stay for 30 days (operator-verifiable rollback window). Deletion documented in runbook.

### 11. Scope: Hydra, Keto, Oathkeeper deferred

- **Hydra (OAuth 2):** Reserved for M16 (Automation API tokens). Separate milestone.
- **Keto (Zanzibar-style authz):** Overkill for two roles (admin/user) + resource ownership. Reconsider if we ever add team-based access.
- **Oathkeeper (identity-aware proxy):** Useful for microservices; we're monolithic.

v1 is Kratos-only.

### 12. Rollback: feature-flagged dual-mode auth, single validator per request

Panel-api config gains `auth.provider = "kratos" | "legacy"`. The middleware wrapper reads the flag ONCE at startup and dispatches to exactly ONE validator:

- **When `auth.provider = "kratos"`:** Kratos session-cookie validation ONLY. Any `Authorization: Bearer <jwt>` header is ignored (NOT a fallback). A request with both an expired JWT and a valid Kratos cookie succeeds (the JWT is discarded). A request with an invalid Kratos cookie fails 401, regardless of the JWT's validity.

- **When `auth.provider = "legacy"`:** JWT validation ONLY. Any `ory_kratos_session` cookie is ignored. A request with both a stale JWT and a valid Kratos cookie fails 401 (we don't check Kratos at all).

**This is NOT a fallback pattern** (never: "try Kratos, if it fails try JWT"). That would be an auth bypass — an attacker with one expired credential could use the other to succeed. The flag selects exactly one validator. Step 3's test suite must explicitly verify this: a request with both invalid credentials fails when the selected validator's credential is invalid, regardless of the other's validity.

Step 9 flips the default to `kratos` and documents the rollback (flip back, redeploy). After 30 days of stable operation, a future PR removes the legacy JWT code path entirely.

### 13. Kratos version pin: v1.3.1

`install.sh install_kratos` downloads the Kratos binary and verifies SHA-256 against a vendored `install/kratos.sha256` file (same pattern as wp-cli + phpmyadmin). Upgrades bump the version + checksum; Kratos's SQL migrations are idempotent and auto-run on startup.

### 14. TLS boundary: loopback services are plain HTTP

Kratos talks to MariaDB over loopback (no TLS); panel-api talks to Kratos over loopback HTTP (no TLS). Everything is on `127.0.0.1` inside the same host. Encryption inside the host is security theater; the real boundary is nginx (TLS to the public internet). Unchanged from today.

### 15. Session cookie: name stays `ory_kratos_session`

We don't rename the cookie. Renaming means configuring Kratos's `session.cookie.name` and losing upstream defaults (the Kratos Admin UI, all kratos-client libs, all docs assume `ory_kratos_session`). Cookie is `HttpOnly`, `Secure`, `SameSite=Lax`, `Path=/`, domain unset (host-only). Same origin as the panel, so `SameSite=Lax` is sufficient for our GET navigations and POST flows (no cross-site XSRF concerns beyond what Kratos's CSRF token handles).

## Alternatives considered

### A. Use Hydra for session management (not just OAuth)

Hydra's login consent flow can work with Kratos, but adding Hydra just for session management is overkill. Kratos's session-cookie model is simpler and sufficient for v1. Hydra comes in M16 for OAuth API tokens.

### B. Keep hand-rolled JWT, just add Kratos for identity storage

Half-migration (JWT for sessions, Kratos for identity storage) introduces data sync bugs: JWT claims drift from Kratos traits, admins revoke a session in Kratos but the JWT remains valid for its TTL, etc. Either move fully or not at all. We move fully.

### C. Force password reset on Kratos import

Could require all users to reset passwords on first Kratos login. Rejected: bcrypt passthrough is safe and the UX cost is high (support load, password-reset-email spam, locked-out users if their email is broken).

### D. Use Postgres for Kratos instead of MariaDB

Upstream recommendation is Postgres. Rejected: we already run MariaDB (ADR-0018), adding a second DB engine inflates `install.sh`, and MariaDB is tier-2 supported at our scale (<10k identities). If benchmarks prove MariaDB is a bottleneck, switch in a future PR.

## Consequences

### Positive

- **Zero custom JWT code after cutover.** One less class of subtle bugs (token refresh races, stale cache, ORB-replay).
- **Instant session revocation.** Admin revokes in Kratos → next request fails, no TTL expiry wait.
- **TOTP + backup codes native.** Users keep their existing TOTP secrets; no forced re-enrollment.
- **phpMyAdmin SSO preserved.** M7 integration flips to session-cookie validation (no behavior change, same UX).
- **Safe rollback window.** 30 days behind a feature flag; legacy code stays for emergency revert.
- **Battle-tested upstream.** Kratos's crypto + security review + community feedback beat our custom code.

### Negative

- **M5a + M5b removed.** Impersonation and break-glass CLI are gone. Replacement workflows via Kratos recovery + CLI are more manual (but sufficient for the operator's job).
- **Whoami on every request.** Adds <1ms loopback latency to all authenticated requests. Mitigated by 10-second LRU cache; acceptable at our scale.
- **Mandatory password reset on cutover (step 9).** All active sessions are invalidated. Users must re-login once. Documented as expected UX.
- **Nginx config footgun (phpMyAdmin SSO).** Must explicitly forward `Cookie:` header. Missing this breaks SSO silently (step 7 test catches it).

### Neutral

- **Separate systemd service.** One more thing to monitor and upgrade, but isolation is worth it.
- **30-day legacy code retention.** Adds code volume; necessary for rollback safety.

## Risks + mitigations

| Risk | Mitigation |
|------|-----------|
| Kratos binary doesn't work on target OS/arch | Install.sh tries `kratos migrate sql` immediately and FATALs on failure. Fail before users are created. |
| SQL mode breaks migrations (K-2) | Spike validated + fixed. DSN includes `sql_mode=NO_ENGINE_SUBSTITUTION`. |
| Bcrypt passthrough doesn't work | Canary in step 3 tests import + login before batch migration. Abort if canary fails. |
| Migration leaves some users orphaned | Per-user idempotent via email lookup. Safe to re-run; already-migrated users are skipped. |
| Impersonation feature leakage (admin retains elevated rights after impersonation) | Feature removed entirely. Non-issue. |
| phpMyAdmin SSO broken by missing nginx header | Explicit test in step 7 verifies the UDS request carries the session cookie. Config snippet documents `proxy_set_header Cookie`. |
| Session cookie stolen via XSS | HttpOnly flag prevents JavaScript access. Mitigated like all cookies. |
| Whoami cache key leaked via logs | Cache key is SHA-256(cookie), not the raw cookie. Logs never show the key. |
| `/admin/` endpoint exposed by accident | Kratos config binds admin API to `127.0.0.1:4434`. Nginx does NOT proxy this. Only panel-api (localhost-only) calls it. |

## Related decisions

- **ADR-0003 (one write path):** Panel-api still owns all writes. Step 4 adds an inline Kratos-create hook on `POST /api/v1/admin/users` (atomic with panel DB INSERT).
- **ADR-0013 (users inline best-effort):** Kratos identity creation is inline on user.ensure (step 3). Reconciler never creates identities.
- **ADR-0015 (admin impersonation):** Dropped in step 6. Historic audit rows retained.
- **ADR-0016 (break-glass CLI):** Dropped in step 7. Replacement via Kratos CLI + `/admin/recovery/code`.
- **M5c (TOTP 2FA):** Data migrated in step 8 via Admin API. UI flow via Kratos in step 5.
- **M7 (phpMyAdmin SSO):** Authz flipped in step 7 to session-cookie validation. Encryption surface unchanged.
- **M16 (Automation API):** Blocked on M20. Hydra integration is a separate milestone.

## References

- **Plan:** `/plans/m20-kratos-identity.md` (full 9-step blueprint, spike findings, review checklist)
- **Kratos docs:** https://www.ory.sh/kratos/docs
- **Spike findings log:** Plan §7 documents K-1 through K-4 (MariaDB, SQL mode, schema_id, recovery methods)
- **Adversarial review:** Plan §5 documents CRITICAL + HIGH findings and resolutions folded into this ADR

## Implementation notes (2026-04-20, cutover)

All 9 steps shipped with two intentional deviations from the original plan.

### Plan deviations

| Plan claim | Reality | Decision |
|---|---|---|
| Step 8: "Kratos accepts raw TOTP seeds and hashed backup codes" | Kratos 1.3.x admin API rejects TOTP/lookup_secret imports (the Kratos CLI itself documents this); Kratos stores backup codes as plaintext and our hashes can't be reversed | `--totp-only` became a read-only CSV reporter; users re-enroll post-cutover via Security → Authenticator. Runbook documents the operator notification flow. |
| Step 6: "Drop M5a admin impersonation audit rows" | Audit rows carry compliance value; truncating them is irreversible | Kept historic rows read-only; deleted only the live feature surface (route + button + JWT claim plumbing). |

### BootstrapAdmin extension (Step 9 addendum)

The plan's Step 9 "flip default to kratos" required `BootstrapAdmin`
(panel-api/internal/auth/bootstrap.go) to be Kratos-aware — otherwise a
fresh install creates an admin in the panel DB with no matching Kratos
identity, and first-boot login 401s. Extension landed as part of Step 9
with compensating-transaction semantics matching the existing
`POST /api/v1/admin/users` hook:

1. panel DB INSERT
2. Kratos `POST /admin/identities` with bcrypt passthrough
3. panel UPDATE with `kratos_identity_id`

Any failure rolls back the prior step(s). Passing `Kratos = nil` keeps
the legacy behavior, so the two code paths share one invariant — ADR-0003
("one write path") extends to bootstrap.

### Default flip

- `config.example.toml`: `provider = "kratos"` (was `"legacy"`)
- `panel-api/internal/config/config.go`: Go default `"kratos"` (was `"legacy"`)
- 30-second rollback: `sed -i 's/^provider = "kratos"/provider = "legacy"/' /etc/jabali/config.toml && systemctl restart jabali-panel`
- Feature flag stays for 30 days per plan §1 step 9. Removal scheduled for a
  follow-up PR after stable operation on production.

### Deliverables (reality)

| Wave | Step | Files |
|---|---|---|
| A | 1 — ADR + kratos.yml + identity schema | This ADR; `install/kratos.yml.tmpl`; `install/kratos-identity-schema.json` |
| A | 2 — install_kratos + systemd + DB + nginx | `install.sh` `install_kratos()`; `install/kratos.sha256`; nginx `/.ory/` proxy block |
| B | 3 — kratosclient + middleware + feature flag | `panel-api/internal/kratosclient/*`; `panel-api/internal/middleware/auth_kratos.go` |
| B | 4 — migration tool + user-create hook + 000046 migration | `panel-api/cmd/server/kratos_migrate_cmd.go`; `panel-api/internal/api/users.go` inline hook; `panel-api/internal/db/migrations/000046_*` |
| C | 5 — authProvider + Login.tsx + kratos.ts | `panel-ui/src/authProvider.ts`; `panel-ui/src/pages/Login.tsx`; `panel-ui/src/kratos.ts`; `panel-ui/src/apiClient.ts` |
| C | 6 — M5a removal | Deletions in `panel-api/internal/api/impersonate.go` + SPA impersonation surface |
| D | 7 — M5b removal + middleware panel-ULID fix | Deletions of `admin_login.go` + `auth_cli_login.go` + `RedeemCLIToken`; added `UserRepository.FindByKratosIdentityID` + middleware lookup |
| D | 8 — TOTP report (plan deviation) | `panel-api/cmd/server/kratos_migrate_totp.go`; runbook "TOTP re-enrollment" |
| E | 9 — cutover + E2E + runbook + BLUEPRINT + this ADR append | Default flip; `BootstrapAdmin` Kratos extension; `panel-ui/tests/e2e/kratos-login.spec.ts`; runbook day-2 + rollback sections; BLUEPRINT M20 section |

### Deliberate non-goals (confirmed at cutover)

- **Legacy JWT code removal** — stays behind the feature flag for the 30-day
  rollback window. Follow-up PR removes it in full.
- **Kratos trait normalization** — traits carry `is_admin` but the panel
  DB stays authoritative on role (middleware reads `users.is_admin`, not
  `identity.traits.is_admin`). Prevents a rogue Kratos patch from
  granting admin.
- **Kratos password-migration webhook** — not used. All current users
  have bcrypt hashes, passthrough covers them.

### Status: SHIPPED 2026-04-20
