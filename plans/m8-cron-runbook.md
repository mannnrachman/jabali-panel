# M8: Cron Scheduling Troubleshooting Runbook

## Architecture Overview

Cron jobs run as systemd-user timers under each user's per-user systemd manager (ADR-0025 provisioned lingering). The pipeline flows:

```
User creates job in panel API
     ↓
Panel DB stores (cron_jobs row)
     ↓
Reconciler reads DB, generates unit files
     ↓
Agent writes /etc/jabali-panel/cron-units/<user>/*.{service,timer}
     ↓
systemctl --user link + enable --now
     ↓
systemd-user timer fires on schedule
     ↓
Service runs command via ExecStart
     ↓
journalctl --user-unit captures stdout/stderr
     ↓
Reconciler polls systemctl show → updates last_run_at, last_exit_code
     ↓
Panel UI reflects latest state
```

## Normal Operation

When a cron job is working correctly, you should see:

1. **Timer listed in user's timers:**
   ```bash
   sudo -u <user> XDG_RUNTIME_DIR=/run/user/$(id -u <user>) \
     systemctl --user list-timers
   # Shows: jabali-cron-<id>.timer with next/last run times and schedule
   ```

2. **Job last_run_at is recent (within reconciler cadence ~30s):**
   ```bash
   # Panel API
   curl https://localhost:8443/api/v1/cron/<id> \
     -H "Authorization: Bearer <token>"
   # Returns: { "last_run_at": "2026-04-18T15:32:00Z", "last_exit_code": 0 }
   ```

3. **Service shows successful status:**
   ```bash
   sudo -u <user> XDG_RUNTIME_DIR=/run/user/$(id -u <user>) \
     systemctl --user show -p ExecMainStatus -p InactiveExitTimestamp jabali-cron-<id>.service
   # ExecMainStatus=0 (success)
   # InactiveExitTimestamp=2026-04-18 15:32:05 CEST (recent)
   ```

4. **Journal contains job output:**
   ```bash
   sudo -u <user> XDG_RUNTIME_DIR=/run/user/$(id -u <user>) \
     journalctl --user -u jabali-cron-<id>.service -n 50
   # Shows command output or "Command exited with code 0" message
   ```

---

## Troubleshooting Decision Tree

### **Q: Job is not running at all (last_run_at is "Never")**

1. **Check if linger is enabled:**
   ```bash
   loginctl show-user <user> | grep Linger
   # Should show: Linger=yes
   # If no: sudo loginctl enable-linger <user>
   ```

2. **Check if timer exists and is enabled:**
   ```bash
   sudo -u <user> XDG_RUNTIME_DIR=/run/user/$(id -u <user>) \
     systemctl --user list-timers jabali-cron-<id>.timer
   # Should show the timer with a future "NEXT" time
   # If not listed: timer unit file is missing or not loaded
   ```

3. **Check user systemd manager is running:**
   ```bash
   ps aux | grep systemd --user
   # Should show a user manager process per lingering user
   # If not: sudo systemctl start user-systemd-runtime-dir@<uid>.service
   ```

4. **Check timer is in the right schedule:**
   ```bash
   sudo -u <user> XDG_RUNTIME_DIR=/run/user/$(id -u <user>) \
     systemctl --user show jabali-cron-<id>.timer -p OnCalendar
   # Should match the database schedule (e.g., OnCalendar=0 * * * *)
   # If wrong: reconciler should fix on next pass; force: touch DB row, wait 30s
   ```

5. **Check if systemd unit file is where reconciler put it:**
   ```bash
   ls -la /etc/jabali-panel/cron-units/<user>/jabali-cron-<id>.*
   # Should exist as service + timer files
   # If missing: agent.cron.apply failed; check panel-agent logs
   ```

6. **Check for journal errors:**
   ```bash
   sudo -u <user> XDG_RUNTIME_DIR=/run/user/$(id -u <user>) \
     journalctl --user -u jabali-cron-<id>.timer -n 20
   # Look for "failed", "could not", "no such file"
   ```

---

### **Q: Job ran once but not again (last_run_at is old)**

1. **Check if timer is still enabled:**
   ```bash
   sudo -u <user> XDG_RUNTIME_DIR=/run/user/$(id -u <user>) \
     systemctl --user is-enabled jabali-cron-<id>.timer
   # Should print: enabled
   # If not: sudo -u <user> XDG_RUNTIME_DIR=/run/user/$(id -u <user>) \
   #          systemctl --user enable jabali-cron-<id>.timer
   ```

2. **Check if Persistent=true is set:**
   ```bash
   sudo -u <user> XDG_RUNTIME_DIR=/run/user/$(id -u <user>) \
     systemctl --user show jabali-cron-<id>.timer -p Persistent
   # Should show: Persistent=yes
   # If no: unit file is corrupt; reconciler will regenerate next pass
   ```

3. **Check next trigger time:**
   ```bash
   sudo -u <user> XDG_RUNTIME_DIR=/run/user/$(id -u <user>) \
     systemctl --user list-timers jabali-cron-<id>.timer
   # Look at "NEXT" column; if it's 0:00 (past), timer may not have fired yet
   # or the system clock is wrong. Check: date
   ```

4. **Manually trigger the service to verify it works:**
   ```bash
   sudo -u <user> XDG_RUNTIME_DIR=/run/user/$(id -u <user>) \
     systemctl --user start jabali-cron-<id>.service
   # Then immediately check journal:
   journalctl --user -u jabali-cron-<id>.service -n 10 -f
   # If it succeeds, the command is fine; schedule is the issue
   ```

---

### **Q: Job exited with non-zero code**

1. **Check last_exit_code in panel:**
   ```bash
   curl https://localhost:8443/api/v1/cron/<id> \
     -H "Authorization: Bearer <token>"
   # Look at: "last_exit_code": <N> where N != 0
   ```

2. **Read full error from journal:**
   ```bash
   sudo -u <user> XDG_RUNTIME_DIR=/run/user/$(id -u <user>) \
     journalctl --user -u jabali-cron-<id>.service -n 100 -o cat
   # Shows the command's actual stderr/stdout
   ```

3. **Common exit codes:**
   - `1` — command not found, bad arg, or script error
   - `127` — executable not in PATH
   - `126` — permission denied
   - `2` — usage error
   - **Example fix:** `wp` command may need `--path=/full/path/to/docroot`

4. **Re-validate the command:**
   ```bash
   # In panel, edit the job and resubmit (or delete + recreate)
   # to get fresh validator error messages
   ```

---

### **Q: "User not lingering" error from panel when creating/running**

The job's user does not have `loginctl enable-linger` set, so the per-user systemd manager will not survive logout.

**Fix:**
```bash
sudo loginctl enable-linger <user>
# Then re-trigger the cron job in the panel (Run Now or wait for schedule)
```

---

### **Q: "command_not_allowed" when creating/updating a job**

The validator rejected the command. Valid formats:

- `wp <subcommand> --path=<owned-docroot> [args...]`
  - Example: `wp cron event run --due-now --path=/home/myuser/example.com/public_html`
  - `<owned-docroot>` MUST be a directory owned by the user

- `php <owned-docroot>/<relative-path>.php [args...]`
  - Example: `php /home/myuser/example.com/public_html/health-check.php`
  - Path must not escape the docroot via `..` or symlinks

**Rejected examples:**
- `cat /etc/passwd` — not an allowed binary
- `bash -c "..."` — shell invocation forbidden
- `php ../../etc/passwd` — path traversal
- `mysqldump` — backups not allowed in v1

**Fix:** Edit the job command to use an allowed format. If you need a different command, file a feature request for the validator allowlist.

---

### **Q: last_run_at doesn't update after waiting 30 seconds**

The reconciler polls `systemctl show` every ~30 seconds. If the job ran but the panel UI doesn't reflect it:

1. **Check if the service actually ran:**
   ```bash
   sudo -u <user> XDG_RUNTIME_DIR=/run/user/$(id -u <user>) \
     systemctl --user show -p ExecMainStatus -p InactiveExitTimestamp \
       jabali-cron-<id>.service
   # If ExecMainStatus=0 and InactiveExitTimestamp is recent: service DID run
   ```

2. **Force reconciler to run:**
   ```bash
   # Panel admin endpoint (if available)
   curl -X POST https://localhost:8443/api/v1/admin/reconcile \
     -H "Authorization: Bearer <admin-token>"
   ```

3. **Check panel-api logs for reconciler errors:**
   ```bash
   journalctl -u jabali-panel-api -n 100 | grep -i "cron\|reconcile"
   ```

4. **Check that the panel API can read the service status:**
   ```bash
   # The reconciler may fail if it cannot invoke systemctl as the user
   # This is a deployment issue; verify sudo rules for panel-api user
   ```

---

## Common Failure Modes & Recovery

### **Orphan unit files after job deletion**

**Problem:** User deletes a job in the panel, but the systemd unit files remain.

**Recovery:**
```bash
# Manually remove orphan files (reconciler should clean these automatically)
sudo rm -f /etc/jabali-panel/cron-units/<user>/jabali-cron-<old-id>.*

# Reload user's systemd manager
sudo -u <user> XDG_RUNTIME_DIR=/run/user/$(id -u <user>) \
  systemctl --user daemon-reload
```

---

### **TOCTOU symlink swap (path changes between Create and service exec)**

**Problem:** User creates a job with command `php /home/user/docroot1/script.php`. Then the domain is moved to a different path or docroot is symlinked elsewhere. At service exec time, the path is invalid or outside the user's tree.

**Prevention:** `ExecStartPre=/usr/local/libexec/jabali/cron-precheck <path>` runs as the target user and re-validates ownership before ExecStart. If the docroot is gone or inaccessible, the service fails with exit code 1 (visible in panel UI).

**Recovery:** Edit the job command to point to the correct docroot, or delete and recreate.

---

### **Race: run-now while timer fires**

**Problem:** User clicks "Run Now" while the scheduled timer is also firing.

**Prevention:** Service unit has `StartLimitBurst=1` and `StartLimitIntervalSec=1`, which prevents a second start within 1 second. `Type=oneshot` ensures the service serializes if already running.

**Visible effect:** One run completes successfully; the second is queued or ignored (systemd logs it as "start request repeated too quickly").

---

### **Systemd unit file corruption or out-of-sync with DB**

**Problem:** Someone manually edits `/etc/jabali-panel/cron-units/<user>/jabali-cron-<id>.service`.

**Recovery:**
1. Reconciler will detect the drift on next pass (every ~30s) and regenerate from DB.
2. To force immediately, touch the database row and wait 30 seconds, or:
   ```bash
   sudo -u <user> XDG_RUNTIME_DIR=/run/user/$(id -u <user>) \
     systemctl --user daemon-reload
   ```
3. If the file has syntax errors, the loader will report them in journal:
   ```bash
   sudo -u <user> XDG_RUNTIME_DIR=/run/user/$(id -u <user>) \
     journalctl --user -n 50 | grep -i "syntax\|parse"
   ```

---

### **User manager dead (linger off, no running sessions)**

**Problem:** User logs out and linger is off. The user-systemd-runtime-dir and user manager are cleaned up.

**Symptom:** `sudo -u <user> systemctl --user status` fails with "could not connect to bus".

**Recovery:**
```bash
# Re-enable linger
sudo loginctl enable-linger <user>

# Restart the user manager (optional; it auto-starts on next action)
sudo systemctl --user --machine=<user>@.host restart
```

---

## Tail Cron Logs (Per-Job)

To view the last 50 lines of a specific job's output:

```bash
# Option 1: Panel API (recommended for users)
curl https://localhost:8443/api/v1/cron/<job-id>/log \
  -H "Authorization: Bearer <user-token>"
# Returns: { "lines": ["line1", "line2", ...] }

# Option 2: Direct journal access (admin/operator use)
sudo -u <user> XDG_RUNTIME_DIR=/run/user/$(id -u <user>) \
  journalctl --user -u jabali-cron-<job-id>.service -n 50 -o cat
```

---

## Cheat Sheet (Quick Reference)

| Task | Command |
|------|---------|
| **List all timers for a user** | `sudo -u <user> XDG_RUNTIME_DIR=/run/user/$(id -u <user>) systemctl --user list-timers` |
| **Enable linger for user** | `sudo loginctl enable-linger <user>` |
| **Check linger status** | `loginctl show-user <user> \| grep Linger` |
| **Manual service start** | `sudo -u <user> XDG_RUNTIME_DIR=/run/user/$(id -u <user>) systemctl --user start jabali-cron-<id>.service` |
| **Reload user daemon** | `sudo -u <user> XDG_RUNTIME_DIR=/run/user/$(id -u <user>) systemctl --user daemon-reload` |
| **View service status** | `sudo -u <user> XDG_RUNTIME_DIR=/run/user/$(id -u <user>) systemctl --user show -p ExecMainStatus -p InactiveExitTimestamp jabali-cron-<id>.service` |
| **Tail service journal** | `sudo -u <user> XDG_RUNTIME_DIR=/run/user/$(id -u <user>) journalctl --user -u jabali-cron-<id>.service -n 50 -o cat` |
| **Tail timer journal** | `sudo -u <user> XDG_RUNTIME_DIR=/run/user/$(id -u <user>) journalctl --user -u jabali-cron-<id>.timer -n 50 -o cat` |
| **Remove orphan unit files** | `sudo rm -f /etc/jabali-panel/cron-units/<user>/jabali-cron-<id>.*` |
| **Verify unit file exists** | `ls -la /etc/jabali-panel/cron-units/<user>/jabali-cron-<id>.{service,timer}` |
| **Force next reconcile** | `curl -X POST https://localhost:8443/api/v1/admin/reconcile -H "Authorization: Bearer <admin-token>"` |
| **Check panel reconciler logs** | `journalctl -u jabali-panel-api -n 100 \| grep -i cron` |

---

## Further Reading

- **Architecture:** `/home/shuki/projects/jabali2/plans/m8-cron.md` (§0-6 cover design, unit templates, and systemd invocation)
- **API Reference:** `/home/shuki/projects/jabali2/panel-api/internal/api/cron.go` (route shapes, error codes)
- **Validator:** `/home/shuki/projects/jabali2/panel-api/internal/cronvalidate/cron.go` (allowlist rules + test cases)
- **Agent Commands:** `/home/shuki/projects/jabali2/panel-agent/internal/commands/cron_*.go` (apply/remove/run_now/tail_log implementations)
- **Reconciler:** `/home/shuki/projects/jabali2/panel-api/internal/reconciler/cron_reconcile.go` (drift detection + orphan cleanup)

---

**Last updated:** 2026-04-18 (M8 Wave F — final delivery)
