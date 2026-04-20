# M16 Hydra Identity Runbook

Operator-facing recovery + routine-maintenance playbook for the Ory Hydra
half of the panel's identity stack (M16). Pairs with the M20 Kratos
runbook — Kratos owns identities, Hydra owns OAuth 2 / OIDC grants.

> Bind addresses (loopback-only): public `127.0.0.1:4444`, admin
> `127.0.0.1:4445`. Panel-api reverse-proxies `/oauth2/*`,
> `/.well-known/openid-configuration`, `/userinfo`,
> `/.well-known/jwks.json` in-process. **NEVER expose 4445 off-host** —
> the admin API has no auth; the loopback bind IS the auth.

---

## Overview

Routine operations:

| Task | Command |
|------|---------|
| List OAuth 2 clients | `hydra list clients --endpoint http://127.0.0.1:4445 --format json \| jq '.items[] \| {client_id,client_name,metadata}'` |
| Show one client | `hydra get client <id> --endpoint http://127.0.0.1:4445` |
| Delete a client | `hydra delete client <id> --endpoint http://127.0.0.1:4445` |
| Revoke all tokens for a client | `hydra revoke token --endpoint http://127.0.0.1:4445 --client-id <id>` |
| Revoke sessions by identity | `curl -s -X DELETE "http://127.0.0.1:4445/admin/oauth2/auth/sessions/login?subject=<kratos-id>"` |
| Service status | `systemctl status jabali-hydra` |
| Service logs (follow) | `journalctl -u jabali-hydra -f` |
| DB schema | `mysql jabali_hydra -e "SHOW TABLES"` |

---

## A Hydra client is orphaned (no matching install row)

### Symptoms

- `hydra list clients` shows a client whose `client_name` is `"<App>
  @ <domain>"` but the panel's install list doesn't include that
  domain+subdir
- A user reports the WP login still redirects to the panel's consent
  screen even after the install was deleted

### Diagnosis

```bash
# Grab the client_id from hydra
hydra list clients --endpoint http://127.0.0.1:4445 --format json \
  | jq '.items[] | {client_id, client_name, application_install_id: .metadata.application_install_id}'

# Check whether the application_install_id still exists panel-side
mysql -D jabali -e "SELECT id,app_type,status FROM application_installs WHERE id='<application_install_id>';"
```

### Resolution

Delete-path compensating transaction runs best-effort. If it missed the
client (panel-api was down during the delete, or the install row was
removed by a DB admin outside the API), clean up by hand:

```bash
hydra delete client <client_id> --endpoint http://127.0.0.1:4445
```

Orphaned clients are harmless — their `redirect_uris` point at a docroot
that no longer serves WordPress — but they pollute `hydra list clients`
and are noise during incident triage. Prune aggressively.

---

## Install succeeded but Allow on the consent screen bounces back with `invalid_request`

### Symptoms

- WP login button redirects to panel's `/consent?challenge=...`
- User clicks Allow
- Browser lands back on WP login with `?error=invalid_request` or
  `?oidc_error=...`

### Diagnosis

```bash
# Confirm the redirect_uri registered with Hydra matches exactly what
# WP's OpenID Connect Generic plugin sends. Hydra rejects even a
# trailing-slash mismatch as invalid_request.
hydra get client <client_id> --endpoint http://127.0.0.1:4445 \
  | jq '{redirect_uris, grant_types, response_types, scope}'

# What the plugin sends (read from wp-cli on the install host):
systemd-run --quiet --uid=<user> --gid=<user> --slice=jabali-user-<user>.slice --pipe --wait \
  wp option get openid_connect_generic_settings --path=<installPath> --format=json \
  | jq '{endpoint_login, scope, client_id}'
```

### Probable Causes & Resolution

1. **Subdirectory install landed at docroot**: the redirect_uri in Hydra
   includes `/blog` but the plugin wrote `/wp-admin/admin-ajax.php`
   without the subdir. Fix by deleting + re-creating the install so
   `applications_service.go`'s siteURL re-derivation kicks in — don't
   hand-edit `openid_connect_generic_settings` unless you understand
   Hydra's exact-match semantics.

2. **HTTPS vs HTTP mismatch**: the plugin sends `https://` but Hydra has
   `http://` registered (or vice versa). Happens when the install ran
   before Let's Encrypt issued a cert. Re-mint the client via delete +
   install, don't patch.

3. **Stale client after a cert rotation that moved the cert host**
   (apex → www subdomain): redirect_uri still pins the old host.
   Re-install is the canonical fix; a manual `hydra update client` is
   possible but bypasses the panel's compensating-rollback invariants.

---

## Consent screen shows "This consent request is no longer valid"

### Symptoms

- User opens `/consent?challenge=...` and sees the 404 actionable
  message instead of the approval card

### Diagnosis

The challenge is single-use and expires in 15 minutes (TTL in
`install/hydra.yml.tmpl`, `ttl.login_consent_request`). Once consumed,
subsequent loads of the same URL return 404 from
`GET /api/v1/oauth2/consent/:challenge`.

### Resolution

Always start the flow fresh from the app's login page. The user
bookmarking `/consent?challenge=...` is a misuse — explain that and move
on. If the same user consistently sees this on the **first** load,
check clock skew between panel-api and Hydra (both read the same
`time.Now()` but Hydra persists `iat`/`exp` in the database):

```bash
timedatectl status          # clock sync
systemctl status chrony     # or the NTP daemon in use
```

---

## Token revocation: "revoke this user from every install"

Use case: a compromised Kratos session. Kratos itself is revoked via
`kratosclient.RevokeSession` (see M20 runbook); to cascade that to every
active OAuth 2 token issued to downstream apps, use the
`identity_provider_session_id` Hydra stores on each login-accept
(ADR-0036 Decision 5).

### Steps

```bash
# 1. Grab the sha256 of the Kratos session token that was compromised.
#    If you have the cookie value, sha256sum it:
echo -n '<kratos-session-token>' | sha256sum

# 2. Revoke every Hydra session keyed on that IdP-session hash.
#    Takes effect on the next introspection; existing access tokens
#    live until their exp (max 30m per install/hydra.yml.tmpl ttl).
curl -s -X DELETE \
  "http://127.0.0.1:4445/admin/oauth2/auth/sessions/login?identity_provider_session_id=<sha256>"

# 3. For belt-and-suspenders, revoke tokens by subject too:
curl -s -X DELETE \
  "http://127.0.0.1:4445/admin/oauth2/auth/sessions/consent?subject=<kratos-identity-id>&all=true"
```

The 30-minute access-token lifetime means even without step 3 you have
at most 30 minutes of risk after the session revoke. If that window is
unacceptable, lower `ttl.access_token` in `hydra.yml.tmpl` and restart
— but note every refresh round-trip goes through Kratos whoami, so
tuning too low produces load.

---

## DB backup + restore

Hydra has its own MariaDB schema `jabali_hydra`, separate from
`jabali`. Backing up `jabali` alone is **not** sufficient — a restore
without the Hydra tables leaves every `application_installs.oidc_client_id`
pointing at a nonexistent Hydra row, and every login attempt 404s at
`hydra get client`.

### Backup (both schemas)

```bash
mysqldump --single-transaction --routines --triggers \
  jabali jabali_hydra \
  > /var/backups/jabali-$(date +%F).sql

gzip /var/backups/jabali-$(date +%F).sql
```

### Restore order matters

On restore:

1. Restore `jabali_hydra` first (Hydra schema must exist before Hydra
   starts up — otherwise the systemd unit crashes on migration check).
2. Start `systemctl start jabali-hydra` and wait for ready.
3. Restore `jabali` second. The `oidc_client_id` FKs aren't strict
   database FKs — they're soft pointers verified at runtime by
   `hydraclient.GetClient` — so restore order within `jabali` is
   unrestricted.
4. Start `systemctl start jabali-panel-api`.

### After a partial restore (only `jabali` came back)

Every install row's Hydra client is gone. The cleanest recovery is:

```bash
mysql -D jabali -e "
  UPDATE application_installs
    SET oidc_client_id = NULL,
        oidc_client_secret_enc = NULL
    WHERE oidc_client_id IS NOT NULL;
"
```

…which leaves the install rows intact, disables OIDC (users fall back to
WP's username/password), and lets operators re-enable SSO per install
by clicking **Repair OIDC** (follow-up feature; for now, delete +
recreate the install).

---

## Hydra janitor — prune stale grants + revoked tokens

Hydra's own `janitor` command GCs expired flows and revoked tokens.
It is NOT run automatically by the systemd unit. Schedule it via cron:

```bash
# /etc/cron.d/jabali-hydra-janitor
# Runs nightly at 03:17 — avoids the top-of-hour cron stampede.
17 3 * * * jabali-panel /usr/local/bin/hydra janitor \
  --config /etc/jabali-panel/hydra.yml \
  --tokens --requests --grants \
  --read-from-env >> /var/log/jabali-hydra-janitor.log 2>&1
```

**Don't skip this** — without the janitor, `hydra_oauth2_access`,
`hydra_oauth2_refresh`, and `hydra_oauth2_flow` grow without bound.
Install.sh should drop this file; if it didn't (pre-Wave-E install),
add it by hand once.

---

## CVE response: upgrading Hydra in place

Hydra ships as a pinned SHA-256 in `install/hydra.sha256`. Upgrade
path:

```bash
# 1. Bump the version pin.
vim install/hydra.yml.tmpl            # confirm config still parses
vim install/hydra.sha256              # paste the new vN.N.N SHA

# 2. Test migrations on a snapshot before touching prod:
mysqldump jabali_hydra > /tmp/hydra.sql.bak
mysql -D jabali_hydra < /tmp/hydra.sql.bak   # into a throwaway DB name
HYDRA_VERSION=<new-version> ./install.sh install_hydra --dry-run

# 3. Roll forward on the host:
./install.sh install_hydra
systemctl restart jabali-hydra
systemctl status jabali-hydra

# 4. Smoke: open any panel-managed install's login page, click SSO,
#    verify redirect → consent skip → land on wp-admin.
```

**Don't downgrade Hydra through the CLI flag** — the DB schema moves
forward only. A rollback requires restoring `jabali_hydra` from the
pre-upgrade mysqldump.

---

## Reference: layout of `application_installs` OIDC columns

| Column | Type | Purpose |
|---|---|---|
| `oidc_client_id` | `CHAR(40)` | Hydra-minted client id (ULID-shaped but we don't parse it); unique per install |
| `oidc_client_secret_enc` | `VARBINARY(512)` | AES-256-GCM envelope of Hydra's one-shot client_secret. Shape: `nonce(12) \|\| ciphertext \|\| auth_tag(16)` using the `/etc/jabali-panel/sso.key`. Never logged. |

Both `json:"-"` — the columns never leave the database via the API.
The plaintext secret leaves panel memory exactly once: at install time,
as an agent-call param, to configure the WP plugin's option table.

Migration is `000050_application_installs_oidc_client`. Rollback drops
both columns; it is destructive to existing client linkages, but the
orphaned Hydra clients can be pruned with `hydra list clients` + delete
(see first section above).
