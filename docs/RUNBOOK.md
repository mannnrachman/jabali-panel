# Jabali Panel Runbook

Operational procedures for managing the panel in production.

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

---

## 2. Service restart & logs

### Restart panel-api

```bash
systemctl restart jabali-panel-api
journalctl -u jabali-panel-api -f  # tail logs
```

### Restart panel-agent

```bash
systemctl restart jabali-agent
journalctl -u jabali-agent -f
```

### Check all services

```bash
systemctl status jabali-panel-api jabali-agent mariadb nginx
```

---

## 3. phpMyAdmin SSO key rotation

**Purpose:** Rotate the AES-256-GCM key used to encrypt database-user passwords stored in shadow MySQL accounts. This is a sensitive operation and requires careful sequencing.

**Prerequisites:**
- Panel is running normally (`systemctl status jabali-panel-api`)
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
   systemctl kill -s SIGHUP jabali-panel-api
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
journalctl -u jabali-panel-api | grep "reconciler paused"
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

## 6. Tarball upgrade (panel-api, panel-agent, panel-ui)

To roll out a new release tarball:

1. **Download and verify tarball:**
   ```bash
   curl -O https://releases.example.com/jabali-panel-2026-04-18.tar.gz
   tar -tzf jabali-panel-2026-04-18.tar.gz | head  # inspect contents
   ```

2. **Stop services:**
   ```bash
   systemctl stop jabali-panel-api jabali-agent
   ```

3. **Backup current installation:**
   ```bash
   tar -czf /var/backups/jabali-panel-$(date +%Y%m%d_%H%M%S).tar.gz \
     /opt/jabali-panel /etc/jabali
   ```

4. **Extract tarball:**
   ```bash
   cd /tmp
   tar -xzf jabali-panel-2026-04-18.tar.gz
   cd jabali-panel-2026-04-18
   ```

5. **Run pre-flight checks:**
   ```bash
   # Validate binaries
   ./bin/panel-api --version
   ./bin/panel-agent --version
   
   # Run migrations (in dry-run mode if supported)
   DATABASE_URL="mysql://root:password@127.0.0.1:3306/jabali_panel" \
     ./bin/panel-api --dry-run-migrations
   ```

6. **Install new binaries and assets:**
   ```bash
   cp ./bin/panel-api /opt/jabali-panel/bin/
   cp ./bin/panel-agent /opt/jabali-agent/bin/
   cp -r ./public/* /opt/jabali-panel/public/
   ```

7. **Apply database migrations (if any):**
   ```bash
   DATABASE_URL="mysql://root:password@127.0.0.1:3306/jabali_panel" \
     /opt/jabali-panel/bin/panel-api --apply-migrations
   ```

8. **Restart services:**
   ```bash
   systemctl start jabali-panel-api jabali-agent
   ```

9. **Verify:**
   ```bash
   systemctl status jabali-panel-api jabali-agent
   curl https://panel.example.com/health  # should return 200
   ```

---

## 7. Emergency procedures

### Panel-api is crashing (error loop)

1. Check logs: `journalctl -u jabali-panel-api -n 100`
2. Common causes:
   - Invalid `DATABASE_URL` — verify in `systemctl show -p Environment jabali-panel-api`
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
4. Check logs: `journalctl -u jabali-panel-api | grep sso_phpmyadmin`

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

