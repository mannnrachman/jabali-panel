# M25 — Unix Sockets Runbook

Operator-facing day-2 ops for the M25 socket cutover. Pair with `docs/adr/0050-m25-unix-sockets.md` (the design rationale).

## Bind state — what should be true after `jabali update`

Run `ss -lntp` on a healthy M25-installed host. The expected localhost-only state:

```
LISTEN  *:25                            stalwart-mail   # SMTP — public, intentional
LISTEN  *:465                           stalwart-mail   # SMTPS — public, intentional
LISTEN  *:587                           stalwart-mail   # submission — public, intentional
LISTEN  *:993                           stalwart-mail   # IMAPS — public, intentional
LISTEN  127.0.0.1:8080                  stalwart-mail   # admin-http (M25 Step 7)
LISTEN  127.0.0.1:8446                  stalwart-mail   # JMAP loopback
LISTEN  127.0.0.1:18181                 stalwart-mail   # internal/training (M25 Step 7)
LISTEN  *:443                           nginx
LISTEN  *:80                            nginx
LISTEN  *:8443                          nginx           # M25 Step 4 — terminates TLS for panel
# MariaDB no longer binds TCP — skip-networking (see 99-jabali-skip-networking.cnf); socket at /var/run/mysqld/mysqld.sock
LISTEN  127.0.0.1:53                    pdns_recursor   # M6.3
LISTEN  10.0.3.13:53                    pdns_server     # public DNS
LISTEN  127.0.0.1:5300                  pdns_server     # M6.3 split-port
```

What MUST NOT appear:

- `*:4433` or `*:4434` — Kratos public/admin must be Unix sockets.
- `*:3000` or `127.0.0.1:3000` — Bulwark must be Unix socket.
- `*:35181` (or any non-`127.0.0.1` :8080) — Stalwart admin must be loopback-pinned.
- `*:8443` bound by anything other than `nginx` — panel-api must be Unix socket; only nginx terminates TLS on :8443.
- `0.0.0.0:5355` — LLMNR must be off.

Sockets that MUST exist with correct perms:

| Path | Owner | Group | Mode |
|---|---|---|---|
| `/run/jabali-kratos/admin.sock` | jabali | jabali-sockets | 0660 |
| `/run/jabali-kratos/public.sock` | jabali | jabali-sockets | 0660 |
| `/run/jabali-panel/api.sock` | jabali | jabali-sockets | 0660 |
| `/run/jabali-bulwark/bulwark.sock` | jabali-webmail | jabali-sockets | 0660 |

`jabali-sockets` group must contain `jabali`, `www-data`, `jabali-webmail` (NOT `jabali-mail`):

```bash
getent group jabali-sockets
# jabali-sockets:x:9XX:jabali,www-data,jabali-webmail
```

## Symptom → fix

### nginx 502 Bad Gateway

Almost always: nginx (running as www-data) can't `connect(2)` to a backend socket.

```bash
# Confirm the socket exists + check perms
ls -la /run/jabali-kratos/public.sock /run/jabali-panel/api.sock /run/jabali-bulwark/bulwark.sock

# Confirm www-data is in jabali-sockets
groups www-data | grep jabali-sockets

# If the group is missing the membership: re-run install.sh (idempotent —
# ensure_jabali_sockets_group adds the user in seconds), then RESTART
# nginx (group changes don't apply to running processes — workers must
# fork from a re-exec'd master).
sudo install.sh --debug
sudo systemctl restart nginx

# If the socket file mode is 0644 (umask race): restart the offending
# service so its ExecStartPost chmod block runs.
sudo systemctl restart jabali-kratos     # for kratos
sudo systemctl restart jabali-panel      # for panel-api
sudo systemctl restart jabali-webmail    # for bulwark
```

### Panel returns 502 only on /sso/webmail

The mail vhost (`/etc/nginx/sites-available/<domain>-mail.conf`) references the `jabali_panel_api` upstream defined in `/etc/nginx/sites-available/jabali-panel.conf`. If the panel vhost was hand-edited away, the upstream isn't declared and nginx can't proxy `/sso/webmail`.

```bash
# Re-render the panel vhost from the template:
sudo install.sh --debug
sudo nginx -t && sudo systemctl reload nginx
```

### Kratos won't start: "address already in use"

Stale Unix socket from an unclean shutdown. The systemd unit's `RuntimeDirectoryPreserve=no` should clean it on stop, but a hard kill bypasses that.

```bash
sudo rm -f /run/jabali-kratos/admin.sock /run/jabali-kratos/public.sock
sudo systemctl restart jabali-kratos
```

`panel-api/cmd/server/listener.go` already does this for `/run/jabali-panel/api.sock` automatically — no operator action needed for panel-api.

### `ss -lntp` shows `*:8080` (Stalwart admin still bound to all interfaces)

Existing Stalwart install with the pre-M25 apply-plan in its registry. `stalwart-cli apply` is `create`-only and won't update the listener.

```bash
# Wipe Stalwart's data (DESTRUCTIVE — only on a host where Stalwart
# state is rebuildable from MariaDB):
sudo systemctl stop jabali-stalwart
sudo rm -rf /var/lib/stalwart
sudo install.sh --debug   # re-runs install_stalwart_apply with the M25 plan
```

If wiping isn't acceptable, edit Stalwart's runtime config via `stalwart-cli` to mutate the existing listener's `bind`:

```bash
stalwart-cli config update '#admin-http.bind' '{"127.0.0.1:8080":true}'
sudo systemctl restart jabali-stalwart
```

### Wildcard `:35181` lingers

Same as above — Stalwart's ephemeral was running before the `#internal-loopback` listener landed. Restart picks up the pinned bind.

```bash
sudo systemctl restart jabali-stalwart
ss -lntp | grep 35181   # should be empty
ss -lntp | grep 18181   # should show 127.0.0.1:18181
```

## Rollback per service (systemd drop-in templates)

Each service has a per-service revert. They DO NOT chain — reverting Kratos doesn't auto-revert panel-api. After dropping any of these in:

```bash
sudo systemctl daemon-reload
sudo systemctl restart <unit>
ss -lntp | grep <port>   # confirm TCP back up
```

### Revert Kratos to TCP

`/etc/systemd/system/jabali-kratos.service.d/90-revert-to-tcp.conf`:

```ini
[Service]
# Override RuntimeDirectory bits so /run/jabali-kratos is no longer
# created on start (the socket file would otherwise still be made
# alongside the TCP listener).
RuntimeDirectory=
ExecStartPost=
```

Then edit `/etc/jabali-panel/kratos.yml`:

```yaml
serve:
  public:
    host: "127.0.0.1"
    port: 4433
  admin:
    host: "127.0.0.1"
    port: 4434
```

And `/etc/jabali-panel/config.toml`:

```toml
[auth.kratos]
public_url = "http://127.0.0.1:4433"
admin_url  = "http://127.0.0.1:4434"
```

### Revert panel-api to TCP + self-TLS

`/etc/systemd/system/jabali-panel.service.d/90-revert-to-tcp.conf`:

```ini
[Service]
Group=jabali
RuntimeDirectory=
```

Then edit `/etc/jabali-panel/config.toml`:

```toml
[server]
addr = "127.0.0.1:8443"
tls_cert = "/etc/jabali/tls/panel.crt"
tls_key  = "/etc/jabali/tls/panel.key"
```

Disable the panel nginx vhost (port 8443 collision):

```bash
sudo rm -f /etc/nginx/sites-enabled/jabali-panel.conf
sudo nginx -t && sudo systemctl reload nginx
```

### Revert Bulwark to TCP

`/etc/systemd/system/jabali-webmail.service.d/90-revert-to-tcp.conf`:

```ini
[Service]
Group=jabali-webmail
RuntimeDirectory=
Environment=
Environment=HOSTNAME=127.0.0.1
Environment=PORT=3000
Environment=NODE_ENV=production
ExecStart=
ExecStart=/usr/bin/node /opt/jabali-webmail/server.js
```

And edit the per-domain mail vhosts to swap `proxy_pass http://jabali_bulwark/;` back to `proxy_pass http://127.0.0.1:3000;` — OR change `/etc/nginx/conf.d/jabali-bulwark-upstream.conf`'s `server unix:...;` to `server 127.0.0.1:3000;` (Form D pays off here — one line, no per-vhost sweep).

## External monitoring

Pre-M25 monitoring that pinged `https://localhost:8443/health` or `http://127.0.0.1:4433/health/ready` no longer works. Two options:

1. **Hit the edge.** `https://<panel-host>:8443/health` still works — nginx terminates TLS and proxies through. This is also the preferred path because it goes through the same code path real users hit.
2. **`curl --unix-socket`.** For monitoring tools running on the host:
   ```bash
   curl --unix-socket /run/jabali-panel/api.sock     http://x/health
   curl --unix-socket /run/jabali-kratos/admin.sock  http://x/admin/health/ready
   curl --unix-socket /run/jabali-kratos/public.sock http://x/health/ready
   curl --unix-socket /run/jabali-bulwark/bulwark.sock http://x/api/health
   ```

## LLMNR

Disabled via `/etc/systemd/resolved.conf.d/10-jabali-disable-llmnr.conf`. To re-enable on a Windows-heavy LAN:

```bash
sudo tee /etc/systemd/resolved.conf.d/20-operator-enable-llmnr.conf <<EOF
[Resolve]
LLMNR=yes
EOF
sudo systemctl restart systemd-resolved
```

Higher-numbered drop-in wins. The jabali drop-in stays in place; operator overrides cleanly.

## M25.1 — shipped

- **Kratos DSN flip to socket** — commit `67bcc9a feat(m25.1): flip Kratos DSN to unix socket`. Verified on 10.0.3.13.
- **phpMyAdmin SSO socket-awareness + MariaDB `skip-networking`** — commit `f04d4f2 feat(m25.1): phpMyAdmin → unix socket + MariaDB skip-networking`. `sso_phpmyadmin_validate.go` returns a `socket` field; `sso.php` forwards it via `PMA_single_signon` as `connect_type=socket`. `install.sh` writes `/etc/mysql/mariadb.conf.d/99-jabali-skip-networking.cnf` and asserts `ss -tln` shows no `:3306`. Verified on 10.0.3.13: no TCP listener; processlist shows `jabali_panel_app` + `jabali_kratos` + `jabali_pdns` all via socket.

Rollback (single host): remove `/etc/mysql/mariadb.conf.d/99-jabali-skip-networking.cnf` and `systemctl restart mariadb` — restores `127.0.0.1:3306` and TCP loopback for clients that haven't been socket-flipped.

## What's out of scope (won't be re-litigated)

- DNS-protocol services (`pdns_recursor`, `pdns_server`, `systemd-resolved`) — sockets don't apply to the protocol.
- Public listeners (nginx :80/:443, sshd :22, Stalwart SMTP/IMAP/POP3) — wildcard binds are the contract.
- Cross-host deployments (memory `project_arch_decisions.md` is single-host).
- mTLS between backends — superseded by socket permissions.
