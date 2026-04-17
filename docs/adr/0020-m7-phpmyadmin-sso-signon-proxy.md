# ADR-0020: M7 phpMyAdmin SSO via server-side signon proxy

**Date:** 2026-04-17
**Status:** Accepted
**Deciders:** Shuki

## Context

M7 exposes `/api/v1/sso/phpmyadmin` so users can jump from the panel into phpMyAdmin without re-typing their database user credentials. phpMyAdmin is a separate PHP app, running on its own nginx vhost or path alias, with its own session cookie. We need a handoff that proves identity from the panel to phpMyAdmin without: (a) shipping the user's password over the wire, (b) letting a leaked URL grant long-lived access, (c) expanding the panel's trust boundary into phpMyAdmin's process.

## Decision

**The panel mints a single-use, SHA-256-hashed, 5-minute TTL SSO token, redirects the user to a small `signon.php` script deployed inside phpMyAdmin's config directory, and `signon.php` exchanges the token for a phpMyAdmin login session.**

Flow:
1. User clicks "Open in phpMyAdmin" in the panel UI.
2. Panel handler `POST /api/v1/sso/phpmyadmin` mints a random 256-bit token, stores its SHA-256 digest in a new `phpmyadmin_sso_tokens` table (`user_id`, `db_user_id`, `token_hash`, `expires_at`), and returns `{ redirect_url: "<pma_url>/signon.php?sso_token=<raw>" }`.
3. Panel UI does `window.location = redirect_url`.
4. `signon.php` calls the panel API `POST /api/v1/sso/phpmyadmin/validate { sso_token }` over loopback.
5. Panel validates: token exists (match by hash), not expired, not already consumed. It deletes the row atomically (single-use) and returns `{ db_username, db_password }`.
6. `signon.php` populates phpMyAdmin's `$_SESSION` and redirects to `/phpmyadmin/index.php`.

Tokens never appear in any log in raw form beyond the single redirect URL. The panel stores only the hash. Exchange is over Unix-domain socket (phpMyAdmin runs on the same host), not over network.

## Alternatives Considered

### Direct credential forwarding (POST username+password via hidden form)
- **Pros:** No new tokens, no signon.php.
- **Cons:** Password traverses the browser; leaks in referer headers; passwords are bcrypt-hashed panel-side (see ADR-0021) and would need plaintext storage.
- **Why not:** Needs plaintext password at rest — non-starter.

### JWT in query string, validated by phpMyAdmin inline
- **Pros:** Stateless, no new DB table.
- **Cons:** JWT ends up in browser history, referer headers, nginx access logs; needs a shared secret between panel and phpMyAdmin; replayable for its full TTL even after use.
- **Why not:** Leakage surface is larger than single-use tokens.

### `auth_type = config` with a shared static credential
- **Pros:** Trivial.
- **Cons:** All panel users share one phpMyAdmin identity; no per-user audit trail; one leak compromises every database.
- **Why not:** Multi-tenant hosting panels cannot share DB credentials across users.

### Browser-stored token (localStorage) consumed by phpMyAdmin JS
- **Pros:** No query-string leakage.
- **Cons:** Cross-origin localStorage is blocked by default; XSS on phpMyAdmin reads it; phpMyAdmin is PHP, not a SPA — doesn't fit the model.
- **Why not:** Structurally wrong for phpMyAdmin.

## Consequences

### Positive
- Tokens are single-use and short-lived — even a leak grants at most one session within 5 minutes.
- Panel never stores plaintext DB passwords; they're minted fresh per SSO request from stored panel state (via `db_user.rotate_password` if needed, though normal flow reveals password at create-time only).
- Every SSO is auditable in `phpmyadmin_sso_tokens` history (if we keep soft-deleted rows; Phase-4 decides).

### Negative
- Requires deploying and maintaining `signon.php` inside phpMyAdmin's config directory — new surface area to update when phpMyAdmin upgrades.
- Token validation requires a round-trip from phpMyAdmin back to the panel API — adds latency to the first redirect.
- Ties the panel to phpMyAdmin 5.1+ (minimum version supporting the `signon.php` hook path we use).

### Risks
- **Token replay on clock skew.** Mitigation: delete row in the same transaction as the lookup (`DELETE ... RETURNING` or equivalent), so second use reads nothing.
- **signon.php bug exposes the panel's validate endpoint.** Mitigation: validate endpoint authenticates via Unix socket only; not reachable from external network even if the PHP file is misconfigured.
- **phpMyAdmin upgrade breaks signon flow.** Mitigation: pin minor version in install.sh; add smoke test that performs one SSO round-trip.
