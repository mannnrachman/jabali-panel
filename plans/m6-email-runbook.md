# M6 Email runbook

Operational reference for the M6 email stack: Stalwart (SMTP/IMAP/JMAP
+ built-in rspamd) and Bulwark (Next.js webmail). Domain lifecycle is
owned by the panel; mailbox-level provisioning happens inline via the
UI, CLI, or HTTP API. ADR-0041–ADR-0045 capture the architectural
decisions this runbook assumes.

## Installation

`install.sh` ships everything:

- `install_stalwart` — binary + systemd unit + `/var/lib/stalwart`
  (RocksDB) + `/etc/stalwart/config.json` (rendered from
  `install/stalwart/config.json.tmpl`) + `/etc/jabali-panel/dkim/`
  (Ed25519 per-domain keys) + `/etc/jabali-panel/stalwart-admin.token`.
- `install_bulwark` — standalone Next.js tarball under `/opt/jabali-webmail`
  (SHA-256 pinned in `install/bulwark.sha256`), `jabali-webmail` service
  user, `/var/lib/jabali-webmail/settings`, `/etc/jabali-panel/bulwark-session.key`
  (generated once, preserved across re-runs), `/etc/jabali-panel/bulwark.env`
  (rendered from `install/bulwark/bulwark.env.tmpl`).
- Both systemd units are installed **disabled**. They're started on first
  `domain.email_enable` for any domain on the host, then left running —
  additional domains reuse the same Stalwart + Bulwark pair.

Verify after install:

```sh
systemctl is-active jabali-stalwart jabali-webmail     # active if any domain has email on
curl -fsS http://127.0.0.1:8446/.well-known/jmap       # 401 (needs Basic auth) — means JMAP is up
curl -fsS http://127.0.0.1:3000/api/health             # 200 — Bulwark Next.js is up
```

## First-enable checklist

**Do this before flipping email on for the first domain.** The panel
cannot work around any of these, and the failure symptoms often look
like M6 bugs when they are really infrastructure gaps.

### 1. Port 25 outbound MUST be open

Most cloud providers (AWS, GCP, Azure, Hetzner, OVH) block outbound
port 25 by default to prevent spam from compromised VMs. Detect:

```sh
openssl s_client -connect aspmx.l.google.com:25 -starttls smtp -brief
```

If this hangs or times out, the provider is blocking 25. Options:
- AWS: file an [EC2 port-25 unblock request](https://aws.amazon.com/premiumsupport/knowledge-center/ec2-port-25-throttle/).
- Hetzner, OVH, GCP: upgrade account or file a ticket citing transactional-mail use.
- **No workaround** without a cooperating upstream — Stalwart will
  appear to accept outbound mail but deliveries will time out at the
  provider's firewall.

### 2. Reverse DNS (PTR) MUST resolve to the server hostname

```sh
dig +short -x $(dig +short $(hostname -f))
# Must return the server's FQDN with a trailing dot.
```

If it doesn't, set a PTR record at your cloud provider's network
console (often called "rDNS" or "reverse lookup"). Gmail and most
other receivers reject or spam mail whose EHLO hostname doesn't
reverse-map to the sending IP.

### 3. Public A record matches the sending IP

```sh
dig +short @8.8.8.8 $(hostname -f) A
ip -4 addr show | awk '/inet.* scope global/ {print $2}'
```

These must agree — or at least the public-facing address used for
outbound SMTP must match `A $(hostname -f)` as seen from the open
internet. SPF (`v=spf1 mx ~all`) resolves MX back to this hostname, so
a mismatch fails SPF even when the DKIM signature is fine.

### 4. Firewall: 25, 465, 587, 993 inbound

```sh
sudo ss -ltnp | grep -E ':(25|465|587|993) '
```

All four should bind (`stalwart-mail`). If you front the VM with a
cloud firewall (AWS Security Group, UFW, etc.), make sure the same
ports are allowed inbound from `0.0.0.0/0`.

### 5. DNS autoconfig records published

After `jabali domain email-enable <domain>`, the panel inserts 3 M6
DNS rows: `jabali._domainkey.<domain>` TXT (DKIM), `autoconfig.<domain>`
CNAME, `_autodiscover._tcp.<domain>` SRV. Verify:

```sh
dig @127.0.0.1 jabali._domainkey.<domain> TXT +short
# -> "v=DKIM1; k=ed25519; p=..."
dig @127.0.0.1 autoconfig.<domain> CNAME +short
# -> mail.
```

If empty, check the domain's zone exists:

```sh
mysql -u jabali_panel_app jabali_panel \
  -e "SELECT name FROM dns_zones WHERE domain_id IN (SELECT id FROM domains WHERE name='<domain>');"
```

No row → the domain was created before M4 DNS or the zone was manually
deleted. Recreate: `jabali domain email-disable <domain> && jabali domain email-enable <domain>`
drives the autoconfig inserts from scratch.

## Operator workflows

### Enable / disable email on a domain

```sh
jabali domain email-enable <domain>
# Generates Ed25519 DKIM keypair on disk; writes DKIM/autoconfig DNS
# rows; flips domains.email_enabled; lazy-starts Stalwart + Bulwark;
# next reconciler tick writes the mail.<domain> nginx vhost.

jabali domain email-disable <domain>
# Reloads Stalwart (SqlDirectory re-reads the email_enabled column);
# removes M6-managed DNS rows (managed_by='m6'); M4 bootstrap records
# survive; DKIM key on disk is preserved per ADR-0043 so re-enable
# doesn't invalidate cached receiver-side DKIM signatures.
```

The UI equivalent lives under DomainEdit → Email tab.

### Mailbox CRUD

```sh
jabali mailbox list --domain <domain>
jabali mailbox create --domain <domain> --local alice --quota-mb 1024
#   → prints a ULID password reveal-once; store it, you cannot retrieve it.
jabali mailbox passwd alice@<domain>                 # rotate, new password printed once
jabali mailbox set-quota alice@<domain> 2048         # MiB
jabali mailbox delete alice@<domain>                 # destroys Stalwart account first, then the row
```

Same surface in the UI: user shell at `/jabali-panel/mailboxes`, admin
at DomainEdit → Mailboxes tab. Passwords are shown in a single reveal-once
modal matching the M7 phpMyAdmin flow; copy on the spot.

### Webmail SSO (Phase B)

The "Webmail" icon on every mailbox row opens Bulwark already logged
in as that mailbox. Under the hood:

1. Panel-API mints a 32-byte random token, stores the SHA-256 hash in
   `mailbox_sso_tokens` with a 5-minute TTL + `FOR UPDATE` on consume
   so a token can only be used once.
2. The UI opens a new tab to `https://mail.<domain>/sso/webmail?token=…`.
3. The landing endpoint decrypts the mailbox's `password_enc` (sealed
   with `/etc/jabali-panel/sso.key`), POSTs `{serverUrl, username,
   password}` to Bulwark's `/api/auth/session` over loopback, forwards
   the Set-Cookie headers onto its own `303 See Other /`.

Operator notes:
- Mailboxes created before migration 000056 landed have
  `password_enc=NULL`. The UI shows a `sso_unavailable_rotate_password`
  hint; a single `jabali mailbox passwd <email>` populates it and the
  button starts working. **No data is lost.**
- Bulwark's own `SESSION_SECRET` is never exposed to the panel —
  Bulwark signs the cookie itself. A full panel-DB compromise cannot
  forge arbitrary webmail sessions.
- **M6.1 SAN expansion:** enabling email on a domain flips the SSL cert
  row to `renewing`; the reconciler re-issues the cert with
  `mail.<domain>` + `autoconfig.<domain>` added to its SANs (certbot
  `--expand` handles existing certs, self-signed regens in place). The
  cert on disk therefore covers the hostname Bulwark's server-side JMAP
  verify fetches, so the previous `NODE_TLS_REJECT_UNAUTHORIZED=0`
  workaround is no longer needed.
- **Dev-host trust store:** self-signed installs need the panel's local
  CA imported into the OS trust store (`/usr/local/share/ca-certificates/`
  + `update-ca-certificates`) AND into the browser's trust store. Two
  reasons the browser trust matters: (1) Chrome refuses to save
  Bulwark's `Secure` session cookies on pages it marks "Not secure,"
  so without trust the SSO redirect succeeds but the cookie is silently
  dropped and the user lands back at `/en/login`. (2) Service workers
  won't register without a valid chain. Production Let's Encrypt certs
  pass both checks natively.
- **Dev-host /etc/hosts:** the VM's `/etc/hosts` must map
  `mail.<domain>` and `autoconfig.<domain>` to `127.0.0.1` (or the
  panel's bound IP). Bulwark's server-side verify fetch uses the OS
  resolver; without a hosts override it goes to public DNS and lands
  on whoever happens to own the parent domain.

## DKIM rotation (manual, v1)

Automatic rotation is M6.1 scope (see ADR-0043). For v1, operator-driven:

```sh
# 1. Disable email (preserves the old DNS row so clients can still
#    verify in-flight mail during the switchover).
jabali domain email-disable <domain>

# 2. Move the old key out of the way so email_enable regenerates.
sudo mv /etc/jabali-panel/dkim/<domain>.key /etc/jabali-panel/dkim/<domain>.key.old

# 3. Re-enable. New Ed25519 key; panel writes the new DNS row over
#    the old one (jabali._domainkey.<domain> TXT, managed_by=m6).
jabali domain email-enable <domain>

# 4. Give receiver caches time to expire the old key before deleting
#    the .old file (48h is a safe upper bound; most are under 6h).
```

### Ed25519 long-tail: RSA-2048 fallback

Some corporate mail servers still don't accept Ed25519 DKIM signatures
(ADR-0043 documents the catalogue). Symptom: mail to Gmail / Outlook.com
passes, mail to `company.com` bounces or lands in spam with
`dkim=neutral (no key)` in the `Authentication-Results` header.

**Recipe** (manual in v1; CLI helper is M6.1):

```sh
# 1. Generate an RSA-2048 keypair.
sudo openssl genrsa -out /etc/jabali-panel/dkim/<domain>.rsa.key 2048
sudo chown jabali:jabali /etc/jabali-panel/dkim/<domain>.rsa.key
sudo chmod 600 /etc/jabali-panel/dkim/<domain>.rsa.key

# 2. Extract the public half as a DKIM TXT record.
sudo openssl rsa -in /etc/jabali-panel/dkim/<domain>.rsa.key -pubout 2>/dev/null \
  | sed -e '/^-----/d' | tr -d '\n'

# 3. Add a TXT record `jabali-rsa._domainkey.<domain>` with
#    "v=DKIM1; k=rsa; p=<public-key>". Panel DNS UI or
#    `dig -t AXFR <zone>` to confirm.

# 4. Tell Stalwart about the second key — edit
#    /etc/stalwart/apply-plan.json to add a second DkimSignature entry
#    with selector "jabali-rsa", then restart Stalwart:
sudo systemctl restart jabali-stalwart
```

Long-term fix path is a `jabali domain email-dkim-add-rsa` CLI wrapper
plus a `dkim_selectors` JSON column on `domains`; tracked under M6.1.

## Backup & restore

### What to back up

1. `/var/lib/stalwart/` — RocksDB mail storage. The running process
   holds write locks; take a **live** checkpoint via `stalwart-cli`,
   not a raw copy.
   ```sh
   sudo -u jabali-mail stalwart-cli \
     --url http://127.0.0.1:8446 \
     backup -p /var/backups/stalwart-$(date +%F)
   ```
2. `/etc/jabali-panel/dkim/` — Ed25519 private keys. Plain file copy;
   ownership is `jabali:jabali`, mode `0750` for the dir and `0600`
   for each key file. Preserve permissions on restore.
3. `jabali_panel.mailboxes` (DB row, bcrypt hash + ciphered plaintext).
   Captured by the regular panel MariaDB dump; no M6-specific work.
4. `/etc/jabali-panel/bulwark-session.key` — Bulwark session-cookie
   signing secret. **Do not rotate this on restore** unless you want
   every active Bulwark "remember me" session invalidated.

### Restore flow

```sh
# 1. Stop services.
sudo systemctl stop jabali-panel jabali-stalwart jabali-webmail

# 2. Restore the MariaDB dump (includes mailboxes + mailbox_sso_tokens).
mysql jabali_panel < backup.sql

# 3. Restore DKIM keys + preserve perms.
sudo tar -xzpf dkim-backup.tar.gz -C /etc/jabali-panel/

# 4. Restore Stalwart RocksDB.
sudo tar -xpf stalwart-backup.tar -C /var/lib/stalwart/
sudo chown -R jabali-mail:jabali-mail /var/lib/stalwart

# 5. Bring services back up. Reconciler re-publishes DNS + mail vhosts
#    on the next tick.
sudo systemctl start jabali-stalwart jabali-webmail jabali-panel
```

## Migrating from an external mail server

Deferred to M15 (ADR-0044). Until M15 lands, the manual path is
`stalwart-cli imap-migrate`:

```sh
sudo -u jabali-mail stalwart-cli \
  --url http://127.0.0.1:8446 \
  imap-migrate \
  --source imaps://old-server.example.net:993 \
  --source-user alice@example.net \
  --dest-user alice@<your-domain>
```

See [Stalwart docs](https://stalw.art/docs/directory/imap-migrate) for
the full flag list. Run it per-mailbox; can be backgrounded for large
accounts.

## Port reachability self-test

```sh
# All four should return a cert chain, not a timeout.
for port in 465 587 993; do
  echo "=== port $port ==="
  openssl s_client -connect $(hostname -f):$port -servername $(hostname -f) -brief </dev/null 2>&1 | head -5
done

# Port 25 uses STARTTLS, not implicit TLS:
openssl s_client -connect $(hostname -f):25 -starttls smtp -brief </dev/null 2>&1 | head -5
```

If any port times out **from the server itself**, check `systemctl status jabali-stalwart`.
If it times out **from the open internet but succeeds locally**, check
your cloud provider's firewall / security group.

## Troubleshooting matrix

### Bulwark login fails

```sh
systemctl status jabali-webmail
journalctl -u jabali-webmail -n 200
curl -fsSI http://127.0.0.1:3000/api/health
curl -fsSI --resolve mail.<domain>:443:127.0.0.1 https://mail.<domain>/ -k
```

Common causes:
- `Failed to verify JMAP session` → Bulwark's server-side fetch to
  `https://mail.<domain>/.well-known/jmap` failed. Check, in order:
  (1) `getent hosts mail.<domain>` resolves to the panel host (add
  to `/etc/hosts` on dev VMs — see §Webmail SSO);
  (2) the cert at `/etc/ssl/jabali-selfsigned/<domain>/fullchain.pem`
  (or the LE `live/` path) has `mail.<domain>` in its SAN list —
  `openssl x509 -in <path> -noout -text | grep DNS:` should show it.
  If it's missing, the M6.1 SAN-expansion trigger didn't fire; poke
  the reconciler with `POST /api/v1/domains/:id/ssl/renew` or wait
  for the next tick;
  (3) the local CA is trusted by the OS (`update-ca-certificates`
  after copying the self-signed CA into `/usr/local/share/ca-certificates/`).
- `500 Internal Server Error` from the root `/` path → the nginx vhost
  is sending `X-Forwarded-Proto: https` to the Bulwark location,
  tripping the Next.js middleware-rewrite self-proxy bug. Our vhost
  template deliberately omits the header; if you see it in
  `/etc/nginx/sites-available/<domain>-mail.conf`, the template has
  drifted — force a rewrite with `rm /etc/nginx/sites-available/<domain>-mail.conf && systemctl restart jabali-panel`.
- Bulwark returns 502 on `/api/auth/session` → our nginx vhost is
  proxying `/api` straight to Stalwart instead of to Bulwark. Same
  force-rewrite fixes it.

### Mailbox usage always 0

`last_usage_bytes` is updated by `reconcileMailboxUsage` (panel-api
reconciler loop) via `mailbox.usage` agent probes. Check:

```sh
journalctl -u jabali-panel | grep mailbox.usage
# No output → the reconciler hasn't run yet, or the code path is disabled.
```

Usage refresh runs on the main reconciler cadence (`ReconcilerInterval`
in panel config, default 60s). Wait two ticks before escalating.

### DKIM records missing

```sh
dig @127.0.0.1 jabali._domainkey.<domain> TXT +short
# Empty → the reconciler didn't write the M6 row. Check the zone:
mysql jabali_panel -e "SELECT r.name, r.type, r.managed_by FROM dns_records r JOIN dns_zones z ON r.zone_id=z.id JOIN domains d ON z.domain_id=d.id WHERE d.name='<domain>';"
```

If the `jabali._domainkey` row is absent, `jabali domain email-disable && jabali domain email-enable`
re-runs the autoconfig insert idempotently.

### Mail to Gmail delivers but Corp-mail-server bounces / spams

Known Ed25519 long-tail. See the **RSA-2048 fallback** recipe above.

### Port 25 blocked by ISP

See the first-enable checklist, item 1. No panel-side workaround.

### Bulwark shows stale mailbox after CLI password change

```sh
sudo systemctl reload jabali-stalwart
```

Stalwart's SqlDirectory caches directory entries for the currently-open
JMAP session. Reload clears the cache; the next Bulwark request
re-authenticates cleanly.

### "Webmail" button shows "rotate password to enable SSO"

The mailbox row has `password_enc=NULL` — either it predates migration
000056 or was created before `SSOKey` was wired. One rotate through
either the UI or `jabali mailbox passwd <email>` fixes it permanently
(the new cipher is written on every subsequent rotate too).

## Known limits (v1)

- No per-user Sieve rules UI (Stalwart backend supports them; UI is M6.1).
- No calendar / contacts (CalDAV / CardDAV — separate milestone).
- No catch-all or forwarder management. Mail to `unknown@<domain>`
  bounces; the panel has no `mail_forwarders` table yet.
- No cluster mode (FoundationDB) — single-node RocksDB only.
- No secondary MX failover.
- ACME certs for `mail.<domain>` SAN expansion is M6.1; v1 reuses the
  main domain's cert which may not list `mail.<domain>` → browser
  warning. Self-signed fallback (`/etc/ssl/jabali-selfsigned/<domain>/`)
  covers operator-only installs.
