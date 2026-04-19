# M5c — Two-Factor Authentication Runbook

Ops reference for the TOTP + backup-codes feature shipped in `feat-2fa-totp`.
Pairs with the implementation plan in `plans/2fa-totp.md`.

## Who's affected

Every panel account (admin and hosting user). TOTP is opt-in per user —
no forced rollout. Two paths intentionally bypass 2FA:

- `jabali admin login` — CLI break-glass for operators.
- Admin impersonation (`jabali user login <email>` → login URL) — the
  admin doesn't prove the target's 2FA because impersonation is the
  escape valve for locked-out users.

## User-facing flows

### Enable 2FA (from the user's MyProfile page)

1. MyProfile → "Two-factor authentication" card → **Enable 2FA**
2. Scan the QR with any RFC 6238 client: Google Authenticator, 1Password,
   Authy, Bitwarden, Microsoft Authenticator.
3. Enter the 6-digit code → **Verify & enable**
4. Save the 10 backup codes shown. They're shown **once**. Copy each code
   or use the **Download as .txt** button. Check "I've saved my backup
   codes" to close the modal.

Each backup code is 8 digits, single-use. Keep them in a password
manager or a safe offline location.

### Regenerate backup codes

MyProfile → **Regenerate backup codes** (button only visible when 2FA
is already on). Requires a current 6-digit TOTP code. Old codes are
invalidated atomically; 10 new ones are displayed (once).

### Disable 2FA

MyProfile → **Disable 2FA**. Requires **both** current password AND a
current 6-digit code. A stolen session token alone cannot turn 2FA off.

### Logging in with 2FA enabled

Standard email + password first. On success, the UI swaps to a 6-digit
input. Two escape valves inside the challenge form:

- **Use backup code** — swaps input to 8 digits for a one-time recovery
  code.
- **Start over** — returns to the password form.

The pending session JWT lives 5 minutes. If it expires, the UI returns
to the password form with "Your 2FA session expired" and the user
re-enters the password.

## Admin-facing flows

### Break-glass unlock: user lost phone AND all backup codes

```
# Shell access on the panel host required — no API equivalent by design.
sudo jabali admin disable-2fa --email <target@example.com>
```

Output:

```
2FA disabled for <target@example.com> (user <ULID>). They can re-enrol on next login.
```

Steps the command performs, in this order (so a mid-failure still leaves
the user unlocked):

1. `DeleteAllByUserID` on `totp_backup_codes` — wipes their 10 backup rows.
2. `DisableTOTP` on the user — clears `totp_secret_encrypted`,
   `totp_enabled`, `totp_enabled_at`.

After this, the user logs in with password alone; they can re-enrol
from MyProfile whenever they want.

The command is audit-logged: search with

```
journalctl --unit jabali-panel -g 'kind=admin_disable_2fa'
```

### Impersonation without prompting for 2FA

`jabali user login <email-or-id>` mints a short-lived CLI token and
prints a login URL. That URL hits `/auth/cli-login`, which skips the
TOTP gate because the token already carries a specific `Purpose`.
Operators doing support never need the user's authenticator.

## Troubleshooting

### "That code isn't right" when the user swears the code is fresh

Almost always a clock-skew issue on the user's phone or your panel host.
Both ends must agree within ±30 s.

```
# Check the panel host:
timedatectl show

# On the user's phone: enable automatic time zone + auto time.
```

The lib we use (`pquerna/otp`) tolerates ±1 step (30 s) by default; beyond
that it rejects.

### "Your 2FA session expired"

The `twofa_pending_token` JWT is valid for 5 minutes from `/auth/login`.
If the user walked away and came back, they have to re-enter the password.
Not a bug.

### Rate limit on challenge endpoint

`/auth/2fa/challenge` rides the same strict rate limiter as `/auth/login`
— unbounded brute force would burn through the 30-s TOTP window or
the 10 backup codes. A user who hits the limit gets back
`{"error": "rate_limited"}`; they're told to wait a minute.

### Database inspection

```sql
SELECT id, email, totp_enabled, totp_enabled_at
FROM users
WHERE email = '<target>';

SELECT count(*) AS unused_codes
FROM totp_backup_codes
WHERE user_id = '<ULID>' AND used_at IS NULL;
```

`totp_secret_encrypted` is an AES-256-GCM envelope — not readable
without the server's key file. Don't try to decrypt by hand; the
admin CLI is the only sanctioned path to clear it.

## Keys and secrets

The TOTP secret at rest reuses `internal/ssokey.Key`, the same
AES-256-GCM key used for the phpMyAdmin SSO password envelope
(ADR-0022 context). Rotation is a single operation — `jabali sso
rotate-key` — that re-seals every encrypted column; the command is
unchanged by this feature.

There's no separate 2FA key by design. A second key would double the
rotation surface without adding defense (an attacker with filesystem
access + DB has both keys either way).

## Migration and rollback

- Forward: `000041_add_2fa_totp.up.sql` — additive only. Adds 3
  nullable user columns + a new `totp_backup_codes` table with a
  foreign-key cascade. Safe on live DBs.
- Reverse: `000041_add_2fa_totp.down.sql` — drops both the columns
  and the table. Re-enrolment required after any forward migration
  post-rollback (secrets are lost).

Fresh refresh tokens remain valid across the deploy (no
cryptographic dependency on the new migration), so mid-session
rollout doesn't log anyone out.

## Out-of-scope / future work

- WebAuthn / security keys: deferred. TOTP covers 95% of the
  threat model for now.
- SMS or push: no plan to add; SMS is SS7-attackable, push requires
  running an app.
- Per-role enforcement (e.g., "admins must have 2FA"): opt-in for
  everyone in v1. A future migration can add an `is_admin`-scoped
  required flag if needed.
- E2E Playwright coverage: the Go integration test covers the
  handler-to-service wiring end-to-end; a Playwright flow needs a
  seeded 2FA-enrolled user in the test DB and runs in CI only.

## Related docs

- `plans/2fa-totp.md` — original implementation plan (decisions, wave
  structure, schema).
- `docs/BLUEPRINT.md` §M5c — feature summary + changelog row.
