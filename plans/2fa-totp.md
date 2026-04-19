# 2FA (TOTP + Backup Codes) — Plan

**Goal:** Every account that logs in with password also verifies a 6-digit TOTP
code after enrolment. Backup codes recover from a lost phone. Admin
break-glass CLI login is unaffected (by design — it's the escape hatch).

## Scope

- TOTP only (RFC 6238) via `github.com/pquerna/otp`. Works with Google
  Authenticator, 1Password, Authy, Bitwarden, etc. No WebAuthn (phase 2).
- 10 backup codes, 8 digits each, shown once at enrolment, hashed in DB, single-use.
- Applies to: all authenticated users (admins + regular). NOT applied to:
  - Break-glass `jabali-panel admin login` CLI flow (stays passwordless).
  - Admin impersonation (existing behavior: impersonator doesn't prove the
    target's 2FA; this is the escape valve).

## Decisions

1. **Library:** `github.com/pquerna/otp/totp` — stdlib of Go TOTP.
2. **Issuer / label:** TOTP URI shows `Jabali Panel (<email>)` so multiple
   accounts on one authenticator app are distinguishable.
3. **Secret encryption at rest:** reuse existing `ssokey.Key` (AES-256-GCM)
   for encryption. Rationale: it's already key-rotation-aware, already
   bound to the server's secret material, adding a second key buys no
   extra security and doubles the rotation surface.
4. **2FA-pending token:** JWT with `purpose="2fa_pending"`, 5-min TTL, no
   other claims — cannot be used to call any resource, only
   `POST /auth/2fa/challenge`.
5. **Backup codes:** bcrypt-hashed (cost 12), single-use (marked
   `used_at` when redeemed). Shown once in the enrolment response.
   Can re-generate via "Regenerate backup codes" action (requires current
   TOTP code).

## Schema (migration 000040)

```sql
ALTER TABLE users
  ADD COLUMN totp_secret_encrypted VARBINARY(256) NULL,
  ADD COLUMN totp_enabled BOOLEAN NOT NULL DEFAULT FALSE,
  ADD COLUMN totp_enabled_at DATETIME(6) NULL;

CREATE TABLE totp_backup_codes (
  id         CHAR(26)    PRIMARY KEY,
  user_id    CHAR(26)    NOT NULL,
  code_hash  VARCHAR(72) NOT NULL,
  used_at    DATETIME(6) NULL,
  created_at DATETIME(6) NOT NULL,
  INDEX idx_totp_backup_user (user_id),
  CONSTRAINT fk_totp_backup_user
    FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE
);
```

## Endpoints

| Method | Path | Auth | Purpose |
|---|---|---|---|
| POST | `/api/v1/auth/2fa/enroll`    | access token | generate secret, return `{secret, otpauth_url}`. Does NOT enable — user must verify first |
| POST | `/api/v1/auth/2fa/verify`    | access token | `{code}` — on success: set `totp_enabled=true`, generate + return 10 backup codes |
| POST | `/api/v1/auth/2fa/disable`   | access token | `{password, code}` — wipes secret + backup codes |
| POST | `/api/v1/auth/2fa/regen-backup` | access token | `{code}` — invalidates old codes, returns 10 new |
| POST | `/api/v1/auth/2fa/challenge` | 2fa-pending token | `{code}` OR `{backup_code}` — returns full access token |

## Login flow changes

```
POST /auth/login (existing)
  Body: {email, password}
  → 200 {access_token, refresh_token}      — if totp_enabled=false (unchanged)
  → 200 {twofa_pending_token, twofa_required: true} — if totp_enabled=true

POST /auth/2fa/challenge (new)
  Auth: Bearer <twofa_pending_token>
  Body: {code} OR {backup_code}
  → 200 {access_token, refresh_token}
  → 401 if invalid after N retries (rate-limit in-memory; no DB lock-out)
```

## UI changes

**MyProfile page** — add "Two-Factor Authentication" card:
- If `totp_enabled=false`: button "Enable 2FA" → Modal
  1. Fetch `/auth/2fa/enroll` → display QR + manual secret
  2. User enters 6-digit code → POST `/auth/2fa/verify`
  3. Success: show 10 backup codes (download + copy), require explicit
     "I've saved these" checkbox to close modal
- If `totp_enabled=true`: show enabled status + 2 actions:
  - "Regenerate backup codes" (requires current code)
  - "Disable 2FA" (requires password + current code)

**Login page** — after password success, if `twofa_required`:
- Render a small "Enter 6-digit code from your authenticator app" form
- Link: "Use a backup code instead" → swaps to 8-digit input
- On success → redirect to original destination

## Risk / rollback

- Admin-only unlock path: `jabali admin disable-2fa --email <target>` CLI
  command (new) lets break-glass admin clear a user's 2FA if they lose
  phone + backup codes.
- Migration is additive + nullable — reversible via 000040 down.
- No cryptographic dependency on existing tokens (fresh refresh tokens remain
  valid across deploy), so rollout is safe mid-session.

## Waves

- **A.** Migration + model + repo (30 min)
- **B.** TOTP service + 4 endpoints + tests (1-1.5 h)
- **C.** Login-flow integration + challenge endpoint + tests (45 min)
- **D.** UI: MyProfile card + Login challenge step (1 h)
- **E.** Admin CLI disable-2fa command + docs (30 min)

Total: ~4 hours realistic.
