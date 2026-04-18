# M11 FileBrowser — proxy-auth session fix

**Status**: Design doc. Not yet scheduled. Discovered 2026-04-18 while shipping M8.
**Context**: After redeploying M8 + the FB baseurl pin (commits `5eeacf9`, `0515b51`, `bb185c0`, `9aec15f`, `4b3a0a0`, `aadc84a`), the first request to `/files/?token=X` returns 200 and the SPA shell loads, but the SPA then POSTs `/files/api/login` and hits a 500. This is not a deploy bug — it's an M11 architectural gap.

---

## Root cause

`auth.method=proxy` in filebrowser is **stateless**. It reads `X-Forwarded-User` on every request; there is no cookie or JWT fallback inside filebrowser when the header is absent. Our nginx config:

```nginx
# Bootstrap — sets the header from the validated SSO token
location = /files/ {
    set $fb_sso_token $arg_token;
    auth_request /internal/fb-validate;
    auth_request_set $fb_user $upstream_http_x_auth_user;
    proxy_set_header X-Forwarded-User $fb_user;
    proxy_pass http://unix:/run/jabali-filebrowser/fb.sock:/;
}

# Catch-all — strips the header, doesn't re-set it
location /files/ {
    proxy_set_header X-Forwarded-User "";   # <-- here
    proxy_pass http://unix:/run/jabali-filebrowser/fb.sock:/;
}
```

The catch-all was written assuming filebrowser keeps a cookie session. It does not. Every subsequent request (the SPA's XHRs to `/files/api/*`) hits headerless filebrowser → 500 / 401 / login redirect depending on the endpoint.

## Three fix paths

### Option A: signed cookie via nginx njs / lua

**Idea**: first request's `fb-validate` subrequest, after validating the token, returns the username. nginx takes that + a shared HMAC secret and issues a signed cookie `jabali_fb_session=<user>.<sig>.<ts>`. Every subsequent `/files/*` request validates the cookie via njs, extracts the username, and sets `X-Forwarded-User` before proxying.

**Pros**
- No round-trip to panel API on every request.
- Close to the intended M11 stateless design.
- Uses the same `auth.method=proxy` we already deployed.

**Cons**
- Requires nginx-njs (or nginx-lua + lua-resty-hmac). Debian 12 ships `nginx-extras` with js_* directives — check at install time.
- HMAC secret lifecycle (rotation, per-install provisioning) needs design — pick a store (sso.key is already rotated by the panel; reuse?).
- Cookie TTL semantics + revocation story (what happens when we want to kill a user's session?).

**Complexity**: medium. ~4-6 hours including install.sh wiring, rotation, tests.

### Option B: session-lived SSO tokens + header on every request

**Idea**: drop "one-time use". Tokens live for a session (say 2h), panel keeps them in the `filebrowser_sso_tokens` table with `expires_at` + `revoked_at` columns. Every `/files/*` request runs `auth_request /internal/fb-validate`, which checks the token is still valid (DB lookup). nginx sets `X-Forwarded-User` from the response on every request.

**Pros**
- Simplest code change. No new nginx module. No HMAC. No client-side state beyond the URL token.
- Revocation is trivial (flip `revoked_at`, next request 401s).
- Rate limiting / abuse mitigation is straightforward (middleware on the validate endpoint).

**Cons**
- DB hit on every `/files/*` subrequest. With the SPA loading ~20 resources on first paint + heartbeats, one user browsing files = 20-100 DB reads/min. At 50 concurrent users, ~80 reads/sec baseline. MariaDB handles that trivially but it's not free.
- Token has to travel in every request URL (`?token=X` on refreshes / deep links breaks unless we cache the token in a cookie ourselves). Alternatively, set a cookie on first-request and have the catch-all read it.
- URL-embedded tokens show up in browser history and nginx access logs. Mitigation: log filtering + Referrer-Policy.

**Complexity**: low. ~2-3 hours. **Recommended as the pragmatic first fix.**

### Option C: switch filebrowser to `json` auth, panel issues JWTs

**Idea**: filebrowser reverts to its native `auth.method=json`. Every panel user gets a filebrowser account provisioned (the reconciler already does this, modulo the SQLite-lock issue we saw today). Panel SSO endpoint issues a filebrowser JWT cookie directly, using filebrowser's JWT secret. Browser navigates `/files/` with the cookie, filebrowser trusts it, done.

**Pros**
- Closest to filebrowser's intended design.
- No header manipulation in nginx — nginx just reverse-proxies.
- Real session semantics (TTL, refresh, etc.) handled by filebrowser.

**Cons**
- Need to solve the SQLite-lock issue for user provisioning. Either stop/start filebrowser on every user-create, or use filebrowser's admin REST API from the reconciler.
- JWT secret sharing between panel and filebrowser (bootstrap, rotation).
- Most code churn: panel SSO endpoint rewrites, reconciler changes, filebrowser config changes.

**Complexity**: high. ~8-12 hours. Only worth it if B proves inadequate.

---

## Recommendation

Ship B. Keep A/C in the backlog. Re-evaluate if B's DB-hit cost becomes visible in Grafana.

## Implementation checklist for Option B

- [ ] Migration: add `expires_at TIMESTAMP NOT NULL`, `revoked_at TIMESTAMP NULL`, index on `(token_hash, expires_at)` to `filebrowser_sso_tokens`.
- [ ] Remove one-time-use consumption in `ValidateAndConsumeFileBrowserToken` — validate only, don't mark used.
- [ ] Add `RevokeFileBrowserToken(tokenID)` for admin revocation UI (nice-to-have).
- [ ] `/sso/filebrowser/validate` handler: no-op on success (just return 200 + X-Auth-User header); leave the row untouched.
- [ ] Set a short cookie on first-request proxy (`Set-Cookie: jabali_fb_session=<token>; Path=/files; HttpOnly; Secure; SameSite=Lax; Max-Age=7200`). nginx can do this via `add_header` in the bootstrap location.
- [ ] Catch-all nginx location reads `$cookie_jabali_fb_session`, runs `auth_request /internal/fb-validate-cookie` (new internal location that looks up by cookie value instead of query token), injects `X-Forwarded-User` from the response.
- [ ] Install.sh + update.go: add `filebrowser config set --auth.method=proxy --auth.header=X-Forwarded-User -b /files -d <dbpath>` between `stop` and `start` so fresh boxes don't regress to JSON auth.
- [ ] Update `tests/e2e/filebrowser.spec.ts` — it currently probably passes on a shape that won't survive this refactor.
- [ ] Update `plans/m11-filebrowser-runbook.md` troubleshooting tree.

## Out of scope for this plan

- Multi-session (same user, multiple browser tabs across different IPs). Tokens will work for that trivially — different cookies.
- MFA. Filebrowser has no MFA primitive; that lives in the panel login, which is already MFA-capable.
- Admin-level filebrowser operations via SSO. Admins log into filebrowser directly with the bootstrap admin credentials; we don't SSO admin.

## What NOT to do

- Don't try to fix this by adding `-b /files` and hoping. Already pushed (`bb185c0`), did not solve it.
- Don't add `filebrowser config set` in every install step "defensively". It needs the daemon stopped. Run it exactly once per install/update inside a stop-set-start sequence.
- Don't delete the SQLite DB to force a re-read of config.json. That wipes admin credentials and per-user provisioning the reconciler already did.
