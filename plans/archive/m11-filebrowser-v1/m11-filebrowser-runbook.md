# M11 FileBrowser Troubleshooting Runbook

## Overview

This runbook covers how to diagnose and recover from common FileBrowser integration failures in the Jabali Panel M11 feature. FileBrowser is a shared system service that provides web-based file management scoped to user homedirs. Integration uses proxy authentication via nginx + SSO token validation.

---

## Files button opens tab that redirects to login or errors

### Symptoms
- User clicks "Files" in the sidebar
- New tab opens but immediately redirects to panel login page
- Or: 403/401 error page appears
- Or: blank page with no filebrowser UI visible

### Diagnosis

1. Check if filebrowser service is running:
   ```bash
   systemctl status jabali-filebrowser
   # Should show "active (running)"
   ```

2. Check if filebrowser socket is accessible:
   ```bash
   ls -la /run/jabali-filebrowser/fb.sock
   # Should exist with mode 0660 and www-data group readable
   ```

3. Check filebrowser logs for auth errors:
   ```bash
   journalctl -u jabali-filebrowser -n 50
   # Look for "auth" or "proxy" error messages
   ```

4. Check nginx logs for the request:
   ```bash
   journalctl -u nginx -n 50 | grep "/files"
   # Look for 403, 401, or proxy errors
   ```

5. Verify SSO token is being generated:
   ```bash
   # From panel host, query the panel API directly
   curl -XPOST https://localhost:8443/api/v1/sso/filebrowser \
     -H "Authorization: Bearer <admin_token>" \
     -H "Content-Type: application/json" \
     -d '{"user_id": "<user_id>"}'
   # Should return { "redirect_url": "https://..." }
   ```

6. Check if filebrowser user exists for the authenticated user:
   ```bash
   filebrowser --database /var/lib/jabali-filebrowser/filebrowser.db users ls
   # Should show entries for each jabali user
   ```

### Probable Causes & Resolution

**Cause: filebrowser service not running**
```bash
sudo systemctl start jabali-filebrowser
sudo systemctl enable jabali-filebrowser
```

**Cause: Socket permissions incorrect**
```bash
sudo chown root:www-data /run/jabali-filebrowser/fb.sock
sudo chmod 0660 /run/jabali-filebrowser/fb.sock
# Restart service to recreate socket if needed
sudo systemctl restart jabali-filebrowser
```

**Cause: SSO token minting failed**
- Check panel logs: `journalctl -u jabali-panel-agent -n 100`
- Look for database errors in `filebrowser_sso_tokens` table insertion
- Verify panel has write access to its own database: `mysql -u panel_admin -e "SELECT 1 FROM filebrowser_sso_tokens LIMIT 1;"`

**Cause: Filebrowser user doesn't exist (reconciler hasn't converged)**
```bash
# Manually trigger reconciler to create missing users
curl -XPOST https://localhost:8443/api/v1/admin/reconcile \
  -H "Authorization: Bearer <admin_token>"

# Or manually create the user
filebrowser --database /var/lib/jabali-filebrowser/filebrowser.db \
  users add <username> --password <tmp> --scope /home/<username>
```

**Cause: Nginx auth_request subrequest failed**
- Check nginx config: `cat /etc/nginx/conf.d/jabali-files.conf | grep auth_request`
- Ensure the subrequest points to a valid panel endpoint
- Check panel endpoint responds: `curl -v https://localhost:8443/api/v1/sso/filebrowser/validate?token=...`

---

## User can log in but sees empty directory or permission denied

### Symptoms
- Filebrowser loads and shows authenticated
- File browser list is empty
- Or: "Permission denied" error on navigation
- Or: breadcrumb shows `/` but no files visible

### Diagnosis

1. Check filebrowser user scope is correct:
   ```bash
   filebrowser --database /var/lib/jabali-filebrowser/filebrowser.db \
     users show <username>
   # Look for "scope: /home/<username>"
   ```

2. Check if user's homedir exists and has correct permissions:
   ```bash
   ls -la /home/<username>/
   # Should be mode 0750, owned by <username>:<username>
   ```

3. Check if filebrowser process can access the homedir:
   ```bash
   # Run as filebrowser user
   sudo -u filebrowser ls /home/<username>/
   # Should succeed; if permission denied, see below
   ```

4. Check filebrowser process is in the correct groups:
   ```bash
   id filebrowser
   # Should show supplementary groups for each user (e.g., "admin", "user1", etc.)
   ```

5. Check filebrowser logs for permission errors:
   ```bash
   journalctl -u jabali-filebrowser -n 100 | grep -i "permission\|access"
   ```

### Probable Causes & Resolution

**Cause: Filebrowser process not in user's group (reconciler hasn't applied group)**
```bash
# Manually add filebrowser to the user's group
sudo usermod -aG <username> filebrowser

# Restart filebrowser to pick up new group membership
sudo systemctl restart jabali-filebrowser
```

**Cause: User's homedir mode is too restrictive**
```bash
# Ensure homedir is readable by group
sudo chmod 0750 /home/<username>/
sudo chown <username>:<username> /home/<username>/
```

**Cause: Scope path is incorrect in filebrowser DB**
```bash
# Update the scope manually (if user was created before homedir was ready)
filebrowser --database /var/lib/jabali-filebrowser/filebrowser.db \
  users update <username> --scope /home/<username>

# Restart filebrowser
sudo systemctl restart jabali-filebrowser
```

**Cause: Homedir doesn't exist (per-user slice not yet created)**
```bash
# Verify jabali user exists and slice was created
id <username>
ls /run/systemd/user-slice-base/ | grep <username>

# If missing, check M9 per-user slice provisioning
# Trigger reconciler to create slices
curl -XPOST https://localhost:8443/api/v1/admin/reconcile \
  -H "Authorization: Bearer <admin_token>"
```

---

## Upload fails or times out

### Symptoms
- User clicks upload button, selects file
- Upload appears to start then hangs
- Or: "Upload failed" error
- Or: 413 Request Entity Too Large error

### Diagnosis

1. Check filebrowser logs for errors:
   ```bash
   journalctl -u jabali-filebrowser -n 50 | tail
   ```

2. Check nginx `client_max_body_size` setting:
   ```bash
   grep "client_max_body_size" /etc/nginx/conf.d/jabali-files.conf
   # Default should be 4G (4096m)
   ```

3. Check user's disk quota/space:
   ```bash
   df /home/<username>/
   # Check available space

   # If using filesystem quotas:
   quota -u <username>
   ```

4. Check if user's directory is writable:
   ```bash
   sudo -u <username> touch /home/<username>/.test
   # Should succeed; if permission denied, see "User sees permission denied" above
   ```

5. Check filebrowser permissions config for upload:
   ```bash
   # Query filebrowser DB to see user permissions
   filebrowser --database /var/lib/jabali-filebrowser/filebrowser.db \
     users show <username> | grep -i "perm\|allow"
   ```

### Probable Causes & Resolution

**Cause: nginx client_max_body_size too small**
```bash
# Edit nginx config
sudo nano /etc/nginx/conf.d/jabali-files.conf

# Ensure client_max_body_size is at least 4G:
# client_max_body_size 4096m;

# Test config
sudo nginx -t

# Reload
sudo systemctl reload nginx
```

**Cause: User's disk is full or quota exceeded**
```bash
# Check available disk space
df -h /home/

# If quota is set, increase or remove
sudo setquota -u <username> <limit> <limit> 0 0 /

# Or remove quota
sudo setquota -u <username> 0 0 0 0 /
```

**Cause: Upload permission disabled in filebrowser**
```bash
# Check if upload is enabled for the user
filebrowser --database /var/lib/jabali-filebrowser/filebrowser.db \
  users show <username>

# If upload is disabled, re-create user with correct permissions or edit DB
filebrowser --database /var/lib/jabali-filebrowser/filebrowser.db \
  users rm <username>

filebrowser --database /var/lib/jabali-filebrowser/filebrowser.db \
  users add <username> --password tmp --scope /home/<username>

# Restart
sudo systemctl restart jabali-filebrowser
```

---

## Stuck/orphan filebrowser users (user deleted but filebrowser entry remains)

### Symptoms
- A jabali user was deleted via the panel
- Their filebrowser user entry still exists
- Running `filebrowser users ls` shows orphaned entries
- Or: error when trying to recreate user with same name

### Diagnosis

1. List all filebrowser users:
   ```bash
   filebrowser --database /var/lib/jabali-filebrowser/filebrowser.db users ls
   ```

2. Compare against jabali users:
   ```bash
   mysql -u panel_admin -p <panel_db> -e "SELECT username FROM users WHERE deleted_at IS NULL;"
   ```

3. Identify orphaned entries (exist in filebrowser but not in panel `users` table)

### Probable Cause & Resolution

**Cause: Reconciler orphan sweep didn't run or failed**

The reconciler (Step 6) should automatically clean up orphaned filebrowser users. If they persist:

```bash
# Manual cleanup: remove the orphaned user
filebrowser --database /var/lib/jabali-filebrowser/filebrowser.db \
  users rm <orphaned_username>

# Trigger reconciler to ensure consistency
curl -XPOST https://localhost:8443/api/v1/admin/reconcile \
  -H "Authorization: Bearer <admin_token>"

# Verify orphans are gone
filebrowser --database /var/lib/jabali-filebrowser/filebrowser.db users ls
```

---

## Changing filebrowser version or binary update

### Symptoms
- Need to update filebrowser to a newer version (security patch or feature)
- Or: testing a different binary version

### Steps

1. **Update binary SHA-256 pin** in `install/filebrowser/filebrowser-linux-amd64.sha256`:
   ```bash
   # Download new release and compute SHA-256
   wget https://github.com/filebrowser/filebrowser/releases/download/v2.38.1/linux-amd64.tar.gz
   sha256sum linux-amd64.tar.gz
   # Copy the hash into install/filebrowser/filebrowser-linux-amd64.sha256
   ```

2. **Update version number** in `install.sh`:
   ```bash
   # Find the FILEBROWSER_VERSION variable and bump it
   nano install.sh
   # FILEBROWSER_VERSION="v2.38.1"
   ```

3. **Re-run install** on panel host:
   ```bash
   sudo /path/to/install.sh
   # Or: jabali-panel update (if that command wraps install.sh)
   ```

4. **Restart filebrowser service**:
   ```bash
   sudo systemctl restart jabali-filebrowser
   ```

5. **Verify update**:
   ```bash
   filebrowser --version
   ```

**Warning:** Always test binary updates on a staging instance first. Filebrowser is in maintenance mode; major version bumps may change config format or DB schema. Plan for migration if upgrading beyond the pinned version.

---

## Force reconciler to sync NOW (manual trigger)

To manually trigger the reconciler without waiting for the next scheduled tick:

```bash
curl -XPOST https://localhost:8443/api/v1/admin/reconcile \
  -H "Authorization: Bearer <admin_token>" \
  -H "Content-Type: application/json"

# Returns { "status": "ok" }
```

The reconciler will:
- Ensure all jabali users have filebrowser entries
- Ensure filebrowser user group memberships are correct
- Remove filebrowser users that no longer have a jabali account
- Validate socket permissions and config consistency

This is useful after making manual edits to `/var/lib/jabali-filebrowser/filebrowser.db` or group memberships.

---

## Service restart storms with high user counts

### Symptom
- Reconciler restarts filebrowser frequently (multiple times per hour)
- Each restart causes brief (~1s) file manager unavailability
- Happens after adding many users rapidly

### Cause
Each user addition requires `usermod -aG <user> filebrowser`, which requires an `NSS` database refresh. To avoid thundering herd, the reconciler should batch group changes and restart once per tick.

### Mitigation (current)
The reconciler batches group updates: it only restarts filebrowser after all group modifications are complete. Avoid adding 50+ users simultaneously; spread user creation across multiple reconciler ticks (10min apart by default).

### Future mitigation (M11+)
Use `systemd` user slices with `PrivateUsers=yes` or nspawn containers to isolate filebrowser, eliminating the need for OS-level group membership changes.

---

## Quick Reference

| Issue | Command |
|-------|---------|
| Check service status | `systemctl status jabali-filebrowser` |
| View recent logs | `journalctl -u jabali-filebrowser -n 50` |
| List filebrowser users | `filebrowser users ls` |
| Add user manually | `filebrowser users add <user> --scope /home/<user>` |
| Delete user | `filebrowser users rm <user>` |
| Restart service | `systemctl restart jabali-filebrowser` |
| Trigger reconciler | `curl -XPOST https://localhost:8443/api/v1/admin/reconcile` |
| Check socket | `ls -la /run/jabali-filebrowser/fb.sock` |

---

**Last updated:** 2026-04-18
