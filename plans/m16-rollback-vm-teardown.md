# M16 Rollback: Hydra VM Teardown Runbook

**Date:** 2026-04-21  
**Related:** ADR-0038 (M16 rollback rationale), `docs/adr/0036-m16-hydra-identity.md` (original M16 decision)

This runbook provides operator-facing commands to fully remove the M16 Ory Hydra service from a production host. All commands are idempotent and safe to re-run. No data loss occurs because Hydra's state (consent sessions, access/refresh tokens) is ephemeral; permanent state (per-install OIDC clients) is duplicated in the panel database.

## Prerequisites

- SSH access to the host as a user with `sudo` privileges
- Bash 4.0+
- Standard Unix utilities: `systemctl`, `rm`, `mysql`, `wp` (for WordPress CLI)

## Step 1: Stop and disable the Hydra systemd unit

```bash
sudo systemctl stop jabali-hydra
sudo systemctl disable jabali-hydra
```

**Verification:**
```bash
systemctl is-enabled jabali-hydra  # Should output: disabled (exit 1)
systemctl is-active jabali-hydra   # Should output: inactive (exit 3)
```

## Step 2: Remove Hydra systemd unit and reload daemon

```bash
sudo rm /etc/systemd/system/jabali-hydra.service
sudo systemctl daemon-reload
```

**Verification:**
```bash
systemctl status jabali-hydra 2>&1 | grep -q "No such file"  # Should match (unit not found)
```

## Step 3: Remove Hydra binary

```bash
sudo rm -f /usr/local/bin/hydra
```

**Verification:**
```bash
which hydra  # Should fail with exit code 1 (not found)
```

## Step 4: Remove Hydra state directory and SQLite database

```bash
sudo rm -rf /var/lib/jabali-hydra
```

**Verification:**
```bash
[ ! -d /var/lib/jabali-hydra ] && echo "OK" || echo "FAIL"  # Should output OK
```

## Step 5: Remove Hydra configuration file

```bash
sudo rm -f /etc/jabali-panel/hydra.yml
```

**Verification:**
```bash
[ ! -f /etc/jabali-panel/hydra.yml ] && echo "OK" || echo "FAIL"  # Should output OK
```

## Step 6: Remove Hydra secrets directory (if present from setup)

```bash
sudo rm -rf /etc/jabali-panel/hydra-secrets
```

**Verification:**
```bash
[ ! -d /etc/jabali-panel/hydra-secrets ] && echo "OK" || echo "FAIL"  # Should output OK
```

## Step 7: Deactivate WordPress OpenID Connect Generic plugin on all installs

For each WordPress install that has the OpenID Connect Generic plugin active:

```bash
# Find all WordPress installs
for install_path in /home/*/wordpress /var/www/html/*/wordpress; do
  if [ -f "$install_path/wp-load.php" ]; then
    echo "Deactivating OpenID plugin at: $install_path"
    sudo wp plugin deactivate daggerhart-openid-connect-generic --path="$install_path" --allow-root 2>/dev/null || true
  fi
done
```

**Alternative (per-install):** If you know the install path:
```bash
sudo wp plugin deactivate daggerhart-openid-connect-generic --path=/home/user/wordpress --allow-root
```

**Verification:**
```bash
sudo wp plugin is-active daggerhart-openid-connect-generic --path=/home/user/wordpress --allow-root
# Should exit with code 1 (plugin inactive) after deactivation
```

## Step 8: Apply migration 000051 to drop OIDC columns

```bash
sudo -u jabali jabali migrate up
```

**Verification:**
```bash
sudo mariadb -e "DESCRIBE jabali.application_installs;" | grep -E "oidc_client_id|oidc_client_secret_enc"
# Should return no rows (columns dropped)
```

## Step 9: (Optional) Clean up stale Hydra MariaDB schema (if host was on Waves A–E with MariaDB path)

If the host previously used Hydra with MariaDB before the SQLite migration (commit d757e29), clean up the orphaned schema:

```bash
sudo mariadb -e "DROP DATABASE IF EXISTS jabali_hydra; DROP USER IF EXISTS 'jabali_hydra'@'localhost'; FLUSH PRIVILEGES;"
sudo rm -f /etc/jabali-panel/hydra-db-password
```

**Verification:**
```bash
sudo mariadb -e "USE jabali_hydra; SHOW TABLES;" 2>&1 | grep -q "Unknown database"  # Should match
```

## Production-Only Checks

If running on production, confirm that no other systems depend on Hydra before proceeding:

```bash
# Check for lingering Hydra network listeners
sudo ss -ltnp | grep -i hydra

# Check for Hydra references in nginx config
sudo grep -r "127.0.0.1:4444\|127.0.0.1:4445" /etc/nginx/

# Check system logs for Hydra errors post-removal (optional, for audit)
sudo journalctl -u jabali-hydra -n 20  # Last 20 lines (will show "unit not found" after removal)
```

All checks should return empty or "not found" results.

## Verification Checklist

Run after completing all steps:

```bash
# Confirm Hydra is fully removed
echo "=== Hydra removal checks ==="
[ ! -f /usr/local/bin/hydra ] && echo "✓ Binary removed" || echo "✗ Binary still exists"
[ ! -d /var/lib/jabali-hydra ] && echo "✓ State dir removed" || echo "✗ State dir still exists"
[ ! -f /etc/jabali-panel/hydra.yml ] && echo "✓ Config removed" || echo "✗ Config still exists"
systemctl is-enabled jabali-hydra 2>&1 | grep -q "disabled\|not-found" && echo "✓ Unit disabled" || echo "✗ Unit still enabled"

# Confirm OIDC columns are dropped
echo "=== OIDC column removal check ==="
sudo mariadb -e "DESCRIBE jabali.application_installs;" 2>&1 | grep -q "oidc_client_id" && echo "✗ OIDC columns still present" || echo "✓ OIDC columns removed"

# Confirm WordPress plugins are deactivated
echo "=== WordPress plugin check ==="
for install_path in /home/*/wordpress /var/www/html/*/wordpress; do
  if [ -f "$install_path/wp-load.php" ]; then
    if sudo wp plugin is-active daggerhart-openid-connect-generic --path="$install_path" --allow-root 2>/dev/null; then
      echo "✗ Plugin active at $install_path"
    else
      echo "✓ Plugin inactive at $install_path"
    fi
  fi
done
```

## Rollback to M22 Magic-Link

Once Hydra is removed, the panel will use M22 magic-link for WordPress SSO. Verify the magic-link plugin is installed:

```bash
sudo wp plugin list --path=/home/user/wordpress --allow-root | grep -i magic
# Should show the magic-link plugin (activated)
```

If magic-link is not present, panel-ui will automatically offer a one-click install on the Applications page.

## Troubleshooting

### Issue: "systemctl status jabali-hydra" shows running after stopping

**Resolution:** The unit may have restarted due to a systemd dependency or a supervisor. Verify all related services:

```bash
sudo systemctl list-dependencies --all | grep -i hydra
sudo ps aux | grep -i hydra | grep -v grep
```

Stop any lingering processes and disable any supervisors that may have auto-restarted it.

### Issue: "jabali migrate up" fails with schema errors

**Resolution:** Ensure migration 000051 exists and is properly formatted. Check:

```bash
ls -la /path/to/migrations/000051*
```

If migration is missing or malformed, contact the jabali team with the full error output.

### Issue: WordPress still shows OIDC settings after plugin deactivation

**Resolution:** WordPress caches plugin configuration. Clear the WordPress object cache:

```bash
sudo wp cache flush --path=/home/user/wordpress --allow-root
```

Then verify the plugin is inactive and the settings are cleared.

## Verification After Teardown

All WordPress installs should still be accessible via classic `/wp-login` after Hydra removal. SSO will be unavailable until M22 magic-link is rolled in (follow-up release). Test a login to confirm fallback works:

```bash
# From a browser: https://yourhost/wp-admin on a WordPress install
# Expected: login form loads (no OIDC provider visible)
# Expected: username/password login works
```

## Related Docs

- **ADR-0038** — M16 rollback decision rationale  
- **ADR-0036** — M16 original decision (now superseded)  
- **docs/adr/0022-m22-magic-link.md** — M22 magic-link design (replacement)  
- **plans/archive/m16-hydra-oauth.md** — Original M16 16-decision design matrix (archived)  
- **plans/archive/m16-hydra-runbook.md** — Original Hydra operations manual (archived)
