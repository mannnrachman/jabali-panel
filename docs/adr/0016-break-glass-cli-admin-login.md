# 0016 — Break-glass admin login via CLI with `purpose=cli_login` claim

## Status
Accepted — 2026-04-17

## Context
If the admin password is lost, email bootstrap fails, or an install ends up in a half-configured state, the panel becomes unrecoverable through the UI. There's no "forgot password" flow — email infrastructure isn't guaranteed on a fresh install, and SMTP configuration itself often requires panel access. The operator needs an emergency path back in that doesn't depend on another auth factor (email, TOTP, SMS) but also can't be trivially abused by someone who shouldn't have panel access.

Implicit trust boundary: whoever can run the binary as the `jabali` service user already has the privileges to read the JWT secret directly and forge any token they want. So the CLI is a *convenience*, not an escalation — it just saves the operator from writing a JWT-forging one-liner during a recovery.

## Decision
`jabali-panel admin login` is a Cobra subcommand that mints a 15-minute one-shot JWT carrying a `purpose="cli_login"` claim and prints a login URL with the token in the query string. A new endpoint `POST /auth/cli-login` redeems the token for a normal session (access_token + refresh cookie). Regular auth middleware rejects any token whose `purpose` is non-empty — CLI tokens are NOT valid for protected API routes. The frontend login page auto-detects `?cli_token=` and redeems it on mount, then strips the token from the URL via `history.replaceState`.

Both issuance (CLI side) and redemption (HTTP side) are audit-logged.

## Consequences

### Positive
- Recovery works without passwords, without re-bootstrapping a user, without direct DB surgery.
- Distinct `purpose` claim makes these tokens unusable for anything except the redemption endpoint.
- Audit logs separate issuance from redemption — operator can see if a token was minted but never used.
- 15-minute TTL and one-URL-per-invocation bound the exposure window.

### Negative
- Requires shell access to the server — no remote-only recovery for cloud-hosted installs where the operator lacks SSH.
- A stolen CLI token is a valid admin-session-seed until it expires (mitigation: 15min TTL + audit log + operator can invalidate by rotating the JWT secret).

### Neutral
- CLI refuses to run if there are zero admins or if there are multiple admins without an explicit `--email` flag. Prevents silent "which one?" ambiguity.

## Alternatives considered

- **Password reset via email**: rejected — email/SMTP isn't reliably configured on a fresh install (and configuring it often needs panel access, creating a chicken-and-egg problem).
- **`--reset-password <email>` CLI flag**: rejected — forces the operator to pick a new password inline instead of just getting into the panel to investigate. Also means the password lands in shell history or the recovery script.
- **Direct DB row injection** (insert a known-hash password): rejected — operator-hostile, fragile across schema changes, no audit trail.
- **Re-use the regular access token signing path** (no `purpose` claim): rejected — any leaked CLI token would be a fully-valid access token for protected routes until it expired.

## References
- `panel-api/cmd/server/admin_login.go` — CLI subcommand
- `panel-api/internal/api/auth_cli_login.go` — redemption endpoint
- `panel-api/internal/auth/jwt.go` — `Purpose` claim
- `panel-api/internal/auth/service.go` — `RedeemCLIToken`
- `panel-api/internal/middleware/jwt.go` — rejects tokens with non-empty `Purpose`
- `panel-ui/src/pages/Login.tsx` — auto-redeems `?cli_token=`
