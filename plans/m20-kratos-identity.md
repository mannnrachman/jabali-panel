# M20 — Kratos Identity Migration (custom JWT → self-hosted Ory Kratos)

**Status:** Reviewed + revised + spiked against live Kratos 1.3.1 (2026-04-19). 4 CRITICAL + 3 HIGH + 4 MEDIUM findings folded from adversarial review. Spike found **2 additional CRITICAL design flaws** + **1 validated assumption** that require plan revision BEFORE Wave A dispatch. See §5 "Adversarial review log" + §7 "Spike findings" below.
**Goal:** Replace our hand-rolled JWT-access + HttpOnly-refresh auth with self-hosted Ory Kratos as the identity provider. Kratos runs as a second systemd-managed service on the same host, sharing MariaDB (its own schema `jabali_kratos`). panel-api stops minting JWTs and becomes a thin session-cookie validator. Preserves the existing admin/user personas, impersonation (M5a), break-glass CLI (M5b), 2FA (M5c), and phpMyAdmin SSO (M7). NO Hydra, NO Keto, NO Oathkeeper — API-token OAuth 2 grants are deferred to M16.

**Context for this rewrite:** our JWT-in-JS + axios-interceptor-refresh pattern has leaked bugs repeatedly (050a725, 2a11073, e6edbfe) — each a legitimate root cause, none a complete fix. The class of bug (client hangs when the token lifecycle race with `<Authenticated>`'s pending state) is structural. Kratos's session-cookie model eliminates the in-JS token entirely: the browser sends an opaque `ory_kratos_session` cookie, the panel validates via `/sessions/whoami`, no refresh dance, no ORB replay, no stale cache on index.html gating asset loads. Battle-tested crypto, upstream security review, free passkeys/recovery/lockout — all the things our custom code doesn't ship.

---

## 0. Key design decisions

1. **Kratos runs as a separate systemd unit, not embedded in panel-api.** Kratos is an upstream binary; shipping it as `jabali-kratos.service` under `jabali.slice` keeps it idempotently installable/upgradable from `install.sh` without tying its release cadence to panel-api. The unit uses `Type=simple` + `Restart=on-failure`, listens on loopback `127.0.0.1:4433` (public self-service endpoints) + `127.0.0.1:4434` (admin API, never exposed beyond the host). nginx reverse-proxies `/.ory/*` → Kratos:4433 so the SPA sees a same-origin URL.

2. **MariaDB with a dedicated schema `jabali_kratos`, not Postgres.** Kratos upstream docs recommend Postgres, but MariaDB is tier-2 supported and we already run MariaDB. Using MariaDB avoids adding a second DB engine to `install.sh` (Postgres would require pg config + its own backup/restore story). Schema isolation via a separate DB name keeps Kratos's tables out of `jabali_panel` — a compromised panel migration can't accidentally corrupt identity state. Kratos runs its own migrations via `kratos migrate sql` at install time and on every upgrade.

3. **Session validation is `/sessions/whoami` per-request — no JWT shortcut.** Whoami round-trips to Kratos (loopback, <1ms) on every authenticated request. Alternative: Kratos can mint JWTs signed with a JWKS-published key; panel-api would verify offline. We choose whoami because (a) it's what every Kratos deployment runs in production, (b) it gives us instant session revocation (admin revokes via Kratos Admin API → next request fails), (c) loopback is fast enough that we don't need the JWKS complexity in v1. If whoami becomes a bottleneck (measured), we add a 5-second LRU cache keyed by the session cookie hash.

4. **Identity traits: `{email, username, is_admin}` — the minimum shape.** Kratos identities have a JSON-schema-validated `traits` object. Our schema is deliberately narrow: `email` (required, unique), `username` (Linux shell username, optional because admins have no OS account — preserves M5a invariants), `is_admin` (boolean, defaults false, settable only via Admin API — never through self-service). Any domain/package/SSH-key/cron relationship stays on the panel DB, keyed by Kratos's identity UUID. No data duplication; the `users` table keeps its ULID PK but adds a `kratos_identity_id` FK column during migration.

5. **Bcrypt passthrough migration — no forced password reset. Kratos's automatic argon2id rehashing is DISABLED to keep rollback safe.** Kratos's `POST /admin/identities` accepts `credentials.password.config.hashed_password` with bcrypt or argon2id hashes. Our existing `users.password_hash` is bcrypt cost-12. `install/kratos.yml.tmpl` pins `hashers.bcrypt.enabled: true`, `hashers.bcrypt.cost: 12`, and explicitly **disables automatic rehashing** (`hashers.algorithm: bcrypt`, no argon2 step). The migration tool imports each user with their current bcrypt hash. Zero forced resets. Critical safety rule for rollback: if argon2id rehashing were enabled, logins via Kratos would overwrite `users.password_hash` with argon2id and `auth.provider = "legacy"` rollback would break (legacy `VerifyPassword` at `panel-api/internal/auth/service.go` only handles bcrypt). Keeping bcrypt as the Kratos hash algorithm means the two systems are mutually compatible for the 30-day rollback window. This addresses review finding #9. **Pre-migration startup canary (step 3):** panel-api startup calls Kratos Admin API with a disposable test identity carrying a known `$2a$12$...` bcrypt hash, then immediately deletes it. If identity-create fails or login-verify fails, the panel FATALs with a clear error — we do NOT proceed with a migration against a Kratos that silently rejects our hash format. (Review finding #2.)

6. **UI: headless Kratos flows rendered by our Refine SPA — NOT Kratos's embedded UI. CSRF token handling is a first-class concern.** Kratos ships a reference UI (`kratos-selfservice-ui-node`) but it's React + Next.js and looks nothing like our AntD shell. Instead, we use Kratos's "browser flows" headless mode: SPA calls `GET /.ory/self-service/login/browser` → Kratos responds with a flow ID and a `ui.nodes` array. The SPA's flow renderer iterates `ui.nodes`, finds every node with `type: "input"` (including `attributes.name === "csrf_token"` — a hidden input whose `attributes.value` MUST be echoed back in the submit). Renderer implementation in step 5 is explicit:
   ```tsx
   const { ui: { nodes, action, method } } = flow;
   const csrfNode = nodes.find(n => n.attributes?.name === 'csrf_token');
   // On submit:
   const formData = new FormData();
   formData.append('csrf_token', csrfNode.attributes.value);
   formData.append('identifier', email);
   formData.append('password', password);
   await fetch(action, { method, body: formData, credentials: 'include' });
   ```
   Without the CSRF token, Kratos rejects the submit with a `security_csrf_violation` error. Missing this is the single most common Kratos integration bug. Same renderer pattern for registration, recovery, and MFA (TOTP). Preserves every pixel of our existing UX. Trade-off: we maintain the form renderers (~200 lines of TSX), but we keep the AntD look. This closes review finding #5.

7. **Impersonation (M5a) via Kratos Admin API `create session for identity` — MFA bypass verified by an explicit test.** Kratos exposes `POST /admin/identities/{id}/sessions` which mints a session for any identity. Admin's browser session stays alive in a separate cookie; the impersonation cookie is scoped to the target identity's session. When the admin "exits impersonation," we call `DELETE /admin/sessions/{session_id}` to revoke and restore the admin's original session. Audit log (who impersonated whom, when) stays in the panel DB. **Critical verification before step 6 dispatch:** step 6 MUST include a test that creates a Kratos identity with TOTP enabled, calls `POST /admin/identities/{id}/sessions`, and verifies the returned session cookie passes `/sessions/whoami` without the admin needing to supply a TOTP code. If Kratos 1.3.x enforces `aal=2` on admin-created sessions for identities with TOTP enabled, the impersonation feature is broken and the plan must pivot to an alternative (e.g., temporarily downgrading the target's AAL before session creation and restoring after). Review finding #3 flagged this as unverified — the test gates step 6 completion. Documentation note for runbook: "Impersonation sessions bypass the target user's TOTP; this is by design (operator is already trusted) and is audit-logged on the panel side."

8. **Break-glass CLI (M5b) — `jabali admin-login` calls Kratos Admin API directly; the "one-shot token" is a Kratos session ID, NOT a JWT.** Current M5b creates a JWT locally signed with the server's JWT_SECRET. New M5b: CLI connects to Kratos admin endpoint on `127.0.0.1:4434` (loopback; Kratos admin is firewall-gated and never nginx-proxied), calls `POST /admin/identities/{id}/sessions` with a 15-min TTL. Kratos returns a full session object containing `session.id` (UUID) and `session.token` (opaque Kratos-generated string). CLI prints `https://<panel_host>/login?kratos_session_token=<session.token>`. Operator opens in a browser. Panel-api's `GET /api/v1/auth/exchange?kratos_session_token=<tok>` handler (new, thin, ~30 LOC): validates the token is a well-formed Kratos session token by calling Kratos `GET /sessions/whoami` with `Cookie: ory_kratos_session=<tok>` — if whoami returns 200, the token is live, panel-api sets the `ory_kratos_session` cookie on the response and redirects to `/jabali-admin`. Single-use enforced by Kratos (session token is the session — once it's in the browser as a cookie, the URL is useless and a 2-min "exchange window" cap is unnecessary). **Explicitly not a JWT**: the token is generated by Kratos, stored in Kratos's session table, validated by Kratos. Panel-api never signs anything. This addresses review finding #4. Break-glass remains functional if panel-api is dead (as long as Kratos is alive, operator can use `kratos identities list` + manual session creation via `kratos sessions issue` CLI to reach a working session — documented in runbook).

9. **phpMyAdmin SSO (M7) keeps sso.key at rest for the shadow-password table, but the client-facing handshake becomes session-based. Nginx MUST forward the `Cookie:` header to the UDS handler — this is a configuration bug waiting to happen.** phpMyAdmin's signon.php still hits `/run/jabali/sso.sock` (existing UDS), but the panel-api handler now (a) parses the `Cookie:` header on the UDS request (NOT the Gin context `c.Cookie()` — UDS requests don't carry browser context the same way), (b) extracts `ory_kratos_session`, (c) calls Kratos whoami with that cookie value, (d) looks up the identity's shadow MariaDB password from the existing `phpmyadmin_shadow_users` table, (e) returns it. sso.key is still needed to decrypt the shadow password at rest. **Nginx config change required in step 2:** the location block that proxies signon.php's SSO validate call to the UDS must include `proxy_pass_request_headers on;` AND explicitly `proxy_set_header Cookie $http_cookie;` to forward the browser's session cookie through the FPM proxy into the UDS request. Test in step 7: user logs in via Kratos → navigates to `/phpmyadmin/` → phpMyAdmin initiates signon.php flow → verify the UDS request carries the `ory_kratos_session` cookie value → whoami succeeds → shadow password returned. This closes review finding #6. Same-origin cookie sharing works because both `/` and `/phpmyadmin/` are served off the panel's host-only cookie. Migration: zero schema change for phpMyAdmin SSO, only the handler authz flips from JWT-in-query to session-cookie-over-UDS (plus the nginx header forwarding).

10. **2FA (M5c) migration: one-shot TOTP seed export + Admin API import, then truncate.** Our `user_totp_secrets` table stores the TOTP seed per user_id. Migration tool iterates rows, calls `PATCH /admin/identities/{id}` to add a `totp` credential with the seed. Kratos supports backup codes natively (`credentials.lookup_secret`); ours are hashed identically so re-import is possible. Users keep their existing TOTP app secret — no re-enrollment. After migration, old tables stay for 30 days (operator-verifiable rollback window), runbook-documented deletion.

11. **Scope boundaries: Hydra, Keto, Oathkeeper all deferred.** Hydra (OAuth 2 server) is the right tool for M16 Automation API tokens — a separate milestone. Keto (Zanzibar-style relationship-based authz) is overkill for our two-role model (admin/user). Oathkeeper (identity-aware reverse proxy) is what we'd use if panel-api were split into microservices — it isn't. v1 is Kratos-only.

12. **Rollback plan: every step on a feature branch; cutover in step 9 flips a feature flag. The flag picks ONE provider per request — never both.** `config.toml` gains `auth.provider = "kratos" | "legacy"`. In step 3, the middleware wrapper reads the flag ONCE at startup and dispatches to exactly one validator. When `auth.provider = "kratos"`: Kratos session cookie validation ONLY; any `Authorization: Bearer <jwt>` header is ignored (NOT an auth-success path). When `auth.provider = "legacy"`: JWT validation ONLY; any `ory_kratos_session` cookie is ignored. **Never** are both accepted on the same request — no precedence, no fallback, no "try legacy if Kratos fails." This closes review finding #1 (dual-mode auth bypass). Unit test in step 3 explicitly verifies a request with both a stale JWT and a Kratos cookie fails when the flag is `legacy` if JWT is invalid, and fails when the flag is `kratos` if cookie is invalid — regardless of the other credential's validity. Step 9 flips the default to `kratos` and documents the rollback (flip back, redeploy). After 30 days of stable Kratos operation, step 10 (future PR, not in this blueprint) removes the legacy code path entirely.

13. **Kratos version pin: `v1.3.1` (latest stable as of 2026-04).** `install.sh install_kratos` verifies SHA-256 against a vendored `install/kratos.sha256` file, same pattern as wp-cli + phpmyadmin. Upgrades bump the pin + checksum; Kratos's SQL migrations are idempotent and auto-run on startup.

14. **Self-signed cert boundary: Kratos talks to MariaDB over loopback (no TLS); panel-api talks to Kratos over loopback HTTP (no TLS).** Everything is on `127.0.0.1` inside the same host; adding TLS between same-host services is security theater. The public boundary (nginx → panel-api, nginx → /.ory/ → Kratos) is where TLS matters — unchanged from today.

15. **Session cookie name stays `ory_kratos_session` — we don't rename it.** Renaming means configuring Kratos's `session.cookie.name` and losing upstream defaults (the Kratos Admin UI, kratos-client libs, docs all assume `ory_kratos_session`). Cookie is `HttpOnly`, `Secure`, `SameSite=Lax`, `Path=/`, `Domain` unset (host-only). Same origin as the panel, so SameSite=Lax is sufficient for our GET navigations; POST flows to `/.ory/self-service/*` are same-origin, no cross-site XSRF concerns beyond what Kratos already handles via its CSRF token on form flows.

---

## 1. Steps / waves

| Step | Wave | Parallel? | Summary | Outputs |
|------|------|-----------|---------|---------|
| 1 — ADR-0034 + kratos.yml template | A | w/ 2 | Record all 15 key design decisions. Vendor `install/kratos.yml.tmpl` (identity schema JSON, cookie config, DSN placeholder, flow URLs, MFA settings). Vendor `install/kratos-identity-schema.json` (traits + credentials schema). | `docs/adr/0034-m20-kratos-identity.md` (next free ADR slot), `install/kratos.yml.tmpl`, `install/kratos-identity-schema.json` |
| 2 — install.sh + systemd unit + DB schema | A | w/ 1 | `install_kratos()`: download+verify binary (v1.3.1, SHA-256 pinned in `install/kratos.sha256`), install to `/usr/local/bin/kratos`, render `/etc/jabali-panel/kratos.yml` from template, create DB `jabali_kratos` + user, run `kratos migrate sql -y`, write `/etc/systemd/system/jabali-kratos.service` under `jabali.slice`, enable + start. nginx gains `location /.ory/ { proxy_pass http://127.0.0.1:4433/; }` block in the panel vhost template. Idempotent (re-runnable). | `install.sh` diff, `install/systemd/jabali-kratos.service`, `install/nginx/.ory-location.conf` snippet |
| 3 — Go: kratosclient + dual-mode auth middleware | B | w/ 4 | `panel-api/internal/kratosclient/` — Whoami(ctx, cookie) calls Kratos `/sessions/whoami`, returns identity or `ErrUnauthenticated`. 10s LRU cache keyed by cookie hash (SHA-256 of the cookie value, never the cookie itself) to cap whoami traffic. Middleware `RequireKratosSession` replaces half of `RequireAuth`. Feature flag `cfg.Auth.Provider == "kratos"` gates the new middleware; `"legacy"` keeps the JWT middleware active. Removes nothing — dual-mode so step 9 can cutover safely. Unit tests with a fake Kratos httptest.Server. | `panel-api/internal/kratosclient/{client,cache}.go`, `panel-api/internal/middleware/auth_kratos.go`, tests |
| 4 — Migration tool: `jabali kratos-migrate` + API user-create hook | B | w/ 3 | New Cobra subcommand. Reads `users`, for each row calls Kratos Admin API `POST /admin/identities` with `{traits: {email, username, is_admin}, credentials: {password: {config: {hashed_password: "$2a$..."}}}}`. Persists `kratos_identity_id` back onto `users.kratos_identity_id` (new nullable column, migration 000046). Dry-run flag prints the payload without writing. Idempotent via email lookup (skip already-migrated). Rolls back per-row on error (delete identity, continue). Migrates TOTP seeds in same pass if `cfg.Kratos.MigrateTOTP` is true. **Canary before batch**: first thing the tool does after startup probe is import a throwaway test identity, run `GET /sessions/whoami` against a login with its bcrypt-hashed password, then delete the canary. If canary fails, abort the batch with a clear error — NEVER start a batch against a Kratos that rejects our hash format. **API user-create hook (same step)**: the existing `POST /api/v1/admin/users` handler gains an inline call to Kratos Admin API `POST /admin/identities` in the same transaction: panel DB INSERT + Kratos identity create are atomic; if Kratos fails, the panel DB INSERT is rolled back. This keeps ADR-0003's "one write path = the API" property and closes review finding #10 — the reconciler never creates identities. During the migration window (steps 3-8, before cutover), new users created via API go into both systems; existing users are backfilled by the one-shot tool. | `panel-api/cmd/server/kratos_migrate_cmd.go`, `panel-api/internal/api/users.go` (inline Kratos call), migration 000046, tests |
| 5 — Refine authProvider + login UI rewrite | C | w/ 6 | `authProvider.login` POSTs the current form to `/.ory/self-service/login?flow=<id>`; `check` calls `/sessions/whoami`; `logout` POSTs to `/.ory/self-service/logout/browser`. New `/login` component fetches the flow on mount and renders Kratos's `ui.nodes` as AntD `Form.Item`s (generic renderer for `input/password/hidden/submit` node types). Impersonation + admin routing logic preserved (reads `is_admin` trait instead of JWT claim). Removes `accessToken` state entirely from `apiClient.ts`; axios just sends cookies. Removes the 401 refresh interceptor (no more refresh dance). Keeps the `withCredentials: true` flag. | `panel-ui/src/{authProvider,apiClient,identity}.ts`, `panel-ui/src/routes/Login.tsx`, `panel-ui/src/routes/Recovery.tsx`, unit tests |
| 6 — Impersonation (M5a) rewrite | C | w/ 5 | Admin endpoint `POST /api/v1/admin/impersonate/:userId` now (a) validates caller has `is_admin=true`, (b) calls Kratos Admin `POST /admin/identities/{kratos_id}/sessions` with a 15-min TTL, (c) returns `{ redirect_url: "/login?session_token=<one-shot>" }` where the token is a short-lived Kratos session-exchange token the panel swaps for the real cookie. Exit impersonation: panel calls Kratos `DELETE /admin/sessions/{session_id}` (the impersonated session) and the admin's original session cookie remains valid. Audit log table unchanged. | `panel-api/internal/api/impersonate.go`, `panel-ui/src/shells/admin/users/ImpersonateButton.tsx` |
| 7 — Break-glass CLI (M5b) + phpMyAdmin SSO (M7) | D | w/ 8 | `jabali admin-login <email>`: connects to Kratos Admin API (loopback), fetches identity by email (`GET /admin/identities?credentials_identifier=<email>`), calls `POST /admin/identities/{id}/sessions`, prints a full `https://<host>/login?session_token=<tok>` URL the operator pastes into a browser. Panel-api gains a thin `GET /api/v1/auth/exchange?session_token=<tok>` handler that verifies the token's freshness and sets the cookie. phpMyAdmin SSO (M7) handler on `/run/jabali/sso.sock` (existing UDS) switches its authz: instead of verifying a JWT, it parses the `Cookie:` header, calls Kratos whoami, looks up the shadow MariaDB password by identity ID, returns to signon.php. `sso.key` for at-rest encryption of shadow passwords is unchanged. | `panel-api/cmd/server/admin_login_cmd.go`, `panel-api/internal/sso/phpmyadmin_handler.go` |
| 8 — 2FA (M5c) migration — export + Admin API import | D | w/ 7 | One-shot tool `jabali kratos-migrate --totp-only` reads `user_totp_secrets` (and backup codes table), for each secret calls `PATCH /admin/identities/{id}/credentials` with `totp` + `lookup_secret` entries. Kratos accepts raw TOTP seeds (base32) and hashed backup codes identically to ours. Users keep their existing TOTP app secret — no re-enrollment. Runbook documents the 30-day retention on old tables. | tool additions to step 4's cmd, `plans/m20-kratos-runbook.md` stub |
| 9 — Cutover + E2E + runbook + BLUEPRINT flip | E | — | Flip `config.toml` default to `auth.provider = "kratos"`. **Cutover explicitly invalidates all legacy sessions — users must re-authenticate. This is documented as expected UX, not a regression.** The feature-flagged middleware (step 3) responds 401 on any request carrying a legacy `jabali_refresh` cookie when the flag is `kratos`, which triggers the SPA's unauthenticated-redirect to /login cleanly (no visible error). Add Playwright E2E: fresh install → user migrates → login via Kratos flow with CSRF token → session cookie set → app renders → logout → cookie cleared → /api/v1/me returns 401. Runbook covers: (a) cutover communication: "flipping the flag invalidates all active sessions; users re-login once"; (b) `kratos identities list`, `kratos identities get <id>`, `kratos sessions revoke <id>`, backup/restore of `jabali_kratos` schema (mysqldump), how to disable a compromised identity (`kratos identities update <id> --state=inactive`), MFA reset (`kratos identities patch <id> --set-totp=null`); (c) **Kratos DB loss recovery**: "the `users` table retains `kratos_identity_id` indefinitely — NOT truncated after 30 days. If Kratos's `jabali_kratos` schema is lost, restore from mysqldump; if no backup exists, re-run the migration tool with `--rebuild-kratos` (queues user password resets for re-enrollment)." This closes review finding #11. BLUEPRINT.md gains M20 section marked SHIPPED; ADR-0034 finalized. Leaves legacy JWT code paths behind the feature flag — removed in a future PR after 30 days of stable operation. | `tests/e2e/kratos-login.spec.ts`, `plans/m20-kratos-runbook.md`, `docs/BLUEPRINT.md` M20 entry |

**Dependency graph:**
- Wave A: 1 ∥ 2 (ADR + install.sh are independent)
- Wave B: 3 ∥ 4 (middleware + migration tool; tool is read-only-to-panel, middleware is new code with feature flag off — neither breaks main)
- Wave C: 5 ∥ 6 (SPA rewrite + impersonation backend — disjoint files)
- Wave D: 7 ∥ 8 (break-glass CLI + 2FA migration — disjoint files)
- Wave E: 9 alone (cutover is single-step, documents the flag flip)

**Model tiers:** Step 1 (ADR), Step 3 (session validator semantics + feature flag), Step 5 (SPA auth rewrite — touchy UX), Step 6 (impersonation — security-critical), Step 9 (cutover verification) → strongest. Steps 2, 4, 7, 8 → default.

**Rollback per step:**
- Step 1: `git revert` the ADR + vendored configs — zero runtime impact.
- Step 2: `systemctl stop jabali-kratos && systemctl disable jabali-kratos && rm /etc/systemd/system/jabali-kratos.service /etc/jabali-panel/kratos.yml /usr/local/bin/kratos`. Drop DB: `DROP DATABASE jabali_kratos`. nginx snippet reversal via `git revert`.
- Step 3: feature flag stays on `"legacy"` — new middleware is dormant. `git revert` to remove the code entirely.
- Step 4: delete all Kratos identities via `kratos identities delete <id>` loop (documented in runbook). `kratos_identity_id` column stays (nullable, harmless).
- Step 5: `git revert` the SPA commit. Old authProvider still works with legacy auth behind the flag.
- Step 6: `git revert` the impersonation commit. Old JWT impersonation is still in the codebase until the final removal PR.
- Step 7: `git revert`. Old JWT admin-login + JWT-query-string SSO remain functional.
- Step 8: `kratos identities patch <id> --clear-totp` loop to reset migrated TOTP credentials.
- Step 9: flip `auth.provider = "legacy"` in `config.toml`, restart panel-api. Full rollback to the old system. After 30 days, rollback window closes and legacy code is removed in a follow-up PR.

---

## 2. Out of scope (v1)

- **Hydra OAuth 2 server.** Reserved for M16 (Automation API tokens). Hydra integrates with Kratos via the login-consent flow; that wiring happens in M16 and requires M20 already in place.
- **Keto / Oathkeeper.** Our authz model is two roles (admin / user) plus resource ownership (domain belongs to user). A relationship-based policy engine is massive overkill. If we ever grow team-based access, reconsider.
- **Postgres for Kratos.** MariaDB is tier-2 supported upstream; our scale (<10k identities on the busiest hosts) is well within its comfort zone. Switch to Postgres only if a benchmark proves we need it.
- **OIDC federation (sign-in with Google/GitHub).** Kratos supports this via `selfservice.methods.oidc` — enable in a future PR when there's demand. v1 is password + TOTP + backup codes.
- **Passkeys / WebAuthn.** Kratos supports it (`selfservice.methods.webauthn`). Enable in a follow-up PR; not worth the UI work for v1 when existing customers already have TOTP.
- **Magic-link email login.** Kratos supports it; our current flows don't. Defer — not worth the SMTP config burden for v1.
- **Passwordless / SMS OTP.** Out of scope, no SMS gateway integration.
- **Multi-tenant identity isolation.** Single-tenant per host (every jabali install is one operator's infra). If we ever host-as-a-service we need tenant scoping.
- **Custom Kratos hooks (webhooks).** Adding webhooks to trigger panel-api actions on identity lifecycle events (create/delete/update) is tempting but the reconciler already converges DB state on its own cadence — keep the architecture simpler.
- **Kratos UI hosted at `/kratos-ui/`.** We're rendering flows in our own SPA. If operator feedback demands Kratos's native UI (it has better error handling for edge cases), enable as a fallback later.
- **Removing the legacy JWT code path in this milestone.** We keep it behind the feature flag for the 30-day rollback window. Removal is a follow-up PR after validation.

---

## 3. Risks + mitigations

| Risk | Impact | Mitigation |
|------|--------|-----------|
| Kratos startup fails on fresh install (bad DSN, missing migrations) | Panel won't reach /login | Install.sh runs `kratos migrate sql -y` synchronously and checks `systemctl is-active jabali-kratos` before `_ok`. Fail loud if Kratos doesn't come up. |
| Bcrypt cost mismatch (we use 12, Kratos default is 10) | New logins slow vs. old | Pin Kratos's `hashers.bcrypt.cost = 12` in kratos.yml. Document in ADR. |
| whoami loopback contention | Slow requests under load | 10s LRU cache on cookie-hash → identity. Benchmark in step 3 with `go test -bench`. If still slow, add a per-process in-memory cache with 60s TTL. |
| Impersonation session leakage (admin returns to their own session but impersonation cookie still active) | Privilege escalation | Exit-impersonation handler calls Kratos DELETE session + clears the impersonation cookie client-side. Audit log records the exit. |
| Migration tool aborts mid-run, leaving users in inconsistent state | Some users can login via Kratos, some via legacy | Per-user idempotent: skip if `users.kratos_identity_id` already set. Safe to re-run. Also record migration status in a table for retry. |
| Operator runs `kratos migrate sql` manually and corrupts schema | Kratos down, panel down | Runbook explicitly warns against this. install.sh is the only path; `jabali update` invokes it. |
| Passwords not accepted by Kratos despite bcrypt passthrough (hash format edge case) | Users locked out | Dry-run migration hits Kratos's identity-create endpoint with a test user first. On real migration, tool verifies by calling `/self-service/login/api` with the imported creds for one canary user. Aborts if canary fails. |
| Legacy JWT cookie lingers after cutover | Ghost sessions | Cutover step (9) adds a one-time `logout_all_legacy` response header that instructs the browser to clear the JWT cookie. Also, feature-flagged middleware returns 401 on any request with a legacy JWT when `auth.provider = "kratos"`. |
| `/run/jabali/sso.sock` auth flip breaks phpMyAdmin during Step 7 | Users can't reach phpMyAdmin mid-migration | Dual-mode sso handler: accept either a legacy JWT or a Kratos cookie; prefer Kratos. Drop JWT accept in the cleanup PR. |
| M5b break-glass session-token URL leaks via shell history / terminal scrollback | Privilege bypass | One-shot token (single-use, 2-min TTL). Panel-api rejects after first exchange. Documented in runbook. |

---

## 4. Out-of-band review checklist

Handed off to a strongest-model review agent in Phase 4. The reviewer should validate:

- [ ] Every step's context brief is self-sufficient (cold-start executable)
- [ ] Step 3's feature flag doesn't leave panel-api accepting BOTH auth sources simultaneously in a way that lets an attacker forge a session (e.g., present a JWT on one request and a Kratos cookie on another — do we trust the more-permissive?)
- [ ] Bcrypt passthrough actually works with Kratos 1.3.x (some releases quietly gated `hashed_password` behind a flag)
- [ ] Schema split: is there ANY reason for `jabali_panel` and `jabali_kratos` to share a transaction? If yes, this design fails.
- [ ] Impersonation: does `POST /admin/identities/{id}/sessions` in Kratos actually skip password verification, or does it require the target's credentials?
- [ ] Step 7's session-token exchange: is the one-shot-token generation cryptographically sound, or is it a JWT in disguise (reinventing the wheel we just removed)?
- [ ] CSRF: Kratos's `/self-service/*` endpoints use a CSRF cookie + token pattern. Our Refine SPA needs to read the CSRF token from the flow response and submit it. Does step 5 actually do this?
- [ ] Rollback on step 9: if `auth.provider = "legacy"` is flipped back, do existing Kratos sessions still work for a while (dual-mode), or does it hard-logout every user?
- [ ] Runbook (step 9): does it cover "my panel DB is fine but kratos DB is corrupted — can I rebuild from users table?"
- [ ] Missing: cookie SameSite interaction with iframe embeds (phpMyAdmin in a modal?) — is our `SameSite=Lax` correct?
- [ ] Missing: how do existing sessions (refresh tokens in `refresh_tokens` table) carry over? Do we force a re-login on cutover, or import sessions too? (Recommendation: force re-login — sessions are ephemeral, importing them is a minefield.)

---

## 5. Adversarial review log

Reviewed 2026-04-19 by strongest-model architect agent. Findings + resolutions:

### CRITICAL (blockers — all folded)

1. **Dual-mode middleware request-sequence auth bypass.** Plan v1 said step 3 would "speak both protocols behind the flag." This left ambiguity: could a request with both a stale JWT AND a Kratos cookie succeed on whichever validator allowed it? **Resolution:** Decision 12 rewritten to require the flag selects exactly ONE validator at startup. The non-selected credential is ignored, not fallback-checked. Step 3's test suite must explicitly cover a request with both credentials present — it must fail when the selected validator's credential is invalid, regardless of the other credential's validity.

2. **Bcrypt passthrough unverified on Kratos 1.3.x.** Plan v1 asserted Kratos accepts `$2a$`/`$2b$` hashes but didn't cite the version's config or test it. Mid-migration rejection would leave half the users stranded. **Resolution:** Decision 5 now pins `hashers.bcrypt.enabled: true` + `hashers.bcrypt.cost: 12` in the vendored `kratos.yml.tmpl` (step 1 output) and DISABLES automatic argon2id rehashing to keep rollback safe. Step 3 adds a startup canary (disposable identity create + login verify + delete); FATALs on rejection. Step 4's migration tool also runs a canary before the batch.

3. **Impersonation with TOTP — unspecified behavior.** Plan v1 asserted `POST /admin/identities/{id}/sessions` skips password verification but said nothing about MFA. If Kratos 1.3 enforces `aal=2` on admin-created sessions for TOTP-enabled identities, M5a rewrite fails silently. **Resolution:** Decision 7 now requires step 6 to include an explicit test: create identity with TOTP enabled, call admin session API, verify whoami passes without a TOTP code. If the test fails, the implementing agent must pivot to an alternative (temporary AAL downgrade or `aal_override` query param) before claiming step 6 complete. Runbook documents the TOTP-bypass behavior for operator-audit clarity.

4. **Break-glass "one-shot token" might be a JWT in disguise.** Plan v1 said "2-min TTL session-exchange token" without specifying whether it's a JWT, a Kratos session ID, or a DB nonce. A JWT would reinvent the wheel we're removing. **Resolution:** Decision 8 now specifies: the token IS the Kratos session token returned by `POST /admin/identities/{id}/sessions`. Panel-api's `/api/v1/auth/exchange` handler validates it via whoami and sets the session cookie. Panel-api never signs anything. Single-use is intrinsic (once the cookie is set, the URL is replayable but the browser already has the cookie — no auth escalation). If Kratos is dead, operators use `kratos sessions issue` CLI directly.

### HIGH (folded)

5. **CSRF token submission in the headless SPA flow.** Plan v1 said "we render those fields" without showing the CSRF plumbing. Missing the CSRF token is the #1 Kratos integration bug. **Resolution:** Decision 6 now shows the concrete renderer code that extracts `csrf_token` from `ui.nodes` and echoes it in the submit. Step 5 outputs include a unit test for the renderer against a recorded Kratos flow response fixture.

6. **nginx must forward the `Cookie:` header to the UDS SSO handler.** Plan v1 assumed the UDS request would carry the browser's cookie but didn't mandate the nginx config. Default proxy configs often DROP the Cookie header on FPM forwards. **Resolution:** Decision 9 now explicitly requires `proxy_set_header Cookie $http_cookie;` in the phpMyAdmin nginx location. Step 7's test verifies the UDS request carries the session cookie.

7. **Refresh-token invalidation on cutover is a silent UX regression if undocumented.** Plan v1 said nothing about what happens to users with active sessions when the flag flips. They'll all be forced to re-login. **Resolution:** Step 9 now explicitly documents the invalidation as expected UX. The feature-flagged middleware returns 401 on any stale legacy cookie when flag is `kratos`, which triggers the SPA's clean redirect to /login. Runbook has a "cutover communication" section.

### MEDIUM (folded)

8. **User creation during migration window — reconciler scope creep risk.** Plan v1 said "Reconciler gains no new responsibilities" but didn't specify how new users get into Kratos during steps 3-8. **Resolution:** Step 4 now adds an inline Kratos-create hook on the existing `POST /api/v1/admin/users` handler — atomic with the panel DB INSERT. This keeps ADR-0003's one-write-path property. Reconciler stays out.

9. **Argon2id rehash would break rollback.** If Kratos rehashes bcrypt hashes to argon2id on login, the `users.password_hash` column drifts and legacy auth fails on rollback. **Resolution:** Decision 5 now pins `hashers.algorithm: bcrypt` in kratos.yml.tmpl, disabling automatic rehashing.

10. **Reconciler stays out of Kratos creation.** Review confirmed plan intent; explicit language added in step 4 and Decision 12.

11. **30-day retention language was ambiguous.** Plan v1 said "old tables stay for 30 days" — could be read as "truncate `users` after 30 days." **Resolution:** Step 9 now explicitly states the `users` table is NOT truncated; `kratos_identity_id` is a permanent FK. Kratos DB loss recovery path documented.

### LOW (folded)

12. **Session cookie name.** Confirmed `ory_kratos_session` default is correct; no rename. Decision 15.

13. **Kratos DSN undocumented.** Added to step 2 + Decision 2: `user:password@tcp(localhost:3306)/jabali_kratos?parseTime=true` on the shared MariaDB instance.

_All CRITICAL + HIGH findings closed. MEDIUM findings 8-11 folded. LOW findings 12-13 addressed. Plan is ready for Wave A dispatch._

---

## 7. Spike findings (2026-04-19)

Ran Kratos 1.3.1 binary against a throwaway MariaDB schema on 10.0.3.13 (teardown afterwards). Tested the three Opus-review claims before dispatch.

### Validated

- **T1 — Bcrypt passthrough (Decision 5 + Review finding #2): ✓ WORKS.** All three prefixes (`$2y$`, `$2a$`, `$2b$`) accepted by `POST /admin/identities` as `credentials.password.config.hashed_password`. Subsequent `/self-service/login/api` with the original plaintext password succeeds; `/sessions/whoami` returns the imported identity with the correct traits. Plan Decision 5 stands as written.
- **T3a — CSRF plumbing (Decision 6 + Review finding #5): ✓ FLOW SHAPE CONFIRMED.** `GET /self-service/login/browser` returns a flow with `ui.nodes[]` containing `{name:"csrf_token", attributes:{value:"…"}, type:"input"}` exactly as the plan's example renderer assumes. The CSRF cookie is also set via `Set-Cookie: csrf_token_<hash>=…; HttpOnly`. SPA renderer code in Decision 6 is correct.

### Broken — plan revisions required

- **T2 — Impersonation (Decision 7 + Review finding #3): ✗ ENDPOINT DOES NOT EXIST.** `POST /admin/identities/{id}/sessions` returns `405 Method Not Allowed` (Allow: `DELETE, GET, OPTIONS`). Tried `/admin/sessions`, `/admin/impersonate`, `/admin/identities/{id}/impersonate` — all 404. **Kratos 1.3.1 self-hosted has no admin-session-mint API.** This feature exists in Ory Network (the SaaS) but not in the OSS build. Decision 7 + Step 6 must pivot.

  **Pivot options** (decide before Wave A):
  1. **Keep JWT-based impersonation unchanged.** Panel-api minting an "impersonation JWT" is orthogonal to Kratos — it's an admin-asserted override. Middleware checks for an `imp_token` cookie BEFORE consulting Kratos; if present and validated against our JWT secret, uses the impersonation claim instead. This means M5a stays on the legacy codepath after the M20 cutover — one feature retains its custom auth surface. Pro: minimal change, security model unchanged from today. Con: we kept a small piece of hand-rolled auth.
  2. **Recovery-code-based "login as" flow.** Admin calls `POST /admin/recovery/code` on Kratos (verified working in spike — returns `{recovery_code, recovery_link}`). CLI/UI prints a recovery URL the admin opens in a new browser tab. User flow completes with forced password reset. Pro: Kratos-native. Con: Destroys the target's password; can't "exit impersonation" cleanly; user notices a reset.
  3. **Direct session-row insert.** Panel-api writes a session directly to Kratos's DB tables (`sessions` + `session_devices`). Works but fragile across Kratos upgrades. Rejected.

  **Recommended pivot: option 1.** Keep M5a JWT-based. Document in ADR-0034 that impersonation retains the legacy auth pattern because Kratos self-hosted doesn't support admin-minted sessions. Re-evaluate if/when Kratos adds this upstream (there's an open RFC).

- **T3b — Break-glass session-token URL exchange (Decision 8 + Review finding #4): ✗ API-MODE SESSIONS ARE NOT COOKIE-COMPATIBLE.** API-flow `session_token` (e.g. `ory_st_SOjbvh…`) is rejected by `GET /sessions/whoami` when presented as `Cookie: ory_kratos_session=…`. Kratos distinguishes between API-mode sessions (header transport) and browser-mode sessions (cookie transport) — they are stored with different `nid`/`aal` assumptions and the whoami guard refuses to cross them. The session-id alone is also not a valid cookie value. **Panel-api cannot convert an admin-generated session_token into a browser-session cookie.** Decision 8 must pivot.

  **Pivot options** (decide before Wave A):
  1. **Recovery-code-based break-glass.** `jabali admin-login <email>` calls `POST /admin/recovery/code` on Kratos, prints the recovery URL. Admin opens URL in browser, completes the flow (forced password reset), is logged in. Pro: Kratos-native, secure by design (single-use, expiring). Con: forces password reset — operator must accept this as the cost of break-glass.
  2. **CLI drives a browser-mode flow with curl.** `jabali admin-login <email>` makes a local cookie-jar session: hits `/self-service/login/browser`, extracts CSRF, POSTs credentials (from an admin-set temporary password), captures the `ory_kratos_session` cookie, prints a URL like `https://<host>/auth/cookie-claim?one_shot=<nonce>` that sets the captured cookie into the operator's browser. Pro: no password reset. Con: requires a panel-api "cookie claim" endpoint with its own one-shot nonce (reinvents a small piece of the problem we're trying to remove). Panel-api signs the nonce (HMAC of session-id + expiry) — it's NOT a full JWT, just a short-lived claim ticket.
  3. **Keep JWT-based break-glass.** Same argument as Option 1 for impersonation — M5b stays on the legacy codepath post-cutover.

  **Recommended pivot: option 1.** `/admin/recovery/code` is Kratos's canonical break-glass primitive; accepting the "forced password reset as break-glass cost" is a reasonable trade. Document in runbook: "break-glass resets the user's password — the operator must communicate this to the user afterwards." Users who need break-glass without reset should use option 3's legacy JWT path, retained behind a flag.

### Additional findings surfaced by the spike

- **K-1 — Kratos OSS binary has NO SQLite support.** The official `kratos_1.3.1-linux_64bit.tar.gz` refuses any `sqlite:` DSN with `sqlite3 support was not compiled into the binary`. `memory` DSN is treated as sqlite internally and fails the same way. **install.sh MUST use MariaDB from day one — no SQLite fallback path.** Development environments that want an embedded DB must build Kratos from source with `-tags sqlite` (out of scope for install.sh).

- **K-2 — MariaDB strict mode breaks Kratos migrations.** `STRICT_TRANS_TABLES` is the Debian MariaDB default. Kratos's migration `20200317160354000002_create_profile_request_forms.mysql.up.sql` does an `INSERT … SELECT` that leaves `created_at` NULL, which strict mode rejects with `Error 1364 (HY000): Field 'created_at' doesn't have a default value`. **Kratos's DSN must include `sql_mode=%27NO_ENGINE_SUBSTITUTION%27`** (URL-encoded). `install.sh install_kratos` must render this into `/etc/jabali-panel/kratos.yml` — add to Step 2 vendored template. Otherwise `kratos migrate sql` fails on first install, every time.

- **K-3 — Kratos identity creation requires `schema_id` field explicitly.** The Admin API doesn't default to the configured default schema if `schema_id` is omitted — it returns 400. Migration tool must send it.

- **K-4 — `POST /admin/recovery/link` returns 404 ("disabled by system administrator") unless the recovery method is explicitly enabled.** The panel's kratos.yml must set `selfservice.methods.link.enabled: true` OR `selfservice.methods.code.enabled: true` depending on which break-glass primitive we adopt. Recovery via code works out-of-the-box; recovery via link is gated. Document in the vendored template.

### Findings folded into the plan (this section)

1. Status line updated to reflect post-spike state.
2. Step 2 outputs must include: `sql_mode=NO_ENGINE_SUBSTITUTION` in the Kratos DSN (K-2); `selfservice.methods.code.enabled: true` in kratos.yml (K-4); explicit "MariaDB required, SQLite unsupported" note in ADR-0034 (K-1).
3. Step 4 migration tool must include `schema_id` in every identity-create payload (K-3).
4. Decision 7 (impersonation) MUST pivot to Option 1 (keep JWT-based M5a) — Step 6 rewrites accordingly.
5. Decision 8 (break-glass) MUST pivot to Option 1 (recovery-code-based) — Step 7 rewrites accordingly.

### Open decision for the operator before Wave A dispatch

The impersonation + break-glass pivots both recommend keeping one foot in the legacy auth world. The plan's original premise ("complete replacement of custom JWT") is no longer true — we'll have a Kratos-primary system with a JWT-fallback for two admin-only features. That's still a huge reduction in the custom-auth surface, but it's worth acknowledging explicitly. Confirm the pivots before dispatching Wave A.

---

## 6. Related work

- ADR-0001 (Go agent over NDJSON UDS) — unchanged, Kratos doesn't touch the agent.
- ADR-0003 (one write path = the API) — preserved, panel-api still owns all writes that aren't identity-scoped.
- M5a (admin impersonation) — rewritten in step 6, semantics preserved.
- M5b (break-glass CLI) — rewritten in step 7.
- M5c (TOTP 2FA) — data migrated in step 8, UI flow via Kratos in step 5.
- M7 Tranche E (phpMyAdmin SSO) — authz flipped in step 7, encryption surface unchanged.
- M16 (Automation API) — blocked on M20; Hydra integration is a separate milestone.
