# M11 FileBrowser Decommission Checklist

**Purpose:** Remove all FileBrowser v2.38.0 integration touchpoints from jabali2 and prepare for custom AntD-based file manager.

**Scope:** 28 files across 10 categories | Approx 350 lines of code/config removed | 1 database migration to reverse | 5 agent commands to delete | 2 API routes to remove

---

## Table of Contents with Counts

1. **Installer & Updater** (2 functions, 1 file) — Download/setup logic in install.sh
2. **Systemd & Service Management** (2 files) — Service unit + tmpfiles.d drop-in
3. **Nginx Configuration** (2 files) — Reverse proxy /files/ location blocks
4. **Agent Commands** (5 files + 1 test) — CLI operations: user ensure/list/delete, group add, service restart
5. **Panel API Routes & Handlers** (3 files) — SSO token issuance & validation endpoints
6. **Database & Models** (4 files) — Migration, model definition, repository interface + implementation
7. **Config Templates** (2 files) — FileBrowser config.json template + SHA256 checksum
8. **Frontend Components** (3 files) — API client, React launcher component, E2E tests
9. **Documentation & Plans** (4 files) — ADR, BLUEPRINT, runbooks, session-fix plan
10. **Runtime State & Orphans** (cleanup steps) — /var/lib/jabali-filebrowser, /run/jabali-filebrowser, /opt/filebrowser, ACLs, system users

---

## Detailed File List (Path | Action | Notes)

### 1. Installer & Updater

| Path | Action | Notes |
|------|--------|-------|
| `/home/shuki/projects/jabali2/install.sh` | **MODIFY** | Delete 4 functions: `install_filebrowser` (lines 1862–1945, 84 lines), `uninstall_filebrowser` (lines 1957–1968, 12 lines), `update_filebrowser` (lines 1970–2022, 53 lines), `ensure_filebrowser_running` (lines 2024–2040, 17 lines). Total: 166 lines. Keep other install functions. |

### 2. Systemd & Service Management

| Path | Action | Notes |
|------|--------|-------|
| `/home/shuki/projects/jabali2/install/systemd/jabali-filebrowser.service` | **DELETE** | Unit file for jabali-filebrowser systemd service. ~35 lines. |
| `/home/shuki/projects/jabali2/install/systemd/tmpfiles-filebrowser.conf` | **DELETE** | tmpfiles.d drop-in for /run/jabali-filebrowser directory creation. ~3 lines. |

### 3. Nginx Configuration

| Path | Action | Notes |
|------|--------|-------|
| `/home/shuki/projects/jabali2/install/nginx/jabali-files.conf` | **DELETE** | HTTP-context map directive for auth routing. ~10 lines. |
| `/home/shuki/projects/jabali2/install/nginx/includes/jabali-files.conf` | **DELETE** | Location block definitions for /files/ reverse proxy. ~40 lines. |

### 4. Agent Commands

| Path | Action | Notes |
|------|--------|-------|
| `/home/shuki/projects/jabali2/panel-api/internal/agent/cmd/filebrowser_user_ensure.go` | **DELETE** | Creates filebrowser users via CLI. Lines 32–110 (79 lines). Validates username/scope, executes filebrowser CLI. |
| `/home/shuki/projects/jabali2/panel-api/internal/agent/cmd/filebrowser_user_list.go` | **DELETE** | Lists filebrowser users. Lines 19–48 (30 lines). |
| `/home/shuki/projects/jabali2/panel-api/internal/agent/cmd/filebrowser_user_delete.go` | **DELETE** | Deletes filebrowser users via CLI. |
| `/home/shuki/projects/jabali2/panel-api/internal/agent/cmd/filebrowser_group_add.go` | **DELETE** | Adds filebrowser to user's Unix group & applies POSIX ACL. Lines 26–114 (89 lines). Uses usermod + setfacl. |
| `/home/shuki/projects/jabali2/panel-api/internal/agent/cmd/filebrowser_service_restart.go` | **DELETE** | Restarts systemd service. Lines 19–37 (19 lines). |
| `/home/shuki/projects/jabali2/panel-api/internal/agent/cmd/filebrowser_user_ensure_test.go` | **DELETE** | Unit tests for filebrowser_user_ensure. |

### 5. Panel API Routes & Handlers

| Path | Action | Notes |
|------|--------|-------|
| `/home/shuki/projects/jabali2/panel-api/internal/api/routes/sso_filebrowser.go` | **DELETE** | Registers POST /api/v1/sso/filebrowser. Lines 24–27 (route reg), 45–82 (issueSSOToken handler + helper methods). ~80 lines total. |
| `/home/shuki/projects/jabali2/panel-api/internal/api/routes/sso_filebrowser_validate.go` | **DELETE** | Registers POST /api/v1/sso/filebrowser/validate. Lines 21–24 (route reg), 44–94 (validate handler). ~70 lines total. Listens on /var/run/jabali-panel/sso.sock. |
| `/home/shuki/projects/jabali2/panel-api/internal/api/routes/sso_uds.go` | **MODIFY** | Delete conditional registration of sso_filebrowser_validate at line 62 (`if ssoSvc != nil { RegisterSSOFileBrowserValidateRoutes(...) }`). Keep UDS listener setup. ~10 lines removed. |

### 6. Database & Models

| Path | Action | Notes |
|------|--------|-------|
| `/home/shuki/projects/jabali2/panel-api/internal/db/migrations/000034_create_filebrowser_sso_tokens.up.sql` | **DELETE** | Migration to create filebrowser_sso_tokens table. 12 lines. Used for 2-hour TTL SSO tokens. |
| `/home/shuki/projects/jabali2/panel-api/internal/db/migrations/000034_create_filebrowser_sso_tokens.down.sql` | **DELETE** | Migration down (DROP TABLE). 1 line. |
| `/home/shuki/projects/jabali2/panel-api/internal/models/filebrowser_sso_token.go` | **DELETE** | FileBrowserSSOToken struct definition. Contains ID, UserID, TokenHash, CreatedAt, ExpiresAt, UsedAt. |
| `/home/shuki/projects/jabali2/panel-api/internal/repository/filebrowser_sso_token_repository.go` | **DELETE** | Repository interface + implementation. Methods: Create, MarkUsed, DeleteExpired, FindByHash. |

### 7. Config Templates & Checksums

| Path | Action | Notes |
|------|--------|-------|
| `/home/shuki/projects/jabali2/install/filebrowser/config.json.tmpl` | **DELETE** | Template for filebrowser SQLite database config. |
| `/home/shuki/projects/jabali2/install/filebrowser/filebrowser-linux-amd64.sha256` | **DELETE** | SHA256 checksum for binary verification (v2.38.0). |

### 8. Frontend Components

| Path | Action | Notes |
|------|--------|-------|
| `/home/shuki/projects/jabali2/panel-ui/src/apiClient.ts` | **MODIFY** | Remove `ssoFileBrowser()` function (calls POST /api/v1/sso/filebrowser). Search for function definition and delete. ~10 lines. |
| `/home/shuki/projects/jabali2/panel-ui/src/shells/user/files/UserFilesLauncher.tsx` | **DELETE** | React component that renders file manager iframe. Lines 9–77 (69 lines). Calls ssoFileBrowser(), handles loading/error state. |
| `/home/shuki/projects/jabali2/panel-ui/tests/e2e/filebrowser.spec.ts` | **DELETE** | E2E tests for FileBrowser SSO flow. |

### 9. Documentation & Plans

| Path | Action | Notes |
|------|--------|-------|
| `/home/shuki/projects/jabali2/docs/adr/0027-m11-filebrowser-integration.md` | **DELETE or ARCHIVE** | ADR documenting M11 filebrowser integration decision. Mark as superseded by AntD file manager ADR. |
| `/home/shuki/projects/jabali2/docs/BLUEPRINT.md` | **MODIFY** | Find "M11: FileBrowser (SHIPPED)" section and update status to "DEPRECATED - Replaced by AntD File Manager" or remove entry. |
| `/home/shuki/projects/jabali2/plans/m11-filebrowser.md` | **DELETE or ARCHIVE** | Original integration plan. |
| `/home/shuki/projects/jabali2/plans/m11-filebrowser-runbook.md` | **DELETE or ARCHIVE** | Operational runbook. |

### 10. Runtime State & Orphans (Cleanup via uninstall script)

| Path/Item | Action | Notes |
|-----------|--------|-------|
| `/opt/filebrowser/` | **DELETE** | Binary installation directory (v2.38.0 symlink + versioned subdirs). Remove via `rm -rf /opt/filebrowser`. |
| `/var/lib/jabali-filebrowser/` | **DELETE** | State directory. Contains SQLite database, config, logs. Remove via `rm -rf /var/lib/jabali-filebrowser`. |
| `/run/jabali-filebrowser/` | **DELETE** | Runtime tmpfiles.d directory. Remove via `rm -rf /run/jabali-filebrowser`. |
| POSIX ACLs on `/home/{username}` | **CLEAN** | Remove setfacl entries (user:filebrowser) from all user home directories. Script: `getfacl /home/* \| grep filebrowser` then remove. |
| `filebrowser` system user/group | **DELETE** | Remove system user/group: `userdel filebrowser && groupdel filebrowser`. |
| Database migration 000034 | **REVERSE** | Run migration down to drop filebrowser_sso_tokens table. |

---

## Deletion ORDER (10-Step Sequence)

**Rationale:** Respect service dependencies — stop services before removing binaries, remove routes before removing database schema, clean up system state last.

1. **Stop & disable systemd service** → `systemctl stop jabali-filebrowser && systemctl disable jabali-filebrowser`
   - Prevents service restart during removal.
   - *File:* install/systemd/jabali-filebrowser.service

2. **Remove nginx configuration** → Delete `install/nginx/jabali-files.conf` and `install/nginx/includes/jabali-files.conf`
   - Stops proxying /files/ requests.
   - Reload nginx: `nginx -s reload` (or systemctl reload nginx).
   - *Impact:* /files/ URLs will 404 unless redirect plan implemented (see below).

3. **Remove agent commands** → Delete `panel-api/internal/agent/cmd/filebrowser_*.go` (5 files + test)
   - Prevents new user/group operations.
   - No runtime impact; agents will fail gracefully if commands missing.

4. **Remove API routes** → Delete `sso_filebrowser.go`, `sso_filebrowser_validate.go`; modify `sso_uds.go` line 62
   - Stops issuing new SSO tokens.
   - Disables /api/v1/sso/filebrowser & /api/v1/sso/filebrowser/validate endpoints.
   - Rebuild & restart panel-api service.

5. **Remove frontend components** → Delete `UserFilesLauncher.tsx`, `filebrowser.spec.ts`; modify `apiClient.ts`
   - UI will no longer try to launch file manager.
   - Rebuild panel-ui frontend.

6. **Reverse database migration** → `migrate -path panel-api/internal/db/migrations -database "mysql://..." down 1`
   - Drops filebrowser_sso_tokens table.
   - Removes SSO token storage.

7. **Remove FileBrowser binary & config** → Delete `/opt/filebrowser/`, config template, SHA256 checksum
   - Only safe after service is stopped and no agents reference it.

8. **Clean POSIX ACLs** → Remove setfacl entries for `filebrowser` user from all /home/* directories
   - Script: `for user in /home/*; do setfacl -R -x u:filebrowser "$user"; done`
   - Prevents lingering permission issues.

9. **Remove system user/group** → `userdel filebrowser && groupdel filebrowser`
   - Cleans up /etc/passwd, /etc/group, /etc/shadow.
   - Only safe after no ACL references remain.

10. **Clean tmpfiles.d & state directories** → Delete `/install/systemd/tmpfiles-filebrowser.conf`, then remove `/var/lib/jabali-filebrowser/` and `/run/jabali-filebrowser/`
    - Final cleanup of state and configuration.

---

## What's Reusable for New File Manager

✓ **SSO token architecture** — 2-hour TTL tokens with SHA256 hash can be adapted for AntD file manager. Keep:
  - `sso.go::MintFileBrowserToken()` logic (lines 192–227) — adapt method name to MintFileManagerToken()
  - `filebrowser_sso_token_repository.go` — rename to `file_manager_sso_token_repository.go`, update model name

✓ **Reconciler pattern** — `panel-api/internal/reconciler/reconciler.go::ReconcileFileBrowserUsers()` (lines 610–753) is generic state reconciliation. Rename to `ReconcileFileManagerUsers()` and reuse the 4-step pattern:
  - Step 1: Ensure user exists in file manager DB
  - Step 2: Apply permission/ACL mappings
  - Step 3: Restart service if needed
  - Step 4: Orphan cleanup

✓ **UDS listener** — `sso_uds.go` Unix domain socket infrastructure for token validation is reusable. Keep the listener, just swap route registration.

✓ **Nginx reverse proxy pattern** — The /files/ location block structure (auth_request, X-Forwarded-* headers, proxy_pass) is reusable. Create new `jabali-files-new.conf` with updated backend URL.

✗ **Agent commands** — POSIX ACL + setfacl logic is FileBrowser-specific (CLItoken interactions). New file manager likely has API instead of CLI, so full rewrite needed.

---

## Surprises & Findings

1. **SQLite contention issue** — Reconciler commented that FileBrowser CLI commands require exclusive SQLite lock, preventing concurrent user_ensure/user_delete operations. This is architectural weakness of FileBrowser's CLI-only management. AntD file manager API should avoid this.

2. **Conditional UDS route registration** — `sso_uds.go` line 62 only registers validate routes if `ssoSvc != nil`. This means SSO UDS is optional at config time. Ensure new file manager registration respects this pattern.

3. **POSIX ACL escalation** — `filebrowser_group_add.go` uses `setfacl -R -d` (recursive with defaults), which applies ACL to all subdirs. This can bloat inode ACL metadata on large home dirs. New file manager should consider directory-level permissions instead.

4. **2-hour SSO token TTL is hardcoded** — `sso.go` line 202 hardcodes `2 * time.Hour`. Consider making this configurable for new manager.

5. **FileBrowser runs as dedicated `filebrowser` system user** — All file operations in /files/ are under this user's context. New file manager should clarify privilege model (root? per-user? shared user?).

6. **Proxy authentication X-Forwarded-User** — FileBrowser's auth method is `proxy`, expecting `X-Forwarded-User` header from nginx. New file manager may support different auth (JWT, session cookie, etc.).

---

## /files/ Redirect Plan

**Problem:** Users may have bookmarks, documentation, or frontend links pointing to /files/*. After removal, these will 404.

**Solution:** Add nginx redirect rule to preserve UX:

```nginx
# In install/nginx/includes/jabali-files.conf (before deleting):
# Preserve redirect for bookmarked /files/ URLs during transition
location /files/ {
    rewrite ^/files/(.*)$ /file-manager/$1 permanent;
    # Redirect to new file manager path (adjust as needed)
}
```

Then in new file manager nginx config, serve from `/file-manager/` (or your chosen path).

**Alternative:** Return 410 Gone + show banner:
```nginx
location /files/ {
    return 410;
    add_header Content-Type text/plain;
    # Optionally serve an HTML page with migration notice
}
```

**Recommendation:** Implement redirect for ≥3 months post-decommission, then switch to 410 Gone. Update CHANGELOG & docs to announce endpoint shift.

---

## Summary

- **Files to delete:** 18 files
- **Files to modify:** 4 files (install.sh, sso_uds.go, apiClient.ts, BLUEPRINT.md)
- **Database migrations:** 1 down (000034)
- **Code removed:** ~350 lines (166 in install.sh, 89 in agent, 70+ in API, 69 in UI)
- **System cleanup:** /opt/filebrowser, /var/lib/jabali-filebrowser, /run/jabali-filebrowser, system user, ACLs
- **Reusable modules:** SSO token logic, reconciler pattern, UDS listener, nginx proxy structure
- **Execution time:** ~30–45 min with proper testing

Execute steps 1–10 in order. Rebuild panel-api & panel-ui after removals. Test nginx reload and verify /files/ redirect behavior before final cleanup.

