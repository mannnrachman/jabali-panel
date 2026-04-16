# 0015 — Admin impersonation with `impersonated_by` JWT claim

## Status
Accepted — 2026-04-17

## Context
Admins need to debug user-specific problems by operating the panel from the user's perspective (same session-scoped data, same layout, same RoleGate redirects). Without a first-class impersonation primitive, support turns into "what do you see on your screen?" screenshots — slow, error-prone, and unaudited.

Key constraints:
- Must prevent admin-to-admin impersonation (no lateral privilege pivot).
- Must preserve audit attribution — actions taken during impersonation should trace back to the *real* admin, not the target user.
- Must not leak secrets (no password sharing, no shared session cookies).

## Decision
`POST /admin/users/:id/impersonate` (admin-only) issues a fresh access token + refresh cookie for the target user, with an `impersonated_by: <admin_id>` claim embedded in the JWT. The same claim is persisted on the `refresh_tokens` row so that rotation preserves it through the full session lifetime. `/auth/me` exposes the claim so the UI can render a persistent banner and downstream audit handlers can record the real actor.

Exit from impersonation = logout + re-login. There is no server-side "pop back to admin" state.

## Consequences

### Positive
- Ongoing actions during impersonation carry `impersonated_by` in claims — any audit handler can attribute to the real admin without extra plumbing.
- Simple exit model: no session-stacking state machine that can desync during refresh rotation.
- Mirrors the industry-standard pattern (Google Workspace, GitHub org impersonation).

### Negative
- Admin must re-authenticate after exiting impersonation.
- One extra column on `refresh_tokens` (`impersonated_by CHAR(26) NULL`).

### Neutral
- Admin-to-admin impersonation is forbidden at the API layer (`403 cannot_impersonate_admin`); self-impersonation at the API layer (`400 cannot_impersonate_self`).

## Alternatives considered

- **Session stacking** (stash admin session server-side, "pop" on exit): rejected — complex server state, hard to reason about during refresh-token rotation, extra failure modes if the admin's refresh expires mid-impersonation.
- **Shared view mode** (admin sees user's data without switching session): rejected — doesn't reproduce real user flows; CSRF tokens, cookies, and browser-side caches all behave differently than a real user's browser.
- **Password sharing** (admin logs in as user manually): rejected — no audit trail, requires storing or transmitting plaintext credentials.

## References
- `panel-api/internal/api/users.go` — `impersonate` handler
- `panel-api/internal/auth/service.go` — `IssueImpersonation`
- `panel-api/internal/auth/jwt.go` — `AccessClaims.ImpersonatedBy`
- `panel-api/internal/db/migrations/000016_add_impersonated_by_to_refresh_tokens.up.sql`
- `panel-ui/src/shells/admin/users/UserImpersonateAction.tsx`
- `panel-ui/src/components/ImpersonationBanner.tsx`
