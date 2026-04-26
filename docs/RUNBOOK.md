# Jabali Panel Runbook

Operational procedures for managing the panel in production.

## Per-feature runbooks

Topic-specific runbooks live in [`runbooks/`](runbooks/). The top-level
sections below cover cross-cutting ops (DB, services, upgrades,
emergencies); for one feature at a time start there.

<!-- AUTO-GENERATED:per-feature-runbooks — regenerate via /update-docs -->
| File | Topic |
|------|-------|
| [`runbooks/applications.md`](runbooks/applications.md) | Adding a new app to the M19 Applications registry. |
| [`runbooks/dns-secondary-nameserver.md`](runbooks/dns-secondary-nameserver.md) | Configuring secondary nameservers for managed zones. |
| [`runbooks/m23-responsive.md`](runbooks/m23-responsive.md) | Responsive UI breakpoints + scroll-table contract (M23). |
| [`runbooks/panel-ssl.md`](runbooks/panel-ssl.md) | M32 Let's Encrypt cert for the panel hostname (ADR-0066). |
| [`runbooks/per-user-slices.md`](runbooks/per-user-slices.md) | Per-user PHP-FPM slice + cgroups layout. |
| [`runbooks/php-extensions.md`](runbooks/php-extensions.md) | M9.6 PHP extension management (ADR-0031). |
| [`runbooks/php-fpm-pools.md`](runbooks/php-fpm-pools.md) | M9 PHP-FPM pool manager (ADR-0023). |
| [`runbooks/server-status.md`](runbooks/server-status.md) | M31 server-status aggregator (ADR-0065). |
<!-- /AUTO-GENERATED -->

---

## 1. Database maintenance

### Backup & restore

**Backup:**
```bash
# Full database dump with timestamp
mysqldump -u root -p jabali_panel > /var/backups/jabali_panel_$(date +%Y%m%d_%H%M%S).sql
```

**Restore:**
```bash
# Restore from backup file
mysql -u root -p jabali_panel < /var/backups/jabali_panel_20260418_120000.sql
```

### Migration recovery (`schema_migrations` dirty)

`jabali-panel` runs `golang-migrate` on every boot. If a migration fails
mid-way, the binary refuses to start with:

```
Error: migrate up: Dirty database version <N>. Fix and force version.
```

The cause is in `journalctl -u jabali-panel` a few lines above the
"Dirty" line (usually a SQL error: errno 1553 — FK-referenced index drop —
is the most common surprise). Recover:

1. **Identify the partially-applied SQL** in the binary's embedded
   migrations: `panel-api/internal/db/migrations/<N>_*.up.sql`.
2. **Apply the remainder by hand** (or roll back the partial change) until
   the schema matches what the migration *would have* produced on success.
3. **Mark the version clean:**
   ```sql
   -- If the migration finished by hand:
   UPDATE schema_migrations SET version = <N>, dirty = 0;
   -- If you rolled back to before <N>:
   UPDATE schema_migrations SET version = <N-1>, dirty = 0;
   ```
4. **Restart the panel:** `systemctl restart jabali-panel`. It re-runs
   from the recorded version forward; if you set version=N-1, migration
   N runs again.

> **FK-referenced indexes:** MariaDB refuses to drop the only index
> supporting an FK (errno 1553). Pattern: add the replacement index
> first, then drop the old one. See migration 000045 for the canonical
> example.

---

## 2. Service restart & logs

### Restart panel-api

```bash
systemctl restart jabali-panel
journalctl -u jabali-panel -f  # tail logs
```

### Restart panel-agent

```bash
systemctl restart jabali-agent
journalctl -u jabali-agent -f
```

### Check all services

```bash
systemctl status jabali-panel jabali-agent mariadb nginx
```

---

## 3. phpMyAdmin SSO key rotation

**Purpose:** Rotate the AES-256-GCM key used to encrypt database-user passwords stored in shadow MySQL accounts. This is a sensitive operation and requires careful sequencing.

**Prerequisites:**
- Panel is running normally (`systemctl status jabali-panel`)
- Reconciler is healthy
- Generate new key: `openssl rand 32 | base64 > /tmp/new_key.txt`

**Procedure (7 steps):**

1. **Generate new key file**
   ```bash
   openssl rand 32 | base64 > /tmp/new_sso_key.txt
   ```
   Save this path; you'll use it in steps 4 and 6.

2. **Pause reconciler**
   ```bash
   jabali-panel admin reconciler pause --token YOUR_ADMIN_JWT
   ```
   This prevents concurrent writes to `mysql_shadow_users` while rotating.

3. **Wait for in-flight operations to drain**
   ```bash
   sleep 5
   ```

4. **Execute key rotation transaction**
   ```bash
   jabali-panel sso rotate-key \
     --current-key /etc/jabali/sso_key.txt \
     --new-key /tmp/new_sso_key.txt
   ```
   This decrypts all shadow-account passwords with the old key and re-encrypts them with the new key in a single transaction. If any password decrypt fails, the transaction is aborted and no rows are updated.

5. **Swap key files** (atomic)
   ```bash
   # Verify the new key file is readable
   test -r /tmp/new_sso_key.txt
   
   # Move it into place (replaces old key)
   mv /tmp/new_sso_key.txt /etc/jabali/sso_key.txt
   ```

6. **Reload key without dropping connections** (SIGHUP)
   ```bash
   systemctl kill -s SIGHUP jabali-panel
   ```
   The panel-api process reloads the key from disk without restarting, preserving existing HTTP/WebSocket connections.

7. **Resume reconciler**
   ```bash
   jabali-panel admin reconciler resume --token YOUR_ADMIN_JWT
   ```

**Rollback (if rotate-key fails):**
- Resume reconciler: `jabali-panel admin reconciler resume --token YOUR_ADMIN_JWT`
- The old key in `/etc/jabali/sso_key.txt` remains unchanged; no rows were updated.
- Delete the new key: `rm /tmp/new_sso_key.txt`

**Verification after rotation:**
```bash
# Verify phpMyAdmin SSO still works: POST to /api/v1/sso/phpmyadmin
curl -X POST \
  -H "Authorization: Bearer $JWT" \
  -H "Content-Type: application/json" \
  -d '{"database_id":"<database_ulid>"}' \
  https://panel.example.com/api/v1/sso/phpmyadmin

# If response is {"redirect_url":"/phpmyadmin/sso.php?token=..."}, key rotation succeeded.
```

---

## 4. Reconciler control (pause/resume)

Use these commands to halt or resume the in-process reconciler loop (domain sync, DNS zone generation, PHP pool application, and SSO token cleanup).

**Pause reconciler** (e.g., during maintenance):
```bash
jabali-panel admin reconciler pause --token YOUR_ADMIN_JWT
```

**Resume reconciler:**
```bash
jabali-panel admin reconciler resume --token YOUR_ADMIN_JWT
```

**Check pause status:**
```bash
# Look at logs; paused state is reported in reconciliation attempts:
journalctl -u jabali-panel | grep "reconciler paused"
```

---

## 5. Database-user account revocation

If a database user's shadow MySQL account must be revoked (e.g., user deleted, password compromised):

```bash
# Connect to MariaDB
mysql -u root -p

# Drop the shadow account for user 'alice'
DROP USER 'alice_mysqladmin'@'127.0.0.1';
DROP USER 'alice_mysqladmin'@'localhost';
FLUSH PRIVILEGES;

# Verify removal
SELECT User, Host FROM mysql.user WHERE User LIKE '%mysqladmin%';
```

Next time that user requests SSO access, the panel will attempt to re-create their shadow account via `EnsureShadow()` in the `sso.Service`.

---

## 6. Upgrades

### Standard upgrade (`jabali update`)

The release path is the in-tree `jabali update` CLI subcommand. It runs as
root, pulls `origin/main` in `/opt/jabali2`, rebuilds the Go binaries +
the React bundle (as the `jabali` user), syncs assets, runs DB migrations,
and restarts services. Idempotent — safe to re-run if a step fails.

```bash
ssh root@<host>
jabali update
```

Expected output ends with `✓ Update complete.` and exits 0. Service
state after:

```bash
systemctl is-active jabali-panel jabali-agent   # both → "active"
curl -k https://localhost/health                # {"status":"ok"}
```

If it fails, the most common causes:

| Error | Cause | Fix |
|-------|-------|-----|
| `EACCES: permission denied, unlink '/opt/jabali-panel/panel-ui/dist/.gitkeep'` | A previous out-of-band rsync wrote `dist/` as root; vite running as `jabali` can't clear it | `chown -R jabali:jabali /opt/jabali-panel/panel-ui/dist` |
| `Dirty database version N` | Prior migration crashed mid-way | See §1 → Migration recovery |
| `golangci-lint` / `tsc` errors after pull | Upstream regression on `main` | Roll back: `git -C /opt/jabali2 reset --hard <previous-sha>` then `jabali update` |

### Hot binary patch (between releases)

For a quick fix that hasn't hit `main` yet — common during incident
response. Build off-host, scp in, atomic-swap, restart:

```bash
# On dev machine
make build
scp bin/jabali     root@<host>:/usr/local/bin/jabali-panel.new
scp bin/jabali-agent root@<host>:/usr/local/bin/jabali-agent.new

# On the host
mv /usr/local/bin/jabali-panel.new /usr/local/bin/jabali-panel
mv /usr/local/bin/jabali-agent.new /usr/local/bin/jabali-agent
chmod 0755 /usr/local/bin/jabali-panel /usr/local/bin/jabali-agent
systemctl restart jabali-panel jabali-agent
```

UI bundle:

```bash
cd panel-ui && npm run build
rsync -avz --delete --chown=jabali:jabali --rsync-path='sudo rsync' \
    panel-ui/dist/ root@<host>:/opt/jabali-panel/panel-ui/dist/
```

> **Important:** any hot-patched binary is overwritten by the next
> `jabali update` (which rebuilds from `origin/main`). Land the fix on
> `main` before the next planned upgrade or it'll silently regress.

---

## 7. Emergency procedures

### Panel-api is crashing (error loop)

1. Check logs: `journalctl -u jabali-panel -n 100`
2. Common causes:
   - Invalid `DATABASE_URL` — verify in `systemctl show -p Environment jabali-panel`
   - Corrupted database — restore backup
   - Disk full — `df -h /var/log /var/lib/mysql`
3. If unrecoverable, roll back to previous release (see tarball upgrade, step 3 restore).

### Panel-agent is not responding

1. Check socket: `ls -la /run/jabali/agent.sock`
2. Restart agent: `systemctl restart jabali-agent`
3. Check for errors: `journalctl -u jabali-agent -n 50`
4. If socket is missing or stale, manually remove: `rm /run/jabali/agent.sock && systemctl start jabali-agent`

### Database is locked (timeouts)

1. Check active connections: `SHOW PROCESSLIST;`
2. Kill long-running query: `KILL <connection_id>;`
3. If reconciler is stuck, pause it: `jabali-panel admin reconciler pause --token YOUR_ADMIN_JWT`

### phpMyAdmin SSO not working

1. Verify shadow account exists:
   ```sql
   SELECT User, Host FROM mysql.user WHERE User LIKE '%mysqladmin%';
   ```
2. Check sso_key.txt is readable:
   ```bash
   test -r /etc/jabali/sso_key.txt && echo "Key readable" || echo "Key not readable"
   ```
3. Check token table for stale tokens:
   ```sql
   SELECT COUNT(*) FROM phpmyadmin_sso_tokens WHERE expires_at < NOW();
   ```
   If many, the nightly prune may be delayed; manually trigger:
   ```bash
   jabali-panel sso prune-tokens  # if available
   ```
4. Check logs: `journalctl -u jabali-panel | grep sso_phpmyadmin`

---

## 8. SSL certificate lifecycle

Every domain with `ssl_enabled=1` (the default on creation) gets a
certificate, always. The reconciler enforces:

1. **On domain create** — try Let's Encrypt (ACME) once, inline.
2. **On any ACME failure** (LE outage, DNS not pointing at the box yet,
   `server_settings.admin_email` not yet set) — generate a self-signed
   cert in `/etc/ssl/jabali-selfsigned/<domain>/` so HTTPS keeps
   working. The vhost serves whichever cert exists on disk.
3. **Retry ACME every 3 hours** flat — no exponential backoff, no
   max-retry cap. The cert row sits at status `pending_acme_retry`
   with `next_retry_at` set; the SSL ticker (1 min) picks it up at the
   scheduled time.
4. **On ACME success** — status flips to `issued`, the LE cert
   replaces the self-signed in the vhost on the next reconciler tick.

Reference: [ADR-0017](adr/0017-ssl-try-acme-then-selfsigned-with-backoff.md).

### Inspecting cert state

```sql
SELECT d.name, d.ssl_enabled, c.status, c.retry_count,
       c.cert_path IS NOT NULL AS has_cert,
       c.last_error, c.next_retry_at
FROM   domains d LEFT JOIN ssl_certificates c ON c.domain_id = d.id
WHERE  d.name = '<domain>'\G
```

| `c.status` | What it means |
|------------|---------------|
| `pending` | Row created, ticker hasn't picked it up yet (≤1 min). |
| `pending_acme_retry` | One or more ACME attempts have failed; self-signed cert is on disk and serving; will retry at `next_retry_at`. |
| `issued` | Let's Encrypt succeeded; renewal handled by the renewal ticker. |
| `failed` | Operator-only state (set via SQL); no further automatic retries. Reset to `pending` to re-engage the loop. |
| `revoked` | `ssl_enabled` was flipped off; cert files cleared, vhost will drop the 443 server block on next reconciler tick. |

### Force a retry now (don't wait 3h)

```sql
UPDATE ssl_certificates
SET    status = 'pending', retry_count = 0,
       next_retry_at = NULL, last_error = NULL
WHERE  domain_id = '<ULID>';
```

Next SSL ticker tick (≤60s) will attempt ACME.

### "HTTPS is 403 but `/jabali-healthcheck.php` works"

Symptom: a domain serves fine on `http://`, but `https://<domain>/`
returns the panel's default 403. Means the vhost has no 443 server block
because no cert exists. Either the cert row is `pending` (ticker hasn't
fired yet) or `failed` (manual recovery needed — see above).

---

## 8. DNS resolvers (panel-managed `systemd-resolved` drop-in)

The panel's **Server Settings → DNS → DNS Resolvers** card writes a
drop-in at `/etc/systemd/resolved.conf.d/jabali.conf` and restarts
`systemd-resolved`. The installer deliberately does **not** touch DNS;
the host keeps whatever resolver was in place until an admin opts in.

### Where the state lives

- **Panel-managed:** `/etc/systemd/resolved.conf.d/jabali.conf` —
  written atomically (`*.tmp` + rename) by the `system.resolver.set`
  agent command. Contains `[Resolve]` + `DNS=` + optional `Domains=`.
- **Source of truth:** on-disk file. The DB does not store resolvers.
- **GET handler** reports `source: "drop-in"` when the file exists,
  `"none"` otherwise (the UI then shows the form empty).
- **SET handler** is a single transaction: validate → write drop-in →
  `systemctl restart systemd-resolved.service` → poll up to 5s for
  `is-active=active`. On restart failure, the previous drop-in
  content (or absence) is restored and a second restart attempts
  recovery; the API returns 409 `failed_precondition`.

### Revert to unmanaged (remove panel-owned drop-in)

```bash
rm -f /etc/systemd/resolved.conf.d/jabali.conf
systemctl restart systemd-resolved.service
resolvectl status | head -20   # confirm upstream DNS
```

The panel UI will then show `Source: none` and the form will be empty
until the admin saves something new.

### Sanity checks

```bash
# Is the service running?
systemctl is-active systemd-resolved.service

# What's /etc/resolv.conf pointing at? (stub = panel-managed takes effect)
readlink -f /etc/resolv.conf

# What resolved sees right now (upstream + per-link)
resolvectl status

# Our drop-in content
cat /etc/systemd/resolved.conf.d/jabali.conf 2>/dev/null || echo "no drop-in"
```

### Troubleshooting

**"Save Resolvers" returns 409 with `"systemd-resolved restart failed …"`**
- The drop-in was rejected or the service is wedged. Check
  `journalctl -u systemd-resolved.service -n 50`. Common causes:
  syntax error from a manually-edited sibling drop-in, missing
  package (rare — `install.sh` installs it), or a locked DNSSEC
  trust anchor.
- Rollback has already happened; the previous config is back in
  place. Investigate, then retry from the UI.

**Admin saved resolvers but host still resolves via the old server**
- Check `/etc/resolv.conf`: if it's a plain file (not a symlink to
  `stub-resolv.conf`), the OS ignores `systemd-resolved` entirely.
  The installer leaves this untouched by policy. To cut over:
  ```bash
  ln -sf ../run/systemd/resolve/stub-resolv.conf /etc/resolv.conf
  ```

**`resolvectl status` shows `DNS=` from a different drop-in**
- Package-installed drop-ins (NetworkManager, cloud-init, etc.) can
  coexist. `systemd-resolved` concatenates all `*.conf` under
  `resolved.conf.d/`. List them:
  ```bash
  ls -la /etc/systemd/resolved.conf.d/ /run/systemd/resolve/resolv.conf.d/ 2>/dev/null
  ```
  If an operator drop-in is fighting `jabali.conf`, either move it
  aside or widen the panel's `DNS=` line to include its entries.

---

## Appendix: CLI command reference

### Admin commands

- `jabali-panel admin reconciler pause --token JWT` — pause reconciler
- `jabali-panel admin reconciler resume --token JWT` — resume reconciler

### SSO commands

- `jabali-panel sso rotate-key --current-key /path/to/old --new-key /path/to/new` — rotate encryption key
- `jabali-panel sso prune-tokens --max-age 7d` — manually purge expired tokens (normally automatic every 5 min)

### Health check

- `curl https://panel.example.com/health` — returns `{"status":"ok"}` if healthy

