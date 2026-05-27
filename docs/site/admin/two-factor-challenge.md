# Two-Factor Challenge (Admin)

After password auth, if TOTP is enrolled, Kratos asks for the 6-digit code.

## Enrolling TOTP

Profile → Security → Two-Factor → Enable. Scan the QR with any TOTP app (Aegis, Authy, 1Password, Google Authenticator). Confirm with one valid code. The panel then displays **8 recovery codes** — store somewhere safe; each works once.

## Recovery codes

If you lose access to the TOTP secret:

1. On the challenge page, click **Use a recovery code**.
2. Paste one of the codes.
3. You're in; that code is now consumed.
4. Profile → Security → Two-Factor → Regenerate recovery codes (you should — the others are still valid).

## CLI escape hatch

If you have neither the TOTP secret nor recovery codes:

```bash
jabali user 2fa-reset <email|username|user-id>
```

(Requires root on the panel host. Direct DB + Kratos; bypasses HTTP auth.)

## WebAuthn / hardware keys

Not yet shipped. TOTP is the only 2FA method currently supported.
