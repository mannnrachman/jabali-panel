# M10 WordPress Troubleshooting Runbook

## Overview

This runbook covers how to diagnose and recover from common WordPress install, clone, and delete failures in the Jabali Panel M10 feature.

---

## Install stuck in `installing` for >5 minutes

### Symptoms
- Install row shows status `installing` even after 5+ minutes
- No activity visible in agent logs

### Diagnosis
1. SSH to the panel host and check the agent log:
   ```bash
   journalctl -u jabali-panel-agent -f
   # Look for ERROR or WARN lines mentioning the domain
   ```

2. Check if `wp-cli` is installed and available:
   ```bash
   /usr/local/bin/wp --info
   # Should output version, PHP version, and MySQL info
   ```

3. Check the panel database for the install record:
   ```bash
   mysql -u panel_admin -p <panel_db> -e "
   SELECT id, domain_id, status, last_error, created_at, updated_at 
   FROM wordpress_installs 
   WHERE status='installing' 
   ORDER BY updated_at DESC LIMIT 5;
   "
   ```

4. Check if the domain's docroot exists and is writable by the OS user:
   ```bash
   ls -la /home/<user>/domains/<domain>/public_html/
   # Should exist and be owned by <user>
   ```

5. Check panel agent's connectivity to MariaDB:
   ```bash
   mysql -u panel_agent -p <db_host> -e "SELECT 1;"
   # Should return 1
   ```

### Resolution
- **wp-cli missing:** Run `install.sh` step 2 (wp-cli provisioning) on the panel host
- **Docroot permissions wrong:** Run `systemd-run --uid=<user> --slice=jabali-user-<user>.slice -- chmod 755 /home/<user>/domains/<domain>/public_html`
- **Agent log shows timeout:** Check network connectivity and MariaDB availability; restart the agent if needed: `systemctl restart jabali-panel-agent`
- **Agent log shows wp-cli error:** Continue to next section ("Install fails with error")

---

## Install fails with error (db_create_failed, db_user_create_failed, wp_cli_install_failed)

### Symptoms
- Install row shows status `failed`
- `last_error` column contains a truncated error message (up to 1024 chars)

### Diagnosis
1. Check the `last_error` column for clues:
   ```bash
   mysql -u panel_admin -p <panel_db> -e "
   SELECT id, domain_id, last_error 
   FROM wordpress_installs 
   WHERE status='failed' 
   ORDER BY updated_at DESC LIMIT 1;
   "
   ```

2. Check the full agent log around the failure timestamp:
   ```bash
   journalctl -u jabali-panel-agent --since "2026-04-18 10:00:00" --until "2026-04-18 10:10:00" | grep -A 5 -B 5 "wordpress"
   ```

3. For database creation failures, check if the database already exists:
   ```bash
   mysql -u panel_admin -p <panel_db> -e "
   SELECT id, name 
   FROM databases 
   WHERE user_id = '<user_id>' 
   ORDER BY created_at DESC LIMIT 5;
   "
   ```

4. For wp-cli install failures, manually test wp-cli on the host:
   ```bash
   systemd-run --uid=<user> --slice=jabali-user-<user>.slice -- \
     /usr/local/bin/wp core version --path=/home/<user>/domains/<domain>/public_html
   # Should return a version string
   ```

### Resolution
- **Database creation failed:** Check if another install is using the same DB; verify MariaDB is accepting connections and disk space is available
- **wp-cli error (PHP / MySQL connectivity):** Ensure target PHP version and MariaDB pool are running:
  ```bash
  systemctl status jabali-php-fpm@<version>.service
  systemctl status mariadb.service
  ```
- **wp-cli error (permissions):** Check that the OS user owns the docroot and can write to it:
  ```bash
  systemd-run --uid=<user> --slice=jabali-user-<user>.slice -- \
    touch /home/<user>/domains/<domain>/public_html/.test
  # If this fails, the user lacks write permission
  ```

---

## Row stuck in `deleting` status

### Symptoms
- Delete was triggered but the row never disappears
- Status remains `deleting` for >5 minutes

### Diagnosis
1. Check agent logs for delete operation errors:
   ```bash
   journalctl -u jabali-panel-agent | grep -A 5 "wordpress.delete"
   ```

2. Check if the docroot still exists:
   ```bash
   ls -la /home/<user>/domains/<domain>/public_html/ 2>&1
   ```

3. Check if the DB is still present:
   ```bash
   mysql -u panel_admin -p <panel_db> -e "
   SELECT name FROM databases 
   WHERE id IN (SELECT db_id FROM wordpress_installs WHERE id='<install_id>');
   "
   ```

### Resolution (manual recovery)
1. **Force-fail the install row** to prevent endless retry:
   ```bash
   mysql -u panel_admin -p <panel_db> -e "
   UPDATE wordpress_installs 
   SET status='failed', last_error='manually failed during delete cleanup'
   WHERE id='<install_id>';
   "
   ```

2. **Manually clean up the docroot** (WordPress files only; don't rm the directory):
   ```bash
   systemd-run --uid=<user> --slice=jabali-user-<user>.slice -- \
     rm -rf /home/<user>/domains/<domain>/public_html/{wp-*.php,wp-admin,wp-content,wp-includes,readme.html,license.txt,index.php}
   ```

3. **Delete the associated database** via the panel's Databases page or CLI:
   ```bash
   mysql -u panel_admin -p <panel_db> -e "
   DELETE FROM databases 
   WHERE id IN (SELECT db_id FROM wordpress_installs WHERE id='<install_id>');
   "
   ```

4. **Manually delete the install row:**
   ```bash
   mysql -u panel_admin -p <panel_db> -e "
   DELETE FROM wordpress_installs WHERE id='<install_id>';
   "
   ```

---

## Reconciler drift detection

The reconciler runs a WordPress install sweeper that detects and fails rows stuck in transitional states (`installing`, `cloning`, `deleting`) beyond a configured threshold (typically 10 minutes).

### Manual trigger
To manually trigger reconciler drift detection without waiting:
```bash
curl -XPOST https://jabali-panel.local:8443/api/v1/admin/reconcile \
  -H "Authorization: Bearer <admin_token>" \
  -H "Content-Type: application/json"
# Returns { "status": "ok" }
```

### What the sweeper does
- Finds all WordPress installs where `status IN ('installing', 'cloning', 'deleting')`
- Checks if `updated_at` is older than the threshold
- Transitions those rows to `failed` with `last_error = "reconciler drift: stuck in <status> for >10min"`
- Does NOT clean up files or databases (that's the user's responsibility via retry or manual cleanup)

### Recovery after drift detection
Once a row is marked `failed` by the reconciler, the user can:
1. Retry the operation (delete and re-do install/clone)
2. Manually clean up via the runbook's "stuck in deleting" section

---

## Clone src/dst PHP version mismatch (CURRENT LIMITATION)

### Symptoms
- Clone operation fails or succeeds but cloned WordPress doesn't load
- Both domains' PHP pools may exist but target different PHP versions

### Note
Currently (M10 Wave E) this is not enforced in the API or UI. The clone operation uses `wp-cli` from the agent host, which runs whatever PHP version the agent has installed.

### Workaround
1. Check both source and destination domains' PHP pool assignments:
   ```bash
   mysql -u panel_admin -p <panel_db> -e "
   SELECT d.name, p.version 
   FROM domains d 
   LEFT JOIN php_pools p ON d.php_pool_id = p.id 
   WHERE d.name IN ('<domain_a>', '<domain_b>');
   "
   ```

2. If versions differ, re-assign both domains to the same PHP pool before cloning:
   - Go to Admin > Domains
   - Edit Domain A and Domain B
   - Set both to the same PHP pool (typically the default)
   - Retry clone

A follow-up ticket (M10.1) will add explicit version checking in the clone handler to reject mismatched pools.

---

## Orphaned database from failed install (CURRENT LIMITATION)

### Symptoms
- Install fails mid-way, leaving a database stranded
- User cannot retry on the same domain because the DB already exists
- The stranded DB is not automatically cleaned up

### Note
This is by design (M10 Open Question #3). Orphaned DBs must be manually deleted by the user.

### Resolution
1. Go to the panel's Databases page
2. Find the orphaned DB (typically named `wp_<ULID[:6]>`)
3. Click Delete
4. Confirm

After deleting the stranded DB, the user can retry the WordPress install on the same domain (or choose a new domain + DB pair).

---

## Health check endpoint

The API exposes a health check for each WordPress install:

```bash
curl -X POST https://jabali-panel.local:8443/api/v1/wordpress/<install_id>/health \
  -H "Authorization: Bearer <token>" \
  -H "Content-Type: application/json"
# Returns { "status": "ok" } if the domain HTTP 200s and wp-cli reports healthy
# Returns { "status": "down", "reason": "..." } if either check fails
```

This can be used in monitoring or status dashboards to detect downed WordPress instances before users notice.

---

## Common patterns for debugging

### Check all WordPress installs for a user
```bash
mysql -u panel_admin -p <panel_db> -e "
SELECT 
  i.id, 
  d.name AS domain, 
  i.status, 
  i.version,
  i.last_error, 
  i.created_at 
FROM wordpress_installs i 
JOIN domains d ON i.domain_id = d.id 
WHERE i.user_id = '<user_id>' 
ORDER BY i.updated_at DESC;
"
```

### List all failed installs across all users (for reconciliation)
```bash
mysql -u panel_admin -p <panel_db> -e "
SELECT 
  i.id, 
  u.email, 
  d.name, 
  i.last_error, 
  i.updated_at 
FROM wordpress_installs i 
JOIN users u ON i.user_id = u.id 
JOIN domains d ON i.domain_id = d.id 
WHERE i.status = 'failed' 
ORDER BY i.updated_at DESC;
"
```

### Manually trigger nginx reconfiguration after a domain's PHP pool changes
```bash
curl -XPOST https://jabali-panel.local:8443/api/v1/admin/reconcile \
  -H "Authorization: Bearer <admin_token>" \
  -H "Content-Type: application/json"
```

---

## Escalation

If none of the above resolve the issue:

1. **Gather logs** from the last 30 minutes:
   ```bash
   journalctl -u jabali-panel-agent --since "30 min ago" > /tmp/agent.log
   journalctl -u jabali-panel --since "30 min ago" > /tmp/panel.log
   mysql -u panel_admin -p <panel_db> -e "
     SELECT * FROM wordpress_installs WHERE updated_at > DATE_SUB(NOW(), INTERVAL 30 MINUTE);
   " > /tmp/installs.txt
   ```

2. **Check reconciler state**:
   ```bash
   curl https://jabali-panel.local:8443/api/v1/admin/reconcile-status \
     -H "Authorization: Bearer <admin_token>" \
     -H "Content-Type: application/json"
   # Shows last reconciliation timestamp and any current errors
   ```

3. **Report** with all logs, the relevant install row from the database, and the domain's docroot listing.
