# Two-Factor Challenge (User)

After entering your password successfully, if your account has TOTP two-factor authentication enrolled, you are prompted for a six-digit code.

## Entering the code

Open your authenticator app (Aegis, Authy, 1Password, Google Authenticator, etc.), find the code for the panel, and enter it. The code rotates every 30 seconds; if it expires while you are typing, wait for the next one.

## Using a recovery code

If you do not have access to your authenticator app, click **Use a recovery code** and paste one of the eight codes you saved at enrollment time. Each code works exactly once.

After using a recovery code, regenerate the set under Profile → Security → Two-Factor → Regenerate Recovery Codes. The remaining old codes continue to work until regeneration.

## Lost both the authenticator and the recovery codes

Contact your administrator. The operator command `jabali user 2fa-reset <email>` strips TOTP and recovery codes from your account; you can then log in with only your password and re-enroll TOTP from the Profile page.

## Why no SMS or email codes

SMS-based two-factor is vulnerable to SIM-swap attacks and incurs delivery costs and reliability issues. Email-based codes assume your email account is at least as secure as your panel account, which is often not the case. TOTP is broadly available and resistant to both attack types.

## Hardware keys

WebAuthn / FIDO2 hardware keys are not yet supported. TOTP is the only second factor currently available.
