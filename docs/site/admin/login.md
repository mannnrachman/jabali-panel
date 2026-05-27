# Admin Login

`/jabali-admin` redirects to the Kratos login flow at `/auth/login`.

## Flow

1. Email or username + password.
2. If TOTP 2FA is enrolled: redirect to [Two-Factor Challenge](./two-factor-challenge.md).
3. Session cookie set; redirect to `/jabali-admin/dashboard`.

Sessions are managed by **Kratos** (M20). The panel itself does not store passwords or session tokens.

## First admin

Created by the installer. The admin one-time-login URL is printed at the end of `bash install.sh`. If you missed it:

```bash
jabali admin one-time-login
```

…prints a fresh URL valid for 10 minutes. Land on it → set a password → optionally enrol 2FA.

## Locked out

If the only admin lost their password or 2FA:

```bash
jabali user password <admin-email>          # generate a new password
jabali user 2fa-reset <admin-email>         # strip TOTP + recovery codes
```

Both bypass HTTP auth (direct DB + Kratos) so they work even when the panel UI is unreachable.

## Bruteforce protection

CrowdSec watches `kratos.public` and `nginx.access` for failed-auth patterns. Repeated failures from one IP earn a 4-hour BAN decision. See [CrowdSec Decisions](./crowdsec-decisions.md).

## No OIDC

The panel does **not** act as an OIDC provider (M16 rolled back; see [removed-features](../removed-features.md)). Login is local-account-only via Kratos.
