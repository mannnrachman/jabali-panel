# 2FA TOTP — Operator Runbook

Reference: ADR-0090. Plan: `plans/2fa-totp.md`.

## What 2FA looks like to a user

1. Sign in with email + password (aal1).
2. Open `My Profile`. Click "Manage account security" — opens the
   Kratos settings flow inline.
3. "Two-factor authentication (TOTP)" card shows a QR code + the
   base32 secret. Scan with Google Authenticator / 1Password /
   Authy / Bitwarden. Enter the 6-digit code to confirm. Click
   "Save TOTP".
4. "Backup codes" card shows "Generate backup codes". After
   clicking, recovery codes appear ONCE — copy and save. Re-clicking
   generates a fresh set; old codes stop working.
5. Sign out and back in. After password, the page asks for a
   6-digit code (or a recovery code via "Use a recovery code
   instead").

## Disabling

User: My Profile → "Two-factor authentication (TOTP)" — Kratos
surfaces a disable action on the same form when enrolment is active.

## Locked-out user — admin reset

When a user has lost their authenticator AND used all recovery
codes:

1. Sign in as an admin.
2. Navigate to `/jabali-admin/users`.
3. Find the user's row. Click **Reset 2FA**.
4. Confirm in the modal.
5. Tell the user: their password still works. They should sign in,
   open My Profile, re-enrol TOTP, save fresh recovery codes.

## When EVERY admin is locked out

Use the break-glass CLI on the host:

```bash
ssh root@<panel-host>
jabali user 2fa-reset <admin-email>
```

Strips totp + lookup_secret credentials from the user's Kratos
identity via the admin API (no panel session required — runs
directly against Kratos's admin socket). Password unchanged.
Admin signs in normally afterwards and can re-enrol from
`/jabali-admin/profile`.

## Inspecting state

Kratos identity creds:
```bash
curl --unix-socket /run/jabali-kratos/admin.sock \
  "http://localhost/admin/identities/<identity-id>?include_credential=totp,lookup_secret" \
  | jq '.credentials'
```

Panel users → Kratos identity mapping:
```sql
SELECT id, email, kratos_identity_id FROM users WHERE email='...';
```

## Leaked TOTP secret

1. Admin runs **Reset 2FA** for the user.
2. Tell user to re-enrol with a fresh authenticator entry.
3. The old secret is gone from Kratos.

## Disabling 2FA globally (escape hatch)

Should never be needed. If you must:

1. Edit `/etc/jabali-panel/kratos.yml`:
   `session.whoami.required_aal: aal1`.
2. `systemctl restart jabali-kratos`.
3. Investigate.
4. Restore + run `jabali update` to re-render from
   `install/kratos.yml.tmpl` (the hand-edit will revert — see
   memory `feedback_install_sh_is_truth`).

## Known limitations

- No WebAuthn / passkeys. Future milestone.
- No "all admins must enable" enforcement. Kratos v26 has no
  per-trait AAL policy.
- No per-route AAL exceptions. Whole panel is
  aal2-when-enrolled.
