# M22 Magic-Link Runbook

## What This Is

Magic-link admin login allows panel users and operators to log directly into WordPress admin dashboards without handling passwords. Clicking "Log in to admin" in the applications table generates a single-use, 60-second token and opens the WordPress site with the query parameter `?jabali_admin_login=<token>`, where a must-use plugin (installed via M22 Step 8) verifies the token against the database and logs the user in automatically.

## Where the Key Lives

The HMAC-SHA256 signing key is stored at:
```
/etc/jabali-panel/magic-link.key
```

File format: comma-separated base64url-encoded keys (32 bytes each), one per line for key rotation support.

File permissions: `0600 root:jabali` (readable only by root and the jabali-panel user)

Example:
```
Wq8zy3bRO2d7kqU9VxL4HpM2nR9sT3vY5q8X2w1Z4a5=,Wq8zy3bRO2d7kqU9VxL4HpM2nR9sT3vY5q8X2w1Z4b5=
```

## Provisioning

### Step 1: Generate and Install the Key

Run the idempotent setup command:
```bash
sudo /usr/local/bin/install_magic_link_key
```

This command:
- Generates a random 32-byte base64url key if the file doesn't exist
- Sets correct ownership and permissions
- Validates the key format
- Outputs the key fingerprint (first 8 chars) for verification

**Output example:**
```
Magic-link key provisioned at /etc/jabali-panel/magic-link.key
Key fingerprint: Wq8zy3bR
Permissions: 600 root:jabali
```

### Step 2: Restart the Panel Service

After provisioning, restart the panel to load the key:
```bash
sudo systemctl restart jabali-panel
```

Verify the service started successfully:
```bash
sudo systemctl status jabali-panel
```

## How to Revoke Tokens

If tokens need to be revoked immediately (e.g., unauthorized login attempt detected):

```bash
sudo mysql -u jabali -p jabali_panel -e "UPDATE magic_link_tokens SET used_at = NOW() WHERE used_at IS NULL;"
```

This marks all unused tokens as used, preventing them from being redeemed. Already-used tokens (with `used_at` set) are unaffected.

To revoke tokens for a specific install:
```bash
sudo mysql -u jabali -p jabali_panel -e "
UPDATE magic_link_tokens 
SET used_at = NOW() 
WHERE used_at IS NULL AND install_id = '<install-uuid>';
"
```

## How to Rotate Keys

Key rotation is done without downtime. The system supports multiple keys in the key file simultaneously.

### Step 1: Generate a New Key

```bash
openssl rand -base64 32 | tr '+/' '-_' | tr -d '=' > /tmp/new_key.txt
```

### Step 2: Append to the Key File

```bash
# Read the new key
NEW_KEY=$(cat /tmp/new_key.txt)

# Append it to the key file
echo "," >> /etc/jabali-panel/magic-link.key
echo -n "$NEW_KEY" >> /etc/jabali-panel/magic-link.key

# Verify
cat /etc/jabali-panel/magic-link.key
```

### Step 3: Restart the Panel

```bash
sudo systemctl restart jabali-panel
```

The new key is now active. Old tokens signed with the previous key will still validate during the 60-second window.

### Step 4: Remove Old Key After TTL

After 60 seconds (the maximum token lifetime), remove the old key(s) from the file:

```bash
# Edit the file to remove the old key (keep only the new one)
sudo nano /etc/jabali-panel/magic-link.key

# Verify only the new key remains
cat /etc/jabali-panel/magic-link.key

# No restart needed; the old key is not consulted
```

## Emergency: Key Compromise

If the key file is exposed or compromised:

1. **Immediately revoke all tokens:**
   ```bash
   sudo mysql -u jabali -p jabali_panel -e "UPDATE magic_link_tokens SET used_at = NOW();"
   ```

2. **Delete the compromised key:**
   ```bash
   sudo rm /etc/jabali-panel/magic-link.key
   ```

3. **Regenerate and install a new key:**
   ```bash
   sudo /usr/local/bin/install_magic_link_key
   sudo systemctl restart jabali-panel
   ```

4. **Audit the logs** for unauthorized login attempts in the past 60 seconds:
   ```bash
   sudo tail -100 /var/log/jabali-panel/access.log | grep "jabali_admin_login"
   ```

5. **Notify users** if unauthorized logins were detected.

## Common Triage

### Button Is Missing for WordPress Install

1. **Check install status:** The button only appears for installations with status "ready". Check the install status in the panel.
   ```bash
   sudo mysql -u jabali -p jabali_panel -e "SELECT id, domain_name, status FROM applications WHERE app_type = 'wordpress';"
   ```

2. **Check app_type:** Ensure the install has `app_type = 'wordpress'` (not NULL or a different type):
   ```bash
   sudo mysql -u jabali -p jabali_panel -e "SELECT id, app_type FROM applications WHERE domain_name = '<domain>';"
   ```

3. **Verify the must-use plugin is installed:** The WordPress site must have the magic-link MU plugin:
   ```bash
   sudo ls -la /var/www/jabali-<install-id>/wp-content/mu-plugins/jabali-magic-link.php
   ```

4. **Check firewall rules:** If the button is visible but clicking it fails, verify the application domain is accessible:
   ```bash
   curl -s -o /dev/null -w "%{http_code}" "https://<domain>/"
   ```

### Button Click Shows Error

1. **Check the API endpoint:** Verify the endpoint is responding:
   ```bash
   curl -s -H "Authorization: Bearer <token>" "https://panel.example.com/api/v1/applications/<install-id>/magic-link" -X POST
   ```

2. **Check panel logs:**
   ```bash
   sudo journalctl -u jabali-panel -n 50
   ```

3. **Verify the install exists and is ready:**
   ```bash
   sudo mysql -u jabali -p jabali_panel -e "SELECT id, status FROM applications WHERE id = '<install-id>';"
   ```

### Token Validation Fails on WordPress

1. **Check the WordPress logs:**
   ```bash
   sudo tail -50 /var/www/jabali-<install-id>/wp-content/debug.log | grep -i "magic"
   ```

2. **Verify the token is not expired:** Tokens are valid for 60 seconds. If more than 60 seconds have passed since the link was generated, it will fail.

3. **Check database sync:** Ensure the token was written to the database:
   ```bash
   sudo mysql -u jabali -p jabali_panel -e "SELECT id, token_id, used_at, created_at FROM magic_link_tokens ORDER BY created_at DESC LIMIT 5;"
   ```

4. **Verify the plugin can read the database:** The MU plugin must have database credentials for `jabali_panel`:
   ```bash
   sudo cat /var/www/jabali-<install-id>/wp-config.php | grep DB_
   ```

### HTTP 410 Gone on Double-Click

If a user clicks "Log in to admin" twice in quick succession or clicks a used token link:

1. The first click generates a token and opens the login window
2. The token is marked as `used_at` after the first successful login
3. The second click (or refresh of the first window) returns HTTP 410 Gone

This is expected and correct. Inform the user to use "Log in to admin" again to generate a fresh token.

### Token Table Growing Unbounded

Tokens are inserted but never deleted (they're marked as used instead). To clean up old tokens (>7 days old):

```bash
sudo mysql -u jabali -p jabali_panel -e "DELETE FROM magic_link_tokens WHERE created_at < DATE_SUB(NOW(), INTERVAL 7 DAY);"
```

Add this to a daily cron job:
```bash
# /etc/cron.d/jabali-magic-link-cleanup
0 2 * * * root mysql -u jabali -p<password> jabali_panel -e "DELETE FROM magic_link_tokens WHERE created_at < DATE_SUB(NOW(), INTERVAL 7 DAY);"
```

## Monitoring

Monitor these metrics in production:

1. **Failed token generations:** Check the panel logs for `POST /api/v1/applications/{id}/magic-link 5xx` errors
2. **Token validation failures:** Check WordPress logs for "magic-link" errors
3. **Plugin installation:** Verify the MU plugin is present on all WordPress installs via install hooks

Add alerting for:
- Panel service is down: `systemctl is-active --quiet jabali-panel || alert`
- Key file missing: `test -f /etc/jabali-panel/magic-link.key || alert`
- Excessive token generation: More than 100 tokens in 1 minute (possible brute-force attempt)
