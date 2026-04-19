# M20 Kratos runbook

Operational reference for the Kratos identity integration shipped across M20
Waves A–E. This stub covers the Wave B identity-migration tool; Wave D + E
will flesh out the cutover and day-2 sections.

## Migrate existing users (Wave B, Step 4)

The panel ships a one-shot command, `jabali kratos-migrate`, that backfills
Kratos identities for every row in `users` whose `kratos_identity_id` is
`NULL`. Passwords survive the move via bcrypt passthrough — no user has to
re-enrol.

### Prerequisites

1. Kratos is running and reachable at `auth.kratos.public_url` +
   `auth.kratos.admin_url` (defaults: `127.0.0.1:4433` public,
   `127.0.0.1:4434` admin — same-host loopback). Verify:
   ```sh
   curl -sf http://127.0.0.1:4433/health/ready && echo OK
   curl -sf http://127.0.0.1:4434/admin/health/ready && echo OK
   ```
2. The 000047 migration has been applied. Verify:
   ```sh
   mariadb -e "DESCRIBE users" jabali_panel | grep kratos_identity_id
   ```
3. `auth.provider` may be left at `legacy` during the backfill — identities
   are created ahead of cutover. Step 9 flips the flag.

### Dry run (always do this first)

```sh
sudo -u jabali /opt/jabali-panel/jabali kratos-migrate --dry-run
```

The dry-run still exercises the bcrypt passthrough canary (creates a
disposable identity, logs in with a known plaintext via the API-mode flow,
deletes it). If that fails, fix Kratos config (`hashers.bcrypt.enabled`) and
re-run — the real batch refuses to start without a green canary.

The dry-run log lines to expect:
- `running bcrypt passthrough canary` → `canary passed`
- `scanned existing kratos identities` with a count
- `fetched unmigrated users` with a count
- Per-row `would create kratos identity` or `would link existing kratos identity`
- Final `kratos-migrate complete` summary

### Real run

```sh
sudo -u jabali /opt/jabali-panel/jabali kratos-migrate
```

Exit code:
- `0` → every user migrated or already linked.
- non-zero → at least one row failed; the log above the summary names the
  affected `user_id` + `email`. Re-running the command retries only the
  still-NULL rows (idempotent — already-linked rows are skipped by the
  email-to-identity map pre-scan).

### Flags

| Flag | Default | Purpose |
|---|---|---|
| `--dry-run` | off | Plan only. Canary still runs. |
| `--batch-size` | 50 | Rows per logged-progress tick. |
| `--skip-canary` | off | Dangerous — skip bcrypt-passthrough verification. Only use when the canary has already been manually reproduced against the target Kratos. |
| `--totp-only` | off | Emit a CSV report of TOTP-enabled users for pre-cutover notification. Read-only. See "TOTP re-enrollment" below. |
| `--totp-output` | `""` | Path to write the `--totp-only` CSV. Empty or `-` writes to stdout. |

### Common failures

- **`canary failed: create identity (kratos rejected the bcrypt hash format ...)`** — `hashers.bcrypt.enabled` is false in `/etc/jabali-panel/kratos.yml`, or `hashers.argon2` is listed first. Wave A's kratos.yml.tmpl pins bcrypt; check the rendered file.
- **`canary failed: login failed (kratos stored the hash but could not verify it ...)`** — Kratos accepted the hash shape but can't verify it at login time. Usually a `hashers.bcrypt.cost` mismatch or a wrong `credentials.password.identifier_similarity_check` blocking the canary's email+password combination. Log snippet from `journalctl -u jabali-kratos` will show the exact rejection.
- **`auth.kratos.public_url is empty`** — you're running the migrate tool with `auth.provider = "kratos"` but no Kratos URLs set. Either set both URLs in `/etc/jabali/config.toml` + `/etc/jabali/panel.env`, or run step 4 BEFORE flipping the flag.

### Safety — what this tool will NOT do

- **Never truncates the panel `users` table** — `kratos_identity_id` is permanent; the legacy column set stays in place for the 30-day rollback window documented in the M20 blueprint §1 step 9.
- **Never overwrites a non-NULL `kratos_identity_id`** — the `UPDATE … WHERE kratos_identity_id IS NULL` guard is by design. If you need to re-link a user to a different identity, do it manually via `kratos identities delete <id>` + column `UPDATE users SET kratos_identity_id = NULL`.
- **Never deletes a Kratos identity that wasn't just-created by this run** — cleanup paths only call `DeleteIdentity` on IDs the tool itself minted seconds earlier.

## TOTP re-enrollment (Wave D, Step 8)

**Plan deviation — Kratos cannot import TOTP credentials.**

The original blueprint §1 step 8 assumed we could `PATCH
/admin/identities/{id}/credentials` with our TOTP secrets and backup codes.
That premise is broken on Kratos 1.3.1:

1. The Kratos CLI explicitly documents that "credential import is not yet
   supported" for anything beyond `password` and `oidc`
   (kratos-identities-import docs). TOTP and `lookup_secret` have no import
   path in the admin API.
2. Kratos's `lookup_secret` credential stores `recovery_codes[].code` in
   plaintext. Our `totp_backup_codes.code_hash` is bcrypt — one-way, no way
   to recover the plaintext that Kratos needs.

So at cutover, every 2FA user must re-enroll via Kratos's self-service
Security → Authenticator flow. The new `jabali kratos-migrate --totp-only`
produces an operator notification list instead of touching Kratos.

### Generate the notification list (run this pre-cutover)

```sh
sudo -u jabali /opt/jabali-panel/jabali kratos-migrate \
    --totp-only --totp-output /tmp/totp-reenroll.csv
```

The CSV columns:

| Column | Meaning |
|---|---|
| `email` | Affected user's login email |
| `username` | Panel username (may be empty for old accounts) |
| `panel_user_id` | ULID of the row in the panel `users` table |
| `kratos_identity_id` | UUID of the matching Kratos identity — empty if password-migration hasn't run yet |
| `totp_enabled_at` | RFC3339 timestamp of original 2FA enrollment |
| `unused_backup_codes` | Count of unredeemed backup codes at report time |
| `needs_reenrollment` | Always `yes` — this column exists so the CSV is grep-ready for future variations |

If `kratos_identity_id` is empty for any row, run the password migration
first (`jabali kratos-migrate` without `--totp-only`). The report still
works in isolation — the unlinked rows will fall under the warn-level log
line the tool emits.

### What to tell users

Before flipping `auth.provider = "kratos"` (Wave E, Step 9), send every
user in the CSV a message like:

> After the auth upgrade on \<date\>, your two-factor authentication will
> reset. Log in with your existing email + password, then visit
> **Security → Authenticator** to re-enroll your TOTP app and generate a
> new set of backup codes. Your existing authenticator app entry and
> backup codes will no longer work.

Backup codes generated on the new system are stored in Kratos, not in the
panel's `totp_backup_codes` table — the panel table becomes append-only
legacy once the feature flag flips.

### Day-of-cutover checks

After step 9 flips the flag:

```sh
# Which identities have a TOTP credential registered in Kratos?
kratos identities list --format json \
  | jq -r '.[] | select(.credentials.totp) | .traits.email'
```

Compare the output to the `email` column of the CSV; the delta is the
"hasn't re-enrolled yet" set. Follow up personally or via a second mail.

## Cutover (Wave E, Step 9)

### What flipping the flag does

`auth.provider = "kratos"` makes panel-api's `/api/v1/*` routes validate an
`ory_kratos_session` cookie instead of a panel-minted JWT. The Go runtime
default is now `"kratos"` (config.go) and config.example.toml ships with it
pre-set; both mean **any fresh install lands on Kratos**. Any legacy
`jabali_refresh` cookie the browser still holds returns 401 — this is
expected UX, not a regression, and the SPA's unauthenticated-redirect
handles it cleanly by bouncing to /login.

### Flip procedure

1. Ensure `jabali-kratos.service` is running and ready:
   ```sh
   systemctl is-active jabali-kratos && \
     curl -sf http://127.0.0.1:4433/health/ready && \
     curl -sf http://127.0.0.1:4434/admin/health/ready
   ```
2. Run `jabali kratos-migrate --dry-run` and verify all rows either would
   link or would create. Fix any failures BEFORE step 3.
3. Run `jabali kratos-migrate` (omit `--dry-run`). Exit 0 = every user has
   a Kratos identity. Non-zero = re-run — it's idempotent.
4. Run `jabali kratos-migrate --totp-only --totp-output /tmp/totp.csv` and
   notify every affected user — see "TOTP re-enrollment" above.
5. `/etc/jabali/config.toml`: ensure `[auth] provider = "kratos"`.
   Fresh installs have it already; in-place upgrades need the edit.
6. `systemctl restart jabali-panel`. Tail `journalctl -fu jabali-panel`
   during the first 60 seconds — the Kratos-aware `BootstrapAdmin` logs
   `kratos_identity_id=...` when it links the first admin.
7. Smoke-test: open /login in an incognito tab, log in, verify you land
   on /jabali-admin (or /jabali-panel for a user), `document.cookie`
   shows `ory_kratos_session` but no `jabali_access_token` /
   `jabali_refresh`.

### Rollback (30-second path)

```sh
# Edit the single line in /etc/jabali/config.toml:
sed -i 's/^provider = "kratos"/provider = "legacy"/' /etc/jabali/config.toml
systemctl restart jabali-panel
```

Rolling back keeps the Kratos database intact — identities stay, `users.
kratos_identity_id` stays. A future re-cutover is a single-line flip away.
**Do NOT** drop `jabali_kratos` or clear `kratos_identity_id` unless you
are rebuilding from scratch; re-running the migration against a
half-cleaned state produces duplicate identities and email-conflict 409s.

## Day-2 operations

### Identity management (operator shell)

```sh
# List every identity, with email + admin status.
kratos identities list -e http://127.0.0.1:4434 --format json \
  | jq -r '.[] | [.id, .traits.email, .traits.is_admin] | @tsv'

# Inspect one identity (credentials + verifiable addresses + state).
kratos identities get <id> -e http://127.0.0.1:4434

# Disable a compromised identity (can't log in; data preserved).
kratos identities update <id> -e http://127.0.0.1:4434 --state=inactive

# Re-enable.
kratos identities update <id> -e http://127.0.0.1:4434 --state=active

# Delete (irreversible — preserves the panel `users` row but orphans
# kratos_identity_id; use UPDATE to clear the column if you want the
# next kratos-migrate run to re-link).
kratos identities delete <id> -e http://127.0.0.1:4434
```

### Session revocation

```sh
# Revoke every active session for one identity (force re-login everywhere).
kratos sessions revoke -e http://127.0.0.1:4434 --all-for-identity <id>

# Revoke one specific session (e.g. after a device-theft report).
kratos sessions revoke <session-id> -e http://127.0.0.1:4434
```

### MFA reset

Clear the TOTP credential so the user re-enrolls on next login:

```sh
kratos identities patch <id> -e http://127.0.0.1:4434 \
  --set '[{"op":"remove","path":"/credentials/totp"}]'
kratos identities patch <id> -e http://127.0.0.1:4434 \
  --set '[{"op":"remove","path":"/credentials/lookup_secret"}]'
```

The user logs in with password only, then Security → Authenticator to
re-enroll. This is also the answer to "I lost my phone" tickets.

### Recovery code (replaces M5a impersonation + M5b break-glass)

Generate a one-time recovery URL the operator can send to a locked-out user:

```sh
curl -sS -X POST http://127.0.0.1:4434/admin/recovery/code \
  -H 'Content-Type: application/json' \
  -d '{"identity_id":"<uuid>"}' | jq .
```

Response includes `recovery_link` — the user clicks it, sets a new
password, logs in. No plaintext password ever touches the operator's
inbox or shell.

### Backup + restore

```sh
# Backup (daily cron recommended).
mysqldump --single-transaction jabali_kratos > /var/backups/kratos-$(date +%F).sql

# Restore.
mariadb jabali_kratos < /var/backups/kratos-2026-04-20.sql
systemctl restart jabali-kratos
```

### Kratos DB loss recovery

If `jabali_kratos` is lost and no backup exists, identities are gone but
the panel `users` table still holds `kratos_identity_id` references. The
path forward:

1. Reprovision: `install.sh` is idempotent — re-running `install_kratos`
   rebuilds the schema. Existing `kratos_identity_id` values now point at
   nothing.
2. Clear the stale column so `kratos-migrate` will treat every user as
   unmigrated:
   ```sql
   UPDATE users SET kratos_identity_id = NULL;
   ```
3. Re-run `jabali kratos-migrate`. Password hashes survived (they live in
   panel `users.password_hash`), so bcrypt passthrough re-creates every
   identity with zero re-enrollment.
4. 2FA-enabled users must re-enroll TOTP — same story as the initial
   cutover (see "TOTP re-enrollment" above). There is no way to recover
   TOTP secrets from a lost Kratos DB.

### Self-signed TLS bootstrap for split-host Kratos

If operators eventually front Kratos on a separate hostname (`kratos.
example.com` instead of loopback), they must terminate TLS there. The
nginx `/.ory/` proxy in the panel vhost assumes loopback; the panel's
`auth.kratos.public_url` would become the external URL. Self-signed cert
on the Kratos host:

```sh
openssl req -x509 -nodes -newkey rsa:2048 \
  -keyout /etc/kratos/tls.key \
  -out /etc/kratos/tls.crt \
  -days 365 -subj "/CN=kratos.local"
```

Drop the nginx proxy block, set `public_url = "https://kratos.local"`,
add the self-signed cert to the panel's CA store so whoami calls verify.
For production use, Let's Encrypt the Kratos hostname instead.
