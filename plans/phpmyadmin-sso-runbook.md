# phpMyAdmin SSO Installation & Smoke Test Runbook

**Date Created:** 2026-04-18  
**Status:** Draft — Step 6 implementation  
**Complexity:** HIGH  
**Audience:** DevOps, system integrators, developers  

## Overview

This runbook documents the installation and smoke testing of phpMyAdmin with SSO
integration via Jabali's panel API. It follows step 6 of the phpMyAdmin SSO plan.

## Prerequisites

- Fresh Debian 13 or Ubuntu 24.04 LXC container or VM
- Root shell access
- Network access to download phpMyAdmin tarball
- Jabali panel already installed (steps 1-5 complete)
- SSO key generated at `/etc/jabali-panel/sso.key`

## Installation Steps

### Step 1: Run install.sh with phpMyAdmin Support

```bash
curl -fsSL https://git.linux-hosting.co.il/shukivaknin/jabali2/raw/branch/main/install.sh \
  | bash -s -- --hostname=panel.example.com
```

This will:
1. Install phpMyAdmin 5.2.3 to `/opt/phpmyadmin/5.2.3/`
2. Symlink `/opt/phpmyadmin/current` → `/opt/phpmyadmin/5.2.3/`
3. Write `config.inc.php` with signon auth enabled
4. Deploy `sso.php` to `/opt/phpmyadmin/current/sso.php`
5. Create nginx location block at `/etc/nginx/sites-available/includes/phpmyadmin.conf`
6. Ensure SSO key exists and is readable by the panel service user

### Step 2: Verify Installation

```bash
# Check phpMyAdmin directory structure
ls -la /opt/phpmyadmin/current/

# Verify symlink
ls -la /opt/phpmyadmin/

# Check config.inc.php exists and is readable
head -20 /opt/phpmyadmin/current/config.inc.php

# Verify sso.php is present
ls -la /opt/phpmyadmin/current/sso.php

# Check nginx includes directory
ls -la /etc/nginx/sites-available/includes/phpmyadmin.conf
```

Expected output:
```
config.inc.php has auth_type = 'signon'
sso.php is readable and contains socket connection logic
phpmyadmin.conf contains nginx location blocks
```

### Step 3: Verify UDS Socket Permissions

The panel-api should have created `/run/jabali/sso.sock` when started.

```bash
# Check socket exists and is writable by www-data (phpMyAdmin runs under www-data in shared pool)
ls -la /run/jabali/sso.sock

# Verify permissions: should be world-readable for phpMyAdmin
stat /run/jabali/sso.sock
```

Expected: Socket exists with `rw-rw----` or similar permissions.

### Step 4: Create a Test Database & User (Optional)

If you want to test the full flow:

```bash
# Log in to MariaDB as root
mariadb -uroot

# Create test database and user
CREATE DATABASE IF NOT EXISTS testdb_sso;
CREATE USER IF NOT EXISTS 'testuser'@'localhost' IDENTIFIED BY 'testpass123';
GRANT ALL PRIVILEGES ON testdb_sso.* TO 'testuser'@'localhost';
FLUSH PRIVILEGES;
EXIT;
```

### Step 5: Verify Panel API is Running

```bash
# Check panel service status
systemctl status jabali-panel

# Test the health endpoint
curl -k https://127.0.0.1:8443/health | jq .

# Expected output: {"status":"ok"}
```

## Smoke Tests

### Test 1: phpMyAdmin Login Page Loads

```bash
# From a browser or curl:
curl -k -I https://panel.example.com:8443/phpmyadmin/

# Expected: HTTP 200 (or 302 redirect if SSL cert issues)
# Look for: index.php or login form
```

### Test 2: SSO Token Generation (Via Panel API)

1. Log into the Jabali panel as an admin
2. Create or select a database owned by the logged-in user
3. Click "phpMyAdmin" or similar SSO link (if UI exists)
4. Expected: Redirects to `/phpmyadmin/sso.php?token=<base64-encoded-token>`

Behind the scenes:
- Panel API calls `POST /api/v1/sso/phpmyadmin` with JWT auth
- Returns redirect URL with signed SSO token
- Browser follows to sso.php

### Test 3: SSO Token Validation (Via sso.php)

1. Manually craft a request to sso.php with a token:

```bash
# First, generate a valid token via the panel API (manually or via UI)
TOKEN="<base64-url-encoded-32-byte-token>"

# Request to sso.php
curl -k -L "https://panel.example.com:8443/phpmyadmin/sso.php?token=${TOKEN}"

# Expected: Redirect to phpMyAdmin index.php?db=<dbname>
# The session should be populated with MySQL credentials
```

### Test 4: Verify Token Is NOT in Nginx Access Log

This is critical for security — SSO tokens must not appear in logs.

```bash
# Make a request with a token
TOKEN="<some-valid-token>"
curl -k "https://panel.example.com:8443/phpmyadmin/sso.php?token=${TOKEN}"

# Check nginx access log
grep "token=" /var/log/nginx/jabali-pma.access.log || echo "✓ No tokens in log (good)"

# Expected output: No matches (token= should NOT appear)
```

If token appears, the log format is not using the `jabali_pma` format.
Check nginx includes configuration:

```bash
grep "log_format jabali_pma" /etc/nginx/sites-available/includes/phpmyadmin.conf
```

### Test 5: Verify SSL Certificate

```bash
# Check that panel vhost is using a valid certificate
curl -k -v https://panel.example.com:8443/phpmyadmin/ 2>&1 | grep "SSL certificate"

# For self-signed: Should see "self signed certificate"
# For Let's Encrypt: Should see issuer info
```

### Test 6: Test with Invalid Token

```bash
# Request with invalid token format
curl -k "https://panel.example.com:8443/phpmyadmin/sso.php?token=invalid"

# Expected: HTTP 400 (Bad Request)
# No HTML body should be returned
```

### Test 7: Test Socket Connectivity

From within the phpMyAdmin container/VM:

```bash
# Simulate sso.php connecting to the socket
php -r '
$sock = stream_socket_client("unix:///run/jabali/sso.sock", $errno, $errstr, 5);
if ($sock === false) {
    echo "ERROR: " . $errstr . "\n";
    exit(1);
}
echo "✓ Socket connection succeeded\n";
fclose($sock);
'
```

Expected: `✓ Socket connection succeeded`

## Troubleshooting

### phpMyAdmin Returns 404

- Verify symlink: `ls -la /opt/phpmyadmin/current/`
- Check nginx location block is included in panel vhost config
- Check nginx syntax: `nginx -t`
- Reload nginx: `systemctl reload nginx`

### sso.php Returns 400

- Check token format validation: Token should be base64url, 32-128 chars
- Verify panel-api is running and socket exists: `ls -la /run/jabali/sso.sock`
- Check firewall/permissions allow www-data to access socket
- Test socket connectivity manually (see Test 7 above)

### Token Appears in Nginx Access Log

- The log format must use the `jabali_pma` format that redacts query strings
- Check `/etc/nginx/sites-available/includes/phpmyadmin.conf` has the correct log_format
- Reload nginx after changes: `systemctl reload nginx`
- Verify with: `grep "args=\[REDACTED\]" /var/log/nginx/jabali-pma.access.log`

### SSO Socket Not Found or Not Writable

- Verify panel-api created the socket: `ls -la /run/jabali/sso.sock`
- Check socket permissions allow www-data access
- If missing, restart panel-api: `systemctl restart jabali-panel`
- Check panel-api logs: `journalctl -u jabali-panel -n 50`

### phpMyAdmin Config Mismatch

- If structure has changed in a newer phpMyAdmin version:
  1. Extract the tarball manually
  2. Check `examples/signon-script.php`
  3. Update `sso.php` and `config.inc.php` to match
  4. Re-run install.sh (it will update the files)

## Performance Notes

- phpMyAdmin runs under the domain owner's PHP-FPM pool (per M9 design)
- No separate `jabali-pma` pool is needed
- First request may be slow (phpMyAdmin initialization); subsequent requests cache
- Session state is stored in `/tmp/` (configurable via `$cfg['SessionSavePath']`)

## Security Notes

1. **Tokens in logs:** Nginx log format must use `jabali_pma` to redact query strings
2. **Session storage:** Tokens are one-time use (consumed by validate handler)
3. **SSL/TLS:** All connections must use HTTPS; plain HTTP will expose tokens
4. **Cookie flags:** Session cookies are `secure`, `httponly`, `samesite=Lax`
5. **Socket access:** UDS socket must be owned by panel user, readable by www-data

## Rollback

To remove phpMyAdmin:

```bash
# Stop services
systemctl reload nginx

# Remove files
rm -rf /opt/phpmyadmin
rm -f /etc/nginx/sites-available/includes/phpmyadmin.conf
rm -f /var/log/nginx/jabali-pma.access.log /var/log/nginx/jabali-pma.error.log

# Reload nginx to remove location block
systemctl reload nginx
```

## Next Steps

- Monitor logs for issues: `tail -f /var/log/nginx/jabali-pma.error.log`
- Track phpMyAdmin version updates for security patches
- Plan for step 7 (Nginx domain vhost template integration)
- Plan for step 8 (Panel UI SSO button integration)
