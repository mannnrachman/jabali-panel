# PHP-FPM Pools

Operational runbooks for managing Jabali's per-user PHP-FPM pools.

## A. Adding a new PHP version post-install

### Symptom
You need to support a new PHP version (e.g., 8.4) after initial install.

### Diagnosis
1. Check what versions are currently installed:
   ```bash
   systemctl list-units --type=service --pattern='php*-fpm'
   ```

2. Check Sury's available versions:
   ```bash
   apt-cache search php | grep "^php[0-9]" | awk '{print $1}'
   ```

### Fix
1. Set the env var and re-run install.sh with only PHP install step:
   ```bash
   JABALI_PHP_VERSIONS="7.4 8.2 8.5" bash install.sh
   ```
   The install script reads `JABALI_PHP_VERSIONS` and installs missing versions via `apt`.

2. Verify all FPM services started:
   ```bash
   systemctl status php7.4-fpm php8.2-fpm php8.5-fpm
   ```

3. Verify socket files exist:
   ```bash
   ls /run/php/php8.*.sock
   ```

### Verification
1. Check each service is active:
   ```bash
   systemctl is-active php8.4-fpm
   ```

2. Test FPM responds on the socket:
   ```bash
   echo -e "GET / HTTP/1.0\r\n" | nc -U /run/php/php8.4-fpm.sock
   ```

---

## B. Removing a PHP version

### Symptom
You want to EOL a PHP version (e.g., 8.1 is no longer supported).

### Diagnosis
1. Check if any pools still use this version:
   ```bash
   mysql -u panel -p jabali_panel -e "SELECT COUNT(*) FROM php_pools WHERE php_version = '8.1';"
   ```

### Fix
**Prerequisite:** The query above must return `0`. If pools exist, delete them first via the API.

1. Remove the FPM service and packages:
   ```bash
   systemctl stop php8.1-fpm
   systemctl disable php8.1-fpm
   apt remove php8.1-fpm php8.1-* -y
   ```

2. If this was the last PHP version from Sury, clean up the Sury GPG key and sources list:
   ```bash
   systemctl list-units --type=service --pattern='php*-fpm' | wc -l
   ```
   If the output is 0:
   ```bash
   rm /etc/apt/trusted.gpg.d/deb-sury-org-php.gpg
   rm /etc/apt/sources.list.d/sury-php.list
   apt update
   ```

### Verification
1. Confirm the service is gone:
   ```bash
   systemctl list-units --type=service --pattern='php*-fpm'
   ```

2. Re-verify no pools reference the deleted version:
   ```bash
   mysql -u panel -p jabali_panel -e "SELECT * FROM php_pools WHERE php_version = '8.1';"
   ```

---

## C. Diagnosing a stuck pool

### Symptom
A domain served by a PHP pool returns 502 (Bad Gateway) or 504 (Gateway Timeout); PHP code doesn't execute.

### Diagnosis
1. Check the FPM service is running:
   ```bash
   systemctl status php8.5-fpm  # replace 8.5 with the pool's php_version
   ```

2. Check FPM logs for errors:
   ```bash
   journalctl -u php8.5-fpm -n 100
   ```

3. Check the pool config exists and is valid:
   ```bash
   cat /etc/php/8.5/fpm/pool.d/jabali-<username>.conf
   php-fpm8.5 -t -y /etc/php/8.5/fpm/pool.d/jabali-<username>.conf
   ```

4. Verify the socket is listening:
   ```bash
   ls -la /run/php/php8.5-fpm.sock
   ```

### Fix
**Option 1: Restart the FPM service**
```bash
systemctl restart php8.5-fpm
```

**Option 2: Trigger reconciliation via API**
```bash
curl -X POST http://localhost:8080/api/v1/reconcile/php-pools \
  -H "Authorization: Bearer <admin-token>" \
  -H "Content-Type: application/json"
```

The reconciler will re-write the pool config and reload FPM.

### Verification
1. Check FPM is running again:
   ```bash
   systemctl is-active php8.5-fpm
   ```

2. Test a simple PHP file:
   ```bash
   echo '<?php echo "OK";' > /home/<user>/<domain>/test.php
   curl -sS http://<domain>/test.php
   ```

3. Clean up test file:
   ```bash
   rm /home/<user>/<domain>/test.php
   ```

---

## D. Manual E2E verification

### Symptom
You want to verify that PHP-FPM pool binding works end-to-end without automated tests.

### Diagnosis & Fix
1. Pick a test domain bound to a PHP pool (or create one).

2. Create a PHP info file:
   ```bash
   echo '<?php phpinfo();' > /home/<user>/<domain>/phpinfo.php
   ```

3. Fetch and check the PHP version:
   ```bash
   curl -sS http://<domain>/phpinfo.php | head -20 | grep "PHP Version"
   ```
   Verify the version matches the pool's `php_version` (from the admin panel or `SELECT php_version FROM php_pools WHERE id = '<pool-id>';`).

4. Re-bind the domain to a different PHP pool via the UI or API:
   ```bash
   curl -X POST http://localhost:8080/api/v1/domains/<domain-id>/php-pool \
     -H "Authorization: Bearer <token>" \
     -H "Content-Type: application/json" \
     -d '{"php_version":"<other-version>"}'
   ```

5. Fetch phpinfo again and confirm the new version:
   ```bash
   curl -sS http://<domain>/phpinfo.php | head -20 | grep "PHP Version"
   ```

6. Clean up the test file:
   ```bash
   rm /home/<user>/<domain>/phpinfo.php
   ```

### Verification
The version shown in the phpinfo output changes after rebinding. If it doesn't, check that the nginx vhost config is using the new pool's socket (`systemctl reload nginx` may help).
