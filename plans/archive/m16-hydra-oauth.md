# M16 — Ory Hydra OAuth 2 / OIDC (WordPress SSO first consumer)

**Status:** Blueprint — not yet dispatched. Needs adversarial review before Wave A.
**Branch:** `m16/hydra-identity` (this worktree, wt-a).
**Goal:** Deploy self-hosted Ory Hydra as an OAuth 2 / OIDC provider layered on top of Kratos. First consumer is WordPress SSO; architecture scales to any OIDC-capable app and (follow-up PR) the Automation API (M15 machine-to-machine tokens).

**Non-goals (explicit, per 2026-04-20 scope):**
- phpMyAdmin SSO migration — stays on the existing nonce pattern (`phpmyadmin_sso_tokens` + UDS exchange). Rationale: phpMyAdmin isn't OIDC-native; an OIDC-fronted signon.php still needs the shadow-password exchange, so migration adds plumbing without removing plumbing. Revisit only if phpMyAdmin ever moves to a separate subdomain or we add external-IdP federation.
- Automation API (client_credentials grant) — the Hydra foundation lands first with WordPress SSO as the proof-of-pattern. Client-credentials + scoped panel-api middleware ships as a follow-up PR, not in M16's initial cut.
- External IdP federation (Google/GitHub OIDC) — Hydra supports it via Kratos's `selfservice.methods.oidc`; enabling it is a config flip, not in-scope for M16.

---

## 0. Key design decisions

1. **Hydra runs as a separate systemd unit, not embedded in panel-api.** Upstream Go binary, pinned version, installable/upgradable via `install.sh install_hydra()` independent of panel-api's release cadence. Unit: `jabali-hydra.service`, `Type=simple`, `Restart=on-failure`, under `jabali.slice` for cgroup parity with `jabali-kratos`. Mirrors the M20 Kratos pattern exactly.

2. **Binds loopback-only: `127.0.0.1:4444` (public), `127.0.0.1:4445` (admin).** Admin endpoint (client CRUD, introspection, consent-accept) is NEVER exposed off-host. Public endpoint serves `/oauth2/auth`, `/oauth2/token`, `/.well-known/openid-configuration`, `/userinfo` — these get fronted by panel-api's in-process reverse proxy.

3. **Panel-api proxies `/oauth2/*` and `/.well-known/openid-configuration` in-process** (same pattern as the M20 `/.ory/*` proxy in `panel-api/internal/app/kratos_proxy.go`). Same-origin cookies, no nginx vhost edits, no cross-origin CORS. OIDC clients see `https://<panel-domain>/oauth2/*` and never know Hydra is on loopback. The proxy registration happens in the same `app.go` flow that registers the Kratos proxy.

4. **MariaDB schema `jabali_hydra`, not Postgres.** Parallel to `jabali_kratos`. Reuses the existing MariaDB instance with `sql_mode=NO_ENGINE_SUBSTITUTION` in the DSN (same K-2 workaround from M20 — Hydra's migrations trip on MariaDB's strict mode the same way Kratos's did). Hydra runs its own migrations via `hydra migrate sql -y` at install time and on every upgrade.

5. **Login flow delegates to Kratos via the login-consent flow.** Hydra's model:
   - Client hits `/oauth2/auth` → Hydra redirects to panel-api's `/oauth2-login?login_challenge=<id>`.
   - Panel-api checks for an `ory_kratos_session` cookie. If present + valid (whoami passes), auto-accept the login challenge with the Kratos identity's panel user id. If absent, redirect to `/login` (Kratos flow), then back to `/oauth2-login` once the user signs in.
   - Hydra then redirects to panel-api's `/oauth2-consent?consent_challenge=<id>`.
   - Panel-ui renders the consent screen listing requested scopes and the client's name. User clicks "Allow" → panel-api accepts the consent challenge with grant-scope, Hydra issues tokens.
   - For trusted first-party clients (`client_metadata.trusted = true`, set at client registration time) the consent step auto-accepts without UI interaction — used for panel-internal WP installs so the user doesn't see a consent screen for their own infrastructure.

6. **Panel-ui owns the consent UI.** New route `/oauth2-consent` in the existing shell (not a separate subdomain). Renders requested scopes with human-readable labels (mapping defined in `panel-api/internal/hydraclient/scope_labels.go`), the client name, and Allow/Deny buttons. Reuses the existing AntD shell so it looks like the rest of the panel. For untrusted clients this screen MUST render — auto-accept is only for `trusted=true` clients whose metadata was set at panel-managed install time.

7. **Per-install OIDC client provisioning via the apps framework.** When a user installs a WordPress app, the installer (`panel-agent` → agent command, and/or the panel-api applications handler) calls Hydra Admin API to create an OIDC client with:
   - `client_name`: `WordPress @ <domain>/<subdir>`
   - `redirect_uris`: `[ "https://<domain>/<subdir>/wp-admin/admin-ajax.php?action=openid-connect-authorize" ]` (default for OpenID Connect Generic plugin; configurable per-install if the operator changes the callback)
   - `grant_types`: `["authorization_code", "refresh_token"]`
   - `response_types`: `["code"]`
   - `scope`: `"openid email profile"`
   - `token_endpoint_auth_method`: `"client_secret_post"` (plugin compatibility)
   - `metadata.trusted`: `true` (auto-consent for panel-managed installs)
   - `metadata.application_install_id`: the ULID of the row in `application_installs`, so we can reverse-lookup
   
   Returns `{client_id, client_secret}`. Both persist on `application_installs` (new columns `oidc_client_id VARCHAR(64)`, `oidc_client_secret_enc VARBINARY(512)`, the latter AES-GCM-sealed with the existing `sso.key` envelope). On install deletion, the matching Hydra client is also deleted (compensating-transaction — if Hydra delete fails, log + continue; orphan clients are harmless).

8. **WordPress OIDC integration: "OpenID Connect Generic" plugin, auto-installed + auto-configured by the installer.** The plugin is open-source (GPL, ~200k active installs), supports PKCE, supports `client_secret_post`, maps OIDC `sub` to WP user via email lookup. Installer downloads the plugin tarball (SHA-256 pinned in `install/openid-connect-generic.sha256`, re-verified at each install), unzips into `<wordpress-root>/wp-content/plugins/daggerhart-openid-connect-generic/`, and writes a `wp-config.php` stanza plus an `openid-connect-generic-settings.json` file the plugin consumes on first boot. User-facing surface: "Login with Jabali" button on the WP login page. Backend: PKCE authorization code flow to `https://<panel-domain>/oauth2/auth`.

9. **User mapping: OIDC `sub` → WP user via email.** Plugin is configured to look up WP users by the `email` claim. If a WP user with that email doesn't exist AND the plugin's "auto-create user" setting is on, a new WP user is created with role=subscriber (configurable per-install — default role is "subscriber" because a panel user isn't automatically a WP admin just because they own the WP install; the install owner gets `role=administrator` at install time via a separate wp-cli call. Login happens for anyone who authenticates via panel, but they only get admin if the installer explicitly granted it.)

10. **Version pin: Hydra v2.4.x (latest stable as of initial dispatch).** `install.sh install_hydra` verifies SHA-256 against `install/hydra.sha256`, same pattern as Kratos/wp-cli/phpmyadmin. Upgrades bump the pin + checksum; Hydra's SQL migrations are idempotent and auto-run on startup.

11. **Rollback plan.** Hydra is additive — turning it off doesn't break existing auth. Rollback = `systemctl stop jabali-hydra && systemctl disable jabali-hydra`, followed by `git revert` of the M16 merge. WordPress installs with OIDC configured will see "SSO temporarily unavailable" on the login page; the WP OIDC plugin falls through to the classic wp-login form, so users can still log in with per-install WP passwords (this is the plugin's default "OIDC optional" behavior — we enable it explicitly in Decision 8). No data loss; `application_installs.oidc_client_id` stays, and re-enabling Hydra is a single migration re-run + service start.

12. **Scope + claim shape.** M16 supports `openid`, `email`, `profile`. No custom scopes in the initial cut. Claims in the ID token:
    - `sub`: panel `users.id` ULID (not Kratos UUID — consistent with how the panel's own middleware resolves identities).
    - `email`: panel user email
    - `name`: `<first_name> <last_name>` if set, else email local-part
    - `jabali.is_admin`: boolean, namespaced to avoid collisions. Emitted so apps can choose role mappings if they care; WP's default plugin ignores it and assigns role via plugin config.
    
    Additional scopes (like `panel:api`) are deferred to the Automation API follow-up PR.

13. **Session lifetime.** Hydra `ttl.login_consent_request = 15m`, `ttl.access_token = 30m`, `ttl.refresh_token = 720h` (30 days), `ttl.id_token = 30m`, `ttl.auth_code = 10m`. Matches the M20 Kratos session lifetime (24h) so a user's panel session outliving their OIDC session is the common case (user re-initiates SSO silently). Refresh tokens rotate on use.

14. **Consent grant persistence.** For `trusted=true` clients (panel-managed WP installs) the consent is auto-granted and no `hydra_oauth2_consent` row survives past the immediate flow — skip_consent behavior. For untrusted clients (if we ever expose them) consent is persisted with a default `remember=true, remember_for=720h` so users don't see the consent screen on every login within the month.

15. **Audit log.** Every login-accept, consent-accept, token-issue, and token-introspection event emits a structured `slog` line with `event=hydra_*`, `client_id`, `panel_user_id`, `outcome`. Matches the M20 audit log format so both streams can be grepped with the same tooling. Refresh-token reuse (a sign of token theft) is detected by Hydra automatically and emits a `hydra_refresh_reuse_detected` event that the panel treats as CRITICAL — audit log + revoke all sessions for that client-user pair.

16. **Testing strategy.** Unit tests with a fake Hydra admin server (httptest.Server). Integration E2E: Playwright spec that spins up a mock Hydra + drives the full flow (panel login → SSO button → consent → token issue). Full end-to-end against real Hydra runs in the optional `make test-e2e-live` path (requires a test VM with Hydra running, not in CI by default).

---

## 1. Steps / waves

| Step | Wave | Parallel? | Summary | Outputs |
|------|------|-----------|---------|---------|
| 1 — ADR-0036 + hydra.yml template + vendored scope-labels | A | w/ 2 | Record all 16 key design decisions. Vendor `install/hydra.yml.tmpl` (DSN placeholder with sql_mode workaround, URL bindings, TTL knobs, login/consent URLs). Vendor `install/hydra.sha256` with the pinned binary hash. Vendor `panel-api/internal/hydraclient/scope_labels.go` with human-readable scope descriptions. | `docs/adr/0036-m16-hydra-identity.md`, `install/hydra.yml.tmpl`, `install/hydra.sha256`, `panel-api/internal/hydraclient/scope_labels.go` |
| 2 — install.sh + systemd unit + DB schema + in-process proxy | A | w/ 1 | `install_hydra()` in `install.sh`: download+verify binary (v2.4.x, SHA-256 pinned), install to `/usr/local/bin/hydra`, render `/etc/jabali-panel/hydra.yml` from template, create DB `jabali_hydra` + user, run `hydra migrate sql -y`, write `/etc/systemd/system/jabali-hydra.service` under `jabali.slice`, enable + start. Idempotent (re-runnable). Wire the in-process reverse proxy in `panel-api/internal/app/hydra_proxy.go` exposing `/oauth2/*`, `/.well-known/openid-configuration`, `/userinfo`, `/.well-known/jwks.json` — mirrors `kratos_proxy.go`. | `install.sh` diff, `install/systemd/jabali-hydra.service`, `panel-api/internal/app/hydra_proxy.go` + test |
| 3 — hydraclient Go package | B | w/ 4 | `panel-api/internal/hydraclient/`: `Client` with admin-API methods (`CreateClient`, `UpdateClient`, `DeleteClient`, `GetClient`, `ListClients`, `AcceptLoginRequest`, `AcceptConsentRequest`, `RejectLoginRequest`, `RejectConsentRequest`, `IntrospectToken`, `RevokeToken`). All return typed structs; ctx-aware; short per-call timeouts (5s). Admin URL via `cfg.Auth.Hydra.AdminURL`. Unit tests against a fake httptest.Server. Client secrets returned by `CreateClient` are surfaced to callers for one-time pickup — callers are responsible for AES-GCM-sealing them before DB persist. | `panel-api/internal/hydraclient/{client,consent,tokens}.go` + tests |
| 4 — Login + consent challenge handlers | B | w/ 3 | New routes in `panel-api/internal/api/oauth2_flow.go`, gated by `RequireKratosSession`:<br>`GET /oauth2-login?login_challenge=<id>` — fetch challenge metadata from Hydra, check that the Kratos session user matches (or kick to `/login?return_to=<same-url>`), accept with `subject=<panel-user-id>` + identity_provider_session_id (opaque string referencing the Kratos session so Hydra can invalidate tokens when the Kratos session is revoked).<br>`GET /oauth2-consent?consent_challenge=<id>` — fetch challenge, look up the client by ID, check `metadata.trusted`. If trusted → accept immediately with the requested scopes. If not trusted → render the consent page (see Step 5).<br>`POST /oauth2-consent/accept` — the consent form's submit target; validates CSRF token, calls `AcceptConsentRequest` with the approved scope set.<br>`POST /oauth2-consent/deny` — user rejected; calls `RejectConsentRequest`. | `panel-api/internal/api/oauth2_flow.go` + test |
| 5 — panel-ui consent screen | C | alone | New route `/oauth2-consent` in the React SPA. Fetches the challenge metadata from a new read-only panel endpoint `GET /api/v1/oauth2/consent/:challenge` (returns client name, requested scopes, user who will be authenticated). Renders an AntD `Card` with the scopes (mapped via `scope_labels.go`), an `Allow` primary button and a `Deny` secondary button. Allow POSTs to `/oauth2-consent/accept` with the challenge id + CSRF token, deny POSTs to `/oauth2-consent/deny`. Stays in the same shell (AdminLayout/UserLayout based on role) — consent for your own account isn't a context-switch. | `panel-ui/src/pages/OAuth2Consent.tsx` + test, new route wired in `App.tsx` |
| 6 — Apps framework: OIDC client minting on install | D | w/ 7 | `application_installs` table gains `oidc_client_id VARCHAR(64)` (nullable, unique) + `oidc_client_secret_enc VARBINARY(512)` (AES-GCM sealed with sso.key, nullable). Migration 000050. Applications registry descriptor gains `OIDCCallbackPath string` — for WP the default is `/wp-admin/admin-ajax.php?action=openid-connect-authorize`. At install time (POST /api/v1/applications handler), after the DB + nginx + install-command succeeds, call `hydraclient.CreateClient` with the rendered redirect URI, store the returned `client_id` + AES-GCM-sealed `client_secret` on the row. Compensating transaction: if client create fails, the install rolls back (same pattern as the Kratos identity create in the users handler). On install deletion, delete the Hydra client first; any failure logs but doesn't block panel row deletion (orphan clients are harmless). | migration 000050, `panel-api/internal/api/applications_service.go` diff, `panel-api/internal/apps/wordpress.go` diff (OIDCCallbackPath) |
| 7 — WordPress OIDC plugin packaging + install-time configuration | D | w/ 6 | Vendor `install/openid-connect-generic.<version>.zip` (SHA-256 pinned in `install/openid-connect-generic.sha256`). At WordPress install time, `panel-agent`'s wordpress_install command:<br>1. Unzips the plugin into `<wp-root>/wp-content/plugins/daggerhart-openid-connect-generic/`.<br>2. Writes `<wp-root>/wp-content/plugins/daggerhart-openid-connect-generic/openid-connect-generic-settings.php` (or uses the wp-cli `option update` path) with: `client_id`, `client_secret` (from decrypted DB row via a targeted admin-API callback), `endpoint_login = https://<panel-domain>/oauth2/auth`, `endpoint_token = https://<panel-domain>/oauth2/token`, `endpoint_userinfo = https://<panel-domain>/userinfo`, `scope = "openid email profile"`, `identity_key = email`, `create_if_does_not_exist = true`, `enforce_privacy = false` (OIDC optional — classic WP login still works as fallback per Decision 11).<br>3. Runs `wp plugin activate daggerhart-openid-connect-generic --path=<wp-root>`.<br>4. On the first run, wp-cli is invoked to ensure the install owner's WP user has role=administrator. | install/ plugin archive, `panel-agent/internal/commands/wordpress_install.go` diff |
| 8 — Playwright E2E spec | E | alone | `panel-ui/tests/e2e/oauth2-wordpress.spec.ts`: stubs the Hydra admin API + consent flow, drives the full path: panel login → click SSO button on a mocked WP login → consent screen renders (or auto-accepts for trusted=true) → token issued → WP logs the user in. Also: consent-deny flow returns to WP login with an error. Covers both the trusted (no UI) and untrusted (UI shown) branches. Runs in CI. | `panel-ui/tests/e2e/oauth2-wordpress.spec.ts`, `fixtures.ts` gains a `mockHydra()` helper |
| 9 — Cutover + runbook + BLUEPRINT flip | E | — | Enable the feature by default on fresh installs (install.sh always runs install_hydra). On in-place upgrades, `jabali update` detects the absence of `jabali-hydra.service` and runs install_hydra idempotently. Runbook (`plans/m16-hydra-runbook.md`) covers: (a) how a user initiates WordPress SSO, (b) operator ops: list/revoke/rotate OIDC clients via `hydra clients` CLI against the admin URL, (c) token introspection (`hydra token introspect`), (d) session revocation (revoking Kratos also revokes all Hydra tokens because login acceptance includes the Kratos session id as `identity_provider_session_id` — verify this in step 4's test), (e) backup+restore of `jabali_hydra` schema, (f) what to do when OIDC plugin breaks (fall back to classic WP login). BLUEPRINT.md gains M16 section marked SHIPPED; ADR-0036 finalized. | `plans/m16-hydra-runbook.md`, `docs/BLUEPRINT.md` M16 entry |

**Dependency graph:**
- Wave A: 1 ∥ 2 (ADR + install.sh/systemd/proxy are independent; step 2 is dispatchable once step 1's hydra.yml.tmpl is vendored)
- Wave B: 3 ∥ 4 (hydraclient is consumed by the handlers; the handlers can be stubbed against a fake client during parallel dev)
- Wave C: 5 alone (SPA consent page)
- Wave D: 6 ∥ 7 (apps-framework changes + WP plugin packaging; neither depends on the other, both are dispatchable once Wave B's handlers exist)
- Wave E: 8 alone, then 9 (E2E exercises everything; runbook + BLUEPRINT flip last)

**Model tiers:** Step 1 (ADR), Step 4 (consent-flow semantics — easy to get wrong, security-sensitive), Step 6 (compensating transaction on install path), Step 8 (E2E correctness across mocked flows) → strongest. Steps 2, 3, 5, 7, 9 → default.

**Rollback per step:**
- Step 1: `git revert` — zero runtime impact.
- Step 2: `systemctl stop jabali-hydra && systemctl disable jabali-hydra && rm /etc/systemd/system/jabali-hydra.service /etc/jabali-panel/hydra.yml /usr/local/bin/hydra`. Drop DB: `DROP DATABASE jabali_hydra`. In-process proxy reversal via `git revert`.
- Step 3: `git revert` — unused code, no runtime impact.
- Step 4: `git revert`. Any in-flight consent flows error out; minor user-visible UX hit for ~15 minutes (challenge TTL).
- Step 5: `git revert`. UI route 404s; backend handlers keep working.
- Step 6: `git revert` migration 000050 via 000050_down.sql. Any existing `oidc_client_id` values are orphaned (Hydra clients still exist); clean up via Hydra CLI manually.
- Step 7: `git revert` panel-agent diff. Existing WP installs keep their plugin + settings; new installs skip OIDC setup.
- Step 8: `git revert`. No runtime impact.
- Step 9: `git revert`. `jabali-hydra.service` stays running; BLUEPRINT flip is purely doc.

---

## 2. Out of scope (M16) + explicit deferrals

- **Automation API (client_credentials grant + scoped panel-api middleware).** Hydra supports client_credentials natively. Follow-up PR on top of M16: `panel-api/internal/middleware/oauth2.go` validates `Authorization: Bearer <token>` via `hydraclient.IntrospectToken`, checks required scope, populates `ginctx.Claims`. Scoping to specific routes via `RequireScope("panel:domains:write")` is a new middleware. Deferred because Hydra's base infrastructure has to ship first; wiring client_credentials is ~2 days on top.
- **External IdP federation.** Kratos supports `selfservice.methods.oidc` (upstream config). Not enabled in M16; operators who want "sign in with Google" add it to `/etc/jabali-panel/kratos.yml` and restart Kratos. No panel-api changes needed.
- **phpMyAdmin SSO migration.** See non-goals above.
- **OIDC Dynamic Client Registration (RFC 7591).** Hydra supports it; we don't enable the public endpoint because every client is panel-managed. If a user ever needs to register a client out-of-band, they use `hydra clients create` via SSH; no public registration endpoint.
- **User-facing OAuth 2 client management UI.** An admin Settings page "Connected Applications" listing installed-app OIDC clients with revoke buttons is a nice-to-have. Deferred; operators use `hydra clients list` on the shell. Revoking via Hydra invalidates all existing tokens.
- **JWKS rotation.** Hydra rotates its signing key on its own schedule. We don't expose a rotation trigger via the panel; operators can run `hydra janitor` or `hydra keys` against the admin URL for manual rotation. Documented in the runbook.
- **`hydra-maester` (Kubernetes operator).** Irrelevant — we're not on K8s.
- **Oathkeeper (identity-aware reverse proxy).** Irrelevant — panel-api is a single Go service, not a microservice mesh. If we ever split, revisit.

---

## 3. Risks + mitigations

| Risk | Impact | Mitigation |
|------|--------|-----------|
| Hydra startup fails on fresh install (bad DSN, missing migrations) | Panel SSO broken; existing auth still works | `install.sh` runs `hydra migrate sql -y` synchronously and checks `systemctl is-active jabali-hydra` before `_ok`. Fail loud if Hydra doesn't come up. Existing Kratos auth is unaffected — Hydra is additive. |
| `sql_mode=STRICT_TRANS_TABLES` breaks Hydra migrations (K-2 from M20) | Install.sh fails at migrate step | kratos.yml.tmpl already documents this; hydra.yml.tmpl pins `sql_mode=NO_ENGINE_SUBSTITUTION` in the DSN from day 1. |
| Login challenge loop (Hydra redirects to /oauth2-login, which redirects back to /login, which redirects to...) | User stuck in redirect loop | Step 4's handler detects when a `login_challenge` is still present after the Kratos login completes (the `return_to` query param round-trips it); explicit check + accept instead of re-kicking to Hydra. E2E test covers this. |
| Consent auto-accept for `trusted=true` is a privilege-escalation surface if an attacker registers a client with `metadata.trusted=true` | Attacker bypasses consent for any scope | `CreateClient` in `hydraclient` REJECTS any caller-supplied `trusted` metadata — only the apps-framework install handler sets it, and it's a server-side add after request validation. Unit test verifies an HTTP POST with `metadata.trusted=true` in the body gets stripped. |
| WP OIDC plugin fails to log user in because of email mismatch (panel email ≠ WP user email) | User sees "login failed" on WP | Plugin setting `identity_key = email` + `create_if_does_not_exist = true` means a new WP user with that email is auto-created if none exists. Install owner's email is used at install time; matches should be stable. Runbook covers the manual reconciliation path. |
| Refresh token reuse (token theft) | Compromised client replays a stolen refresh token | Hydra detects reuse automatically and revokes the token family. Panel audit log captures the event as CRITICAL; ops runbook has a "refresh reuse detected" section. No panel-side action needed beyond the audit log. |
| Client secret leakage in logs | Any log line capturing the admin-API response leaks the secret | `hydraclient.CreateClient` return type is `CreateClientOutput{ClientID, ClientSecret}` with `ClientSecret` implementing the `Stringer` interface to return `"[REDACTED]"`. Structured logs never see the raw secret. Unit test asserts this. |
| WP plugin update (from upstream) breaks OIDC config schema | Existing WP installs start failing SSO | Pin the plugin version in `install/openid-connect-generic.sha256`; updates are deliberate repo commits, not auto-pulled. Plugin has stable settings schema (~5 years); low actual risk. |
| Hydra binary doesn't support MariaDB at some version (Postgres-only) | Install-time failure | Verified: Hydra has maintained MariaDB/MySQL support throughout 2.x. Pin to 2.4.x; upgrade path is tested at version bump time, not at every install. |
| Simultaneous Hydra client creates on the same install (race) | Duplicate clients | `application_installs` already has per-user uniqueness; the installer is synchronous within a single request, and the compensating-transaction unwinds any orphans. Tests cover the "two requests race" case. |
| Panel-ui and panel-api disagree on consent scope labels | User approves more than they think | Scope labels are in `panel-api/internal/hydraclient/scope_labels.go` (single source of truth), returned by `GET /api/v1/oauth2/consent/:challenge`. panel-ui never hardcodes labels. Unit test asserts the map covers every scope Hydra is configured to issue. |

---

## 4. Out-of-band review checklist

Handed off to a strongest-model review agent in Phase 4. The reviewer should validate:

- [ ] Every step's context brief is self-sufficient (cold-start executable)
- [ ] Step 4's login-accept flow includes the Kratos `identity_provider_session_id` so revoking the Kratos session invalidates tokens (critical invariant — without it, a logged-out user can still replay OIDC tokens until their natural TTL)
- [ ] Step 4's consent-auto-accept gate cannot be bypassed by attacker-supplied `metadata.trusted=true` (verify server-side strip)
- [ ] Step 6's compensating transaction unwinds the Hydra client if the panel install row insert fails AFTER client creation (verify both directions)
- [ ] Step 7's plugin settings are written atomically — no half-configured plugin state surviving a crash mid-install
- [ ] Step 9's runbook covers the "Hydra DB lost, panel DB fine" recovery (similar to M20's Kratos DB loss section)
- [ ] CSRF protection on `POST /oauth2-consent/{accept,deny}` — Kratos middleware alone isn't enough; the consent challenge id must be verified as a signed/bound token (Hydra returns one; use it)
- [ ] Scope label map covers every scope Hydra is configured to issue (fail-loud on unknown scope rather than falling back to raw name)
- [ ] Integration test: revoking a user in the panel (SetAdmin=false, delete) immediately revokes all their Hydra tokens for all clients
- [ ] The `/oauth2-consent` UI doesn't allow approval for a user who isn't the consent flow's subject (guard against the "click allow on behalf of another user in the same tab" scenario)
- [ ] Client secret AES-GCM envelope uses the right `sso.key` and handles key rotation gracefully (note: we have no key-rotation path yet; document it as M16 limitation, not a new feature)

---

## 5. Adversarial review log

_To be populated by strongest-model review before Wave A dispatch._

---

## 6. Related work

- ADR-0001 (Go agent over NDJSON UDS) — unchanged; Hydra doesn't touch the agent except where Step 7's wordpress_install command writes plugin config files.
- ADR-0003 (one write path = the API) — preserved; Hydra client creation is inline with the applications handler, not reconciler-driven.
- ADR-0018..0021 (M7 MariaDB) — reuses the instance with a new schema.
- M19 Applications Framework — Step 6 + Step 7 are additive extensions to the existing registry. No existing app descriptor breaks.
- M20 Kratos — Hydra's login flow delegates to Kratos. Decision 5 is the wiring point.
- M7 phpMyAdmin SSO — stays on the nonce pattern; NOT migrated. See "Non-goals" above.
- Future Automation API (follow-up PR) — reuses Hydra's client_credentials grant + introspection. `oauth2.go` middleware + `RequireScope` helper.

---

## 7. Cold-start dispatch notes

Each step below is self-contained enough that a fresh agent with no prior conversation can execute it. The agent MUST:

1. Run `gitnexus_impact` on any symbol it's about to modify (per the project's CLAUDE.md sub-agent mandate).
2. Work on `m16/hydra-identity` in wt-a (or create a sub-branch `m16/hydra-identity-stepN` if parallel work demands).
3. Rebase onto latest `origin/main` before the final report.
4. Report branch name + commit SHAs + `git log main..<branch>` summary + confirmation that tests were re-run post-rebase.

**First dispatch: Step 1 + Step 2 (parallel).** Both are Wave A and can land independently on the same branch.

---

## 8. Vendoring pins (set at Step 1)

- Hydra binary: `v2.4.x` (verify latest stable at dispatch time; pin exact patch version)
- OpenID Connect Generic plugin: `v3.x` (WordPress.org latest stable)
- No other new upstream deps.
