# M12 SFTP Troubleshooting Runbook

## Overview

This runbook covers how to diagnose and recover from common SFTP integration failures in the Jabali Panel M12 feature. SFTP access is restricted to users in the `jabali-sftp` Linux group via an openssh `Match Group` directive. SSH public keys are managed through the panel and synced to `~/.ssh/authorized_keys` on the panel agent.

---

## User cannot authenticate via SFTP

### Symptoms
- User clicks "Add Key" in panel, adds a public key, but SFTP login fails with "Permission denied (publickey)"
- Or: SSH key-based login is rejected even though the key was added to the panel
- Or: SFTP connect hangs or disconnects immediately

### Diagnosis

1. Check if the user exists on the system and is in the `jabali-sftp` group:
   ```bash
   id <username>
   # Should output: uid=NNN(<username>) gid=NNN(<username>) groups=...,NNN(jabali-sftp)
   
   # If jabali-sftp group is missing:
   id -nG <username> | grep jabali-sftp
   # Should print "jabali-sftp"; if empty, see "Probable Causes" below
   ```

2. Check if authorized_keys file exists and is readable:
   ```bash
   ls -la /home/<username>/.ssh/authorized_keys
   # Should exist with mode 0600, owned by <username>:<username>
   
   # Check contents:
   cat /home/<username>/.ssh/authorized_keys
   # Should show the public key lines added via the panel
   ```

3. Check sshd configuration for the Match Group block:
   ```bash
   sudo sshd -T -C user=<username> | grep -iE 'match|forcecommand|passwordauthentication|allowtcpforwarding'
   # Should show: forcecommand internal-sftp, passwordauthentication no, allowtcpforwarding no
   ```

4. Check sshd drop-in file exists:
   ```bash
   ls -la /etc/ssh/sshd_config.d/jabali-sftp.conf
   # Should exist with mode 0644
   
   # Verify syntax:
   sudo sshd -T -f /etc/ssh/sshd_config.d/jabali-sftp.conf
   ```

5. Check sshd logs for auth errors:
   ```bash
   sudo journalctl -u ssh -n 50 | grep -iE '<username>|auth|publickey'
   # Look for "Accepted publickey" (success) or rejection reason
   ```

6. Manually test SSH connection (without SFTP):
   ```bash
   # From a client with the private key:
   ssh -vvv -i ~/.ssh/id_ed25519 <username>@<host>
   # Should show "Accepted publickey" in verbose output
   # Connection may close immediately (expected, no shell allowed)
   ```

7. Test SFTP explicitly:
   ```bash
   sftp -i ~/.ssh/id_ed25519 -v <username>@<host>
   # Should show "Connected to <host>" and an sftp> prompt
   # Try: ls, cd, pwd
   ```

8. Check panel agent logs for authorized_keys sync errors:
   ```bash
   sudo journalctl -u jabali-panel-agent -n 100 | grep -iE 'ssh|authorized|reconcile'
   # Look for errors when writing authorized_keys
   ```

9. Check panel API database for the SSH key:
   ```bash
   mysql -u panel_admin -p <panel_db> -e "SELECT id, user_id, name, fingerprint, created_at FROM ssh_keys WHERE user_id = '<user_id>';"
   # Should show the key entry with correct fingerprint
   ```

### Probable Causes & Resolution

**Cause: User not in jabali-sftp group (reconciler hasn't run or failed)**

```bash
# Manually add user to group (reconciler should do this automatically)
sudo usermod -aG jabali-sftp <username>

# Verify group membership took effect (may require new SSH session)
id <username>

# Trigger reconciler to converge state
curl -XPOST https://localhost:8443/api/v1/admin/reconcile \
  -H "Authorization: Bearer <admin_token>" \
  -H "Content-Type: application/json"
```

**Cause: authorized_keys file missing or unreadable**

```bash
# Check if .ssh directory exists with correct mode
ls -la /home/<username>/.ssh/
# Should be mode 0700, owned by <username>:<username>

# If missing, create it:
sudo mkdir -p /home/<username>/.ssh
sudo chmod 0700 /home/<username>/.ssh
sudo chown <username>:<username> /home/<username>/.ssh

# If authorized_keys is missing, trigger reconciler to recreate it:
curl -XPOST https://localhost:8443/api/v1/admin/reconcile \
  -H "Authorization: Bearer <admin_token>"

# Or manually add the key via panel UI (triggers reconciler)
```

**Cause: authorized_keys has incorrect permissions or ownership**

```bash
# Check current permissions:
ls -la /home/<username>/.ssh/authorized_keys

# Fix ownership and mode:
sudo chown <username>:<username> /home/<username>/.ssh/authorized_keys
sudo chmod 0600 /home/<username>/.ssh/authorized_keys
```

**Cause: SSH key not in authorized_keys despite being added in panel**

The reconciler syncs SSH keys to authorized_keys. If the key is in the database but not on disk:

```bash
# Check panel database for the key:
mysql -u panel_admin -p <panel_db> -e \
  "SELECT id, name, public_key FROM ssh_keys WHERE user_id = '<user_id>';"

# If key exists in DB, force reconciler to re-sync:
curl -XPOST https://localhost:8443/api/v1/admin/reconcile \
  -H "Authorization: Bearer <admin_token>"

# Check panel-agent logs for errors:
sudo journalctl -u jabali-panel-agent -n 200 | grep -iE 'ssh_authorized|write|error'

# If reconciler is stuck, check agent UDS is responsive:
ls -la /run/jabali/agent.sock
# Should exist and be accessible by www-data (panel API user)
```

**Cause: sshd configuration incorrect (Match Group block missing)**

```bash
# Verify jabali-sftp.conf was installed:
cat /etc/ssh/sshd_config.d/jabali-sftp.conf

# If missing, re-run install:
sudo /path/to/install.sh

# If present but sshd rejects it, check syntax:
sudo sshd -t
# Should output "Configuration OK"

# Reload sshd:
sudo systemctl reload sshd
```

**Cause: User's public key pasted as private key by accident**

When adding a key in the panel, if the user pastes a private key instead of the public key:

```bash
# Panel will reject it with "invalid_key" error
# User should:
# 1. Regenerate keypair: ssh-keygen -t ed25519 -f ~/.ssh/id_ed25519
# 2. Copy the PUBLIC key ONLY: cat ~/.ssh/id_ed25519.pub
# 3. Paste into panel "SSH Keys" → "Add Key" → "Public Key" field
# 4. Never paste the contents of ~/.ssh/id_ed25519 (private key)
```

**Cause: RSA key too small (< 2048 bits)**

Panel rejects RSA keys smaller than 2048 bits. If user has an old 1024-bit RSA key:

```bash
# User should generate a new key:
ssh-keygen -t ed25519 -f ~/.ssh/id_ed25519 -C "user@host"
# ed25519 is preferred (shorter, more secure than RSA)

# Or if RSA is required, ensure 4096 bits:
ssh-keygen -t rsa -b 4096 -f ~/.ssh/id_rsa_newkey -C "user@host"
```

---

## SFTP connection works but user cannot list files or sees permission denied

### Symptoms
- SFTP login succeeds (`sftp> ` prompt appears)
- `ls` command hangs or returns "Permission denied"
- `pwd` shows "/" or unexpected path
- `cd /home/<username>` fails with "Permission denied"

### Diagnosis

1. Check if user's homedir exists and has correct mode:
   ```bash
   ls -la /home/<username>/
   # Should be mode 0750, owned by <username>:<username>
   
   # Check if sshd can access it (sshd runs as root, but respects permissions):
   stat /home/<username>/
   ```

2. Check if the per-user systemd slice was created (M9 integration):
   ```bash
   systemctl status user-<uid>.slice
   # Should be "active (active)"
   
   # Or list all user slices:
   systemctl list-units --type slice | grep user-
   ```

3. Verify sshd Match Group block restricts to home directory:
   ```bash
   sudo sshd -T -C user=<username> | grep -iE 'chrootdirectory|internal-sftp'
   # Should show: forcecommand internal-sftp (no ChrootDirectory — that's by design)
   ```

4. Manually check sshd homedir expansion:
   ```bash
   # sshd -T shows the effective config after expanding %h, %u, etc.
   sudo sshd -T -C user=<username> | grep -i home
   ```

### Probable Causes & Resolution

**Cause: User homedir doesn't exist (per-user slice not created)**

This is an M9 prerequisite. If homedir is missing, the per-user slice wasn't provisioned:

```bash
# Check if user was created by the panel:
id <username>
# Should show UID, groups, etc.

# If user exists but homedir doesn't:
sudo -u <username> test -d /home/<username> || echo "missing"

# Create the homedir:
sudo mkdir -p /home/<username>
sudo chmod 0750 /home/<username>
sudo chown <username>:<username> /home/<username>

# Trigger M9 reconciler to set up slice:
curl -XPOST https://localhost:8443/api/v1/admin/reconcile \
  -H "Authorization: Bearer <admin_token>"

# Verify slice was created:
systemctl status user-<uid>.slice
```

**Cause: Homedir mode too restrictive (mode 0700)**

By design, homedirs are mode 0750 (user + group readable). If mode 0700:

```bash
# Fix permissions:
sudo chmod 0750 /home/<username>/
sudo chown <username>:<username> /home/<username>/
```

**Cause: sshd's HOME environment variable not set correctly**

If `pwd` shows "/" instead of `/home/<username>`:

```bash
# This can happen if sshd can't resolve the user's homedir.
# Verify the user exists in /etc/passwd:
grep "^<username>:" /etc/passwd
# Should show: <username>:x:NNN:NNN:...:(/home/<username>|/var/lib/...):...

# If user's shell is listed as /bin/false or /sbin/nologin, that's OK for SFTP.
# If homedir path is wrong, fix it:
sudo usermod -d /home/<username> <username>
```

**Cause: PAM or NSS issues resolving user**

On some systems, getent may fail to resolve the user if NSS caches are stale:

```bash
# Check if NSS can resolve the user:
getent passwd <username>
# Should show the user entry

# If NSS cache is stale, flush it:
sudo systemctl restart nscd
# (or systemctl restart systemd-logind if nscd is not running)
```

---

## SSH attempts with a shell are refused (expected behavior, but user confused)

### Symptoms
- User tries: `ssh -i ~/.ssh/id_ed25519 <username>@<host>`
- Connection is accepted but closed immediately (no shell prompt)
- Or: error "Unexpected channel type" or "subsystem error"
- User expects a shell but is restricted to SFTP only

### Diagnosis

1. Verify the Match Group block in sshd config includes ForceCommand:
   ```bash
   grep -A10 "Match Group jabali-sftp" /etc/ssh/sshd_config.d/jabali-sftp.conf
   # Should show: ForceCommand internal-sftp
   ```

2. Check sshd logs for the connection attempt:
   ```bash
   sudo journalctl -u ssh -n 50 | grep -iE '<username>|forced|internal-sftp'
   # Should show: "Forced command: internal-sftp" (informational)
   ```

### Explanation

**This is intentional and secure.** SFTP users in the `jabali-sftp` group are restricted to SFTP-only access. They cannot:
- Open an interactive shell
- Forward TCP ports
- Run arbitrary commands
- Use X11 forwarding

If a user in the group attempts `ssh` (shell), sshd will:
1. Accept the key authentication (✓ correct key)
2. Apply the Match Group directives
3. Enforce `ForceCommand internal-sftp` (the SFTP subsystem only)
4. Close the connection (no interactive shell available)

This is **not a bug or error**; it's the designed security boundary. Shell access would be a separate feature (M13 or later).

---

## User deleted from panel but can still SFTP

### Symptoms
- User was deleted from the panel (via the UI or API)
- User can still SFTP into their homedir using an old public key
- Or: user is no longer in the panel but their `.ssh/authorized_keys` file still exists

### Diagnosis

1. Check if user still exists in panel database:
   ```bash
   mysql -u panel_admin -p <panel_db> -e "SELECT id, username, deleted_at FROM users WHERE username = '<username>';"
   # If deleted_at is NOT NULL, the user was soft-deleted
   ```

2. Check if authorized_keys still exists:
   ```bash
   ls -la /home/<username>/.ssh/authorized_keys
   # If file exists and contains keys, user can still auth
   ```

3. Check reconciler state (if reconciler runs):
   ```bash
   sudo journalctl -u jabali-panel-agent -n 100 | grep -iE '<username>|orphan|cleanup'
   # Look for "cleaning up deleted user" messages
   ```

### Probable Causes & Resolution

**Cause: authorized_keys file not deleted when user was removed**

The reconciler should clean up orphaned SSH keys when a user is deleted. If keys persist:

```bash
# Manual cleanup: remove the authorized_keys file
sudo rm /home/<username>/.ssh/authorized_keys

# Or remove the entire .ssh directory if completely orphaned:
sudo rm -rf /home/<username>/.ssh

# Force reconciler to run:
curl -XPOST https://localhost:8443/api/v1/admin/reconcile \
  -H "Authorization: Bearer <admin_token>"
```

**Cause: User exists in system but is soft-deleted in panel**

If the panel soft-deletes users (sets `deleted_at` timestamp), but the Linux user account still exists:

```bash
# This is expected during the soft-delete grace period.
# After the grace period, a background job should remove the Linux user:
sudo userdel -r <username>
# (-r removes homedir and all files)

# Alternatively, disable SFTP access immediately by removing from group:
sudo usermod -G "" <username>
# (this removes all supplementary groups; you may want to preserve others)
```

---

## Deleting a key in the panel doesn't revoke SFTP access immediately

### Symptoms
- User deletes an SSH key in the panel UI
- Panel shows key is deleted
- But user can still SFTP using that key for a short time (a few seconds)
- Or: key is still in authorized_keys file on disk

### Diagnosis

1. Check if key is still in the panel database:
   ```bash
   mysql -u panel_admin -p <panel_db> -e "SELECT id, user_id, name, created_at FROM ssh_keys WHERE id = '<key_id>';"
   # If no results, key was deleted from DB
   ```

2. Check if key is still in authorized_keys:
   ```bash
   cat /home/<username>/.ssh/authorized_keys
   # Look for the key that was deleted
   ```

3. Check reconciler sync status:
   ```bash
   sudo journalctl -u jabali-panel-agent -n 50 | grep -iE 'ssh|authorized|write'
   # Look for "wrote authorized_keys" message with recent timestamp
   ```

### Explanation

**This is normal and expected.** There's a brief grace period (~1–5 seconds) between:
1. User deletes key in panel UI
2. Panel API deletes key from database
3. Reconciler detects the change
4. Agent writes updated authorized_keys to disk
5. SSH session respects the new authorized_keys

During this window, if a user initiates an SFTP connection with the old key, sshd may accept it because the connection negotiation happens before the reconciler's next sync cycle.

To minimize this window, the reconciler runs frequently (default every 10 seconds). To trigger an immediate sync:

```bash
# Force reconciler to run NOW:
curl -XPOST https://localhost:8443/api/v1/admin/reconcile \
  -H "Authorization: Bearer <admin_token>"
```

---

## Multiple SSH keys for the same user

### Symptoms
- User wants to add 2 or more SSH keys (e.g., one for laptop, one for desktop)
- Panel allows adding multiple keys per user
- But SFTP might not find the right key or shows confusion

### Explanation

**This is supported and expected.** A user can have multiple SSH keys in the panel, and all will be written to their `authorized_keys` file. sshd will accept any of them.

When the user SFTP connects:
```bash
# Try each key in sequence until one is accepted:
sftp -i ~/.ssh/id_laptop <username>@<host>
sftp -i ~/.ssh/id_desktop <username>@<host>
sftp -i ~/.ssh/id_rsa <username>@<host>  # if also added
```

Alternatively, use `~/.ssh/config` to specify multiple identities:
```
Host jabali-panel
  HostName <host>
  User <username>
  IdentityFile ~/.ssh/id_laptop
  IdentityFile ~/.ssh/id_desktop
```

Then: `sftp jabali-panel:`

To revoke access from a specific key, delete it in the panel. That key is removed from authorized_keys, and sshd will reject future attempts with that key.

---

## Debugging a key that's rejected as invalid in the panel

### Symptoms
- User pastes a public key in the "Add Key" modal
- Panel shows error: "The public key could not be parsed"
- Or: "Must start with ssh-rsa, ssh-ed25519, or ecdsa-sha2-"
- Or: "RSA key must be at least 2048 bits"

### Diagnosis

1. Check the format of the key the user pasted:
   ```bash
   # Should START with one of:
   # - ssh-rsa (for RSA keys)
   # - ssh-ed25519 (for Edwards curve keys, recommended)
   # - ecdsa-sha2-nistp256, ecdsa-sha2-nistp384, ecdsa-sha2-nistp521 (for ECDSA)
   
   # Should be a single line (or pasted correctly; line breaks are OK if auto-unwrapped)
   ```

2. Verify the user copied the PUBLIC key, not the private key:
   ```bash
   # Public key location (correct):
   cat ~/.ssh/id_ed25519.pub
   # Output: ssh-ed25519 AAAA... user@host
   
   # Private key location (WRONG — never paste this):
   cat ~/.ssh/id_ed25519
   # Output: -----BEGIN OPENSSH PRIVATE KEY----- ...
   ```

3. If the key format looks correct, check the RSA key size:
   ```bash
   # Extract the RSA key and check its size:
   ssh-keygen -l -f <(echo "<pasted_key>")
   # Output: 4096 SHA256:... (should show bit size)
   
   # If < 2048 bits, it will be rejected
   ```

### Probable Causes & Resolution

**Cause: User pasted private key instead of public key**

```bash
# Never paste the private key (~/.ssh/id_ed25519 or ~/.ssh/id_rsa)
# Always paste the PUBLIC key (~/.ssh/id_ed25519.pub or ~/.ssh/id_rsa.pub)

# Example (correct):
cat ~/.ssh/id_ed25519.pub
# ssh-ed25519 AAAA... user@host  ← paste THIS line

# Incorrect (never):
cat ~/.ssh/id_ed25519
# -----BEGIN OPENSSH PRIVATE KEY-----
# ...
```

**Cause: Key is RSA < 2048 bits (too weak)**

```bash
# Generate a new, stronger RSA key:
ssh-keygen -t rsa -b 4096 -f ~/.ssh/id_rsa_new -C "user@host"

# Or use ed25519 (recommended, shorter and more secure):
ssh-keygen -t ed25519 -f ~/.ssh/id_ed25519 -C "user@host"

# Paste the public key from the generated .pub file
```

**Cause: Key line was corrupted or truncated in paste**

```bash
# If the pasted key looks strange or cut off, regenerate and try again:
cat ~/.ssh/id_ed25519.pub | wc -c
# Should be 80–200 characters for ed25519

# Paste carefully; ensure the entire line is copied (no line breaks in the middle)
```

---

## Manual SFTP verification steps (happy path)

To confirm SFTP is working end-to-end:

```bash
# 1. On the client, generate or locate a key:
ssh-keygen -t ed25519 -f ~/.ssh/id_test -C "test@example.com"

# 2. Add the public key in the panel:
# - UI: Click "Add Key", fill Name and Public Key, submit
# - Then verify the key appears in the list with a fingerprint

# 3. From the client, SFTP connect:
sftp -i ~/.ssh/id_test -P 22 <username>@<host>

# 4. At the sftp> prompt, verify operations:
sftp> pwd
# Remote working directory: /home/<username>

sftp> ls
# (lists files in homedir)

sftp> put /tmp/testfile.txt
# (uploads testfile.txt)

sftp> ls testfile.txt
# (confirms upload)

sftp> get testfile.txt /tmp/testfile-download.txt
# (downloads the file)

sftp> bye
# (closes connection)

# 5. Verify the file exists on the panel server:
ls -la /home/<username>/testfile.txt
# Should show the file with correct ownership
```

---

## Rollback (disable SFTP temporarily)

To quickly disable SFTP access without uninstalling:

```bash
# Option 1: Remove users from jabali-sftp group (revokes SFTP access immediately)
sudo usermod -G "" <username>
# (Removes all supplementary groups; you may want to be more selective)

# Option 2: Disable the sshd drop-in (requires reload)
sudo mv /etc/ssh/sshd_config.d/jabali-sftp.conf /etc/ssh/sshd_config.d/jabali-sftp.conf.disabled
sudo systemctl reload sshd

# Option 3: Full uninstall (remove all SFTP components)
# Remove SSH keys from panel database:
mysql -u panel_admin -p <panel_db> -e "DELETE FROM ssh_keys;"

# Remove group and drop-in:
sudo groupdel jabali-sftp
sudo rm /etc/ssh/sshd_config.d/jabali-sftp.conf

# Reload sshd:
sudo systemctl reload sshd
```

---

## Security invariants (reference)

The following security boundaries are **intentional and must be maintained:**

- **SFTP-only access:** ForceCommand internal-sftp prevents shell logins for users in `jabali-sftp` group
- **No TCP forwarding:** AllowTcpForwarding no prevents port forwarding
- **No X11 forwarding:** X11Forwarding no prevents X11 tunneling
- **No tunneling:** PermitTunnel no
- **No password auth:** PasswordAuthentication no (key-only)
- **Homedir isolation:** Each user's homedir is mode 0750; users cannot ls or access other homedirs
- **authorized_keys protection:** File mode 0600, owner is the user (only they can read/modify via reconciler)

If any of these are weakened, file isolation and access control will be compromised.

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
- Ensure all jabali users are in the `jabali-sftp` group
- Write or update authorized_keys for each user from the database
- Remove SSH keys from authorized_keys for users with no keys
- Clean up authorized_keys files for deleted users

---

## Quick reference

| Issue | Command |
|-------|---------|
| Check user in jabali-sftp group | `id -nG <user> \| grep jabali-sftp` |
| Check authorized_keys | `cat /home/<user>/.ssh/authorized_keys` |
| Fix authorized_keys ownership | `sudo chown <user>:<user> /home/<user>/.ssh/authorized_keys` |
| Fix authorized_keys mode | `sudo chmod 0600 /home/<user>/.ssh/authorized_keys` |
| Add user to group | `sudo usermod -aG jabali-sftp <user>` |
| Remove user from group | `sudo usermod -G "" <user>` |
| Test SFTP connection | `sftp -i ~/.ssh/key <user>@<host>` |
| Check sshd config | `sudo sshd -T -C user=<user>` |
| Reload sshd | `sudo systemctl reload ssh` |
| Check sshd logs | `sudo journalctl -u ssh -n 50` |
| Trigger reconciler | `curl -XPOST https://localhost:8443/api/v1/admin/reconcile` |
| Verify key in DB | `mysql -u panel_admin -p <db> -e "SELECT name, fingerprint FROM ssh_keys WHERE user_id = '<uid>'"` |
| Delete key in DB | `mysql -u panel_admin -p <db> -e "DELETE FROM ssh_keys WHERE id = '<key_id>'"` |

---

**Last updated:** 2026-04-18
