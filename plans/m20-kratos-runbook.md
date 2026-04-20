# M20 Kratos runbook

Operational reference for the Kratos identity integration. The panel's
own JWT/TOTP/refresh-token surface was removed in the M20 batch — Kratos
is the only identity store, its self-service flows own password resets
and 2FA enrolment. This runbook covers day-2 ops + recovery.

## Installation

Everything required is driven by `install.sh install_kratos`:

1. Downloads Kratos `v1.3.1` (SHA-256 pinned in `install/kratos.sha256`).
2. Creates MariaDB schema `jabali_kratos` + restricted user.
3. Renders `/etc/jabali-panel/kratos.yml` from `install/kratos.yml.tmpl`.
4. Runs `kratos migrate sql -y`.
5. Installs `/etc/systemd/system/jabali-kratos.service` under `jabali.slice`.
6. Enables + starts the unit.

The flow is idempotent: re-running on an existing install is a no-op
unless the pinned version changes. Verify health after install:

```sh
systemctl is-active jabali-kratos
curl -sf http://127.0.0.1:4433/health/ready && echo OK
curl -sf http://127.0.0.1:4434/admin/health/ready && echo OK
```

panel-api proxies `/.ory/*` in-process to Kratos's public port so the
SPA sees a same-origin endpoint. No nginx vhost edits needed.

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
# kratos_identity_id; clear the column manually if the user should be
# reissued an identity on next login).
kratos identities delete <id> -e http://127.0.0.1:4434
```

### Session revocation

```sh
# Revoke every active session for one identity (force re-login everywhere).
kratos sessions revoke -e http://127.0.0.1:4434 --all-for-identity <id>

# Revoke one specific session (e.g. after a device-theft report).
kratos sessions revoke <session-id> -e http://127.0.0.1:4434
```

### MFA reset ("I lost my phone")

Clear the TOTP credential so the user re-enrolls on next login:

```sh
kratos identities patch <id> -e http://127.0.0.1:4434 \
  --set '[{"op":"remove","path":"/credentials/totp"}]'
kratos identities patch <id> -e http://127.0.0.1:4434 \
  --set '[{"op":"remove","path":"/credentials/lookup_secret"}]'
```

The user logs in with password only, then Security → Authenticator in
the panel's profile page (which links to `/.ory/self-service/settings/browser`)
to re-enroll.

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

If `jabali_kratos` is lost and no backup exists, identities and
credentials are gone. Passwords were stored only in Kratos (bcrypt
cost-12) — the panel DB no longer mirrors them. Recovery path:

1. Reprovision: `install.sh install_kratos` is idempotent — re-running
   it rebuilds the schema.
2. Clear the orphaned link column on the panel side:
   ```sql
   UPDATE jabali_panel.users SET kratos_identity_id = NULL;
   ```
3. For every user, recreate their Kratos identity via the admin API
   and issue a recovery link:
   ```sh
   curl -sS -X POST http://127.0.0.1:4434/admin/identities \
     -H 'Content-Type: application/json' \
     -d '{"schema_id":"default","traits":{"email":"<email>","is_admin":false}}' \
     | jq -r .id
   # feed the returned UUID back into jabali_panel.users.kratos_identity_id,
   # then POST /admin/recovery/code for the same id so the user can set a
   # fresh password.
   ```
4. The `BootstrapAdmin` path in serve.go handles the first admin
   automatically when `JABALI_BOOTSTRAP_ADMIN_EMAIL/_PASSWORD` are set.

There is no bulk migration tool anymore — the `jabali kratos-migrate`
command was deleted with the legacy stack. For multi-user restores,
script the admin-API calls above.

### CLI after M20

Every operator CLI command is direct-DB. The reconciler ticks every
`agent.reconciler_interval` (default 60s), so manual triggers are
unnecessary.

| Subcommand | Status | Notes |
|---|---|---|
| `jabali user list / create / delete` | ✅ Works | Direct DB + Kratos + agent. `create` is atomic (panel row + identity). `delete` cascades to domains + Kratos identity + OS user. |
| `jabali domain list / create / enable / disable / delete` | ✅ Works | Direct DB. Reconciler materialises/tears down nginx within `agent.reconciler_interval`. |
| `jabali package list / create / edit / delete` | ✅ Works | Direct DB, no agent side-effects. |
| `jabali admin slice-cutover` | ✅ Works | Direct DB + agent. |
| `jabali limits *` | ✅ Works | Direct DB + agent. |
| `jabali system *` | ✅ Works | Local-only. |
| `jabali migrate` | ✅ Works | Direct DB. |
| `jabali update` | ✅ Works | Local git + systemctl. |
| `jabali admin disable-2fa` | ❌ Removed | 2FA lives in Kratos — use `kratos identities patch` (see MFA reset above). |
| `jabali admin admin-login` (M5b) | ❌ Removed | Use `POST /admin/recovery/code`. |
| `jabali user login` | ❌ Removed | Same — recovery code flow. |
| `jabali kratos-migrate` | ❌ Removed | One-shot backfill tool from the cutover; no fresh install needs it. |
| `jabali reconcile` | ❌ Removed | Background reconciler covers it. |

### Self-signed TLS bootstrap for split-host Kratos

If operators eventually front Kratos on a separate hostname (`kratos.
example.com` instead of loopback), they must terminate TLS there. The
panel's in-process `/.ory/*` proxy assumes loopback; the panel's
`auth.kratos.public_url` would become the external URL. Self-signed
cert on the Kratos host:

```sh
openssl req -x509 -nodes -newkey rsa:2048 \
  -keyout /etc/kratos/tls.key \
  -out /etc/kratos/tls.crt \
  -days 365 -subj "/CN=kratos.local"
```

Drop the panel's `/.ory/*` proxy registration, set `public_url =
"https://kratos.local"`, add the self-signed cert to the panel's CA
store so whoami calls verify. For production use, Let's Encrypt the
Kratos hostname instead.

## Rollback

There is no in-product toggle — legacy JWT code is deleted. If a severe
regression surfaces, the rollback is a git revert of the M20 removal
commit (`git log --grep='M20\|legacy'`). The Kratos schema stays
intact; reverting the code restores the panel's own auth surface.

## Troubleshooting

Symptoms from post-cutover debugging, with root causes. If a fresh
install falls into any of these states, start here before reading code.

### Login crashes with `e.ui is undefined`

Browser DevTools shows `TypeError: can't access property "messages", e.ui is undefined`; the login form never renders.

Means: `GET /.ory/self-service/login/browser` either 404'd or returned
HTML (SPA fallback). Check:

```sh
curl -sS -k -H "Accept: application/json" -H "Host: jabali-panel.local:8443" \
  "https://127.0.0.1:8443/.ory/self-service/login/browser" | head -c 100
```

Expect JSON starting `{"id":"...`. If it's `<!DOCTYPE html>`, the
`/.ory/*` reverse proxy isn't registered. Either the binary is stale
or `auth.kratos.public_url` is unset — check `/etc/jabali/config.toml`.

### Every `/api/v1/*` returns 401 `identity_not_linked` right after login

Login succeeds in Kratos (journalctl shows "Identity authenticated
successfully") but the dashboard can't fetch anything.

Means: `users.kratos_identity_id` is NULL for the identity Kratos
returned via whoami. Verify:

```sh
mariadb -uroot -e "SELECT email, kratos_identity_id FROM jabali_panel.users WHERE is_admin=1;"
```

NULL → the bootstrap compensating transaction didn't run (Kratos not
ready on first serve, or manual DB edit cleared the column). Patch by
hand:

```sh
curl -sS http://127.0.0.1:4434/admin/identities | python3 -c "import sys,json; [print(i['id'], i['traits']['email']) for i in json.load(sys.stdin)]"
mariadb -uroot -e "UPDATE jabali_panel.users SET kratos_identity_id='<uuid>' WHERE email='<email>';"
```

### Kratos restart-loops with `allOf failed` / `additionalProperties not allowed`

`install/kratos.yml.tmpl` drifted from Kratos's JSON Schema. Full
field-by-field fix list is in ADR-0034's post-cutover section. If
Kratos upgrades past 1.3.x and new errors surface, cross-reference
Ory's [configuration reference](https://www.ory.sh/docs/kratos/reference/configuration).
No `kratos validate-config` subcommand exists; journalctl is ground truth.

### `https://domain/` shows nginx 403 + "Not secure" cert

Unrelated to auth — domain has `ssl_enabled=1` but no cert yet. ACME
either hasn't attempted or skipped because `server_settings.admin_email`
is empty:

```sh
mariadb -uroot -e "SELECT admin_email FROM jabali_panel.server_settings;"
```

Empty → set via Server Settings page in the admin UI. The reconciler
picks up the change on its next tick.
