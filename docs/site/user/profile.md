# Profile

`/jabali-panel/profile`. The tenant's account settings.

## Sections

### Identity

- Display name
- Email address (used for login and password recovery)
- Locale (UI language)
- Time zone

Email changes propagate to Kratos; the next login uses the new email.

### Security

- **Change password** — current password required.
- **Two-Factor Authentication** — enroll TOTP. Scan a QR code, enter one valid 6-digit code to confirm. Eight recovery codes are displayed once; save them. See [Two-Factor Challenge](./two-factor-challenge.md).
- **Active sessions** — list of current sessions with device, IP, last activity. **Revoke** any session except the current one.

### Notifications

Opt in or out of notifications about the account:

- Cron job failures
- Backup succeeded / failed (if the package allows tenant-visible backups)
- Mail quarantine events affecting your mailboxes
- Disk usage approaching quota
- Cert renewal failures on your domains

Each row picks the channels: in-app bell, email, Web Push (browser-subscribed). Slack / Telegram / ntfy are admin-only channels.

### Backups

The **Backup card** on the profile page lets the tenant download a snapshot of their own account on demand, subject to the package allowing tenant-initiated backups. See [Backup Download](./backup-download.md).

### Usage

The **Usage card** summarises disk, bandwidth, mailboxes, databases, and domain counts against the package limits. Identical data to the Dashboard cards but in one place for quick reference and screenshot purposes.

### Danger zone

- **Delete account** — only enabled when the admin has set the per-tenant flag "self-service deletion allowed". Default: disabled. When enabled, deletion requires typing the username and an emailed confirmation token.

## Logout

The header avatar dropdown carries Sign Out, which clears the Kratos session and returns to the login page.
