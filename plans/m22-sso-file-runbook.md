# M22 SSO-File Runbook (Self-Deleting `jabali-sso-<nonce>.php`)

**Status**: Operator runbook for the M22 rework (replaces the M22 magic-link runbook).
**Design**: ADR-0040 (`docs/adr/0040-m22-sso-file.md`). ADR-0039 superseded.
**Shipped**: 2026-04-21.

## Architecture

The "Log in to admin" button on a ready WordPress install row mints a one-shot SSO file directly in the WP webroot. There is no persistent panel-side WordPress plugin, no callback to the panel, and no signing key.

End-to-end flow:

1. Operator clicks "Log in to admin" on a `Ready` WordPress install row in the panel UI.
2. Panel UI calls `POST /applications/<id>/magic-link/mint` (Kratos-authenticated).
3. Panel-API:
   - Verifies the operator owns the install (or has admin scope).
   - Loads the install row, the domain row (for the host) and the OS user (for `chown`).
   - Calls the agent over its UDS: `wordpress.create_sso_file` with `{install_path, os_user, install_id, admin_username}`.
4. Panel-agent:
   - Generates a 256-bit nonce via `crypto/rand` and base64url-encodes it (43 chars, no padding).
   - Resolves `wp-load.php` (at `install_path` or 2 levels up for subdir installs).
   - Renders the embedded PHP template (`sso_template.php`) with the resolved values.
   - Writes the file atomically (tmp + rename), `chown`s to the install's OS user / `www-data`, `chmod 0440`.
   - Returns `{file_name, expires_at_unix}`.
5. Panel-API composes `https://<domain>[/<subdir>]/jabali-sso-<nonce>.php` and returns `{url, expires_in: 60}`.
6. Panel UI opens the URL in a new tab.
7. The PHP file:
   - Short-circuits known prefetch / preview headers (`Sec-Purpose`, `Purpose`, `X-Moz`, `X-Purpose`) and any non-GET method → 204, no side effects.
   - Calls `flock(LOCK_EX | LOCK_NB)` on `__FILE__`. If the lock can't be acquired, exits 409 (another request is already consuming the file).
   - Checks `filemtime(__FILE__) >= time() - 60`. If older, exits 410 (TTL exceeded — the reaper will pick it up).
   - Loads `wp-load.php`.
   - Resolves the admin via `get_user_by('login', $admin_username)`. Aborts 404 if not found.
   - Calls `wp_set_current_user($user->ID)` + `wp_set_auth_cookie($user->ID, false, is_ssl())`.
   - Sends `Referrer-Policy: no-referrer` and `Cache-Control: no-store` headers.
   - `unlink(__FILE__)` (the directory entry is removed; the open fd survives until the script exits).
   - `error_log("[jabali-sso] install_id=<id>: admin login completed")` (no admin uid, no nonce).
   - `wp_safe_redirect(admin_url(), 302)` then `exit`.
8. Operator lands at `/wp-admin` signed in. The file is gone.

The reaper (`jabali-sso-reaper.timer`, every 30s) walks the `application_installs` table, builds the install path list, scans each path non-recursively for files matching `^jabali-sso-[A-Za-z0-9_-]{43}\.php$` with `mtime` older than 60s, and deletes them. Worst-case stranded lifetime is `60s (TTL) + 30s (interval) = 90s`.

## Operator UX

- The "Log in to admin" button only appears for WordPress installs in `Ready` status.
- Click → new tab opens, redirects to `/wp-admin`.
- Mint failures show a toast in the panel UI; the panel logs include the error code:
  | Code | Meaning |
  |------|---------|
  | `install_not_ready` | The install row's status is not `ready`. Wait or check `jabali app list`. |
  | `unsupported_app_type` | The install isn't WordPress. Other CMS types are not supported by M22. |
  | `user_not_provisioned` | The OS user doesn't exist. Run `jabali user reconcile <id>` and retry. |
  | `admin_user_unresolved` | `application_installs.admin_username` is empty. Set it manually (see Troubleshooting). |
  | `agent_failed` | UDS call to the agent failed. Check `journalctl -u jabali-agent`. |

## Reaper

The reaper is a systemd timer + service pair:

- `jabali-sso-reaper.timer` — fires `OnBootSec=60s`, `OnUnitActiveSec=30s`, `Persistent=true`.
- `jabali-sso-reaper.service` — `Type=oneshot`, `ExecStart=/usr/local/bin/jabali sso-reap`.

The `jabali sso-reap` subcommand:
- Queries `application_installs WHERE app_type='wordpress' AND status IN ('ready','installing','failed')`.
- Joins `domains` to compose each install path (`docroot + subdirectory`, `path.Clean`'d).
- Calls `agent.Call("wordpress.reap_sso_files", {install_paths: [...]})`.
- Logs `sso-reap: scanned=N deleted=M paths=K`.

Inspect activity:

```bash
systemctl status jabali-sso-reaper.timer    # active (waiting), Trigger: Mon ...
systemctl list-timers jabali-sso-reaper.*   # next + last fire time
journalctl -u jabali-sso-reaper.service --since "10 min ago"
```

## Troubleshooting

### Click → blank page or "404"

1. Verify the install path matches the domain: `jabali app list --json | jq '.[] | select(.id=="<id>") | {docroot, subdirectory}'`.
2. Verify nginx serves `.php` for the host (default for jabali-managed sites).
3. The file may have been consumed by a browser prefetcher between mint and click. Click "Log in to admin" again.

### Click → 409 / 410

- 409: another browser window already entered the lock. Click again.
- 410: TTL elapsed (more than 60s between mint and click). Click again.

### `admin_user_unresolved`

Means the install row has no `admin_username`. Causes:
- Pre-M19 install (older app entries didn't store admin username).
- WP install completed but the username write was lost.

Fix:
```sql
UPDATE application_installs
SET admin_username = '<wp-admin-login>'
WHERE id = '<install_id>';
```

Then click again. The reaper does not depend on this column.

### Files accumulating in webroot

If `find /home -name 'jabali-sso-*.php' -mmin +5` returns matches:
1. Verify the timer is active: `systemctl is-active jabali-sso-reaper.timer`.
2. Check the last service run: `journalctl -u jabali-sso-reaper.service -n 50`.
3. The reaper queries the panel DB. If the panel-api / DB is down, the reaper logs the error and the next sweep retries.
4. As a one-shot manual sweep: `/usr/local/bin/jabali sso-reap`.

### Suspicious activity in panel logs

The mint handler logs `magic_link.mint: install_id=<id> file_hash=<sha256-prefix>`. Repeated mints for the same install from the same user may be a UI double-click; repeated mints across many installs from the same operator within seconds may be a script. Cross-reference with Kratos session logs.

The PHP file logs `[jabali-sso] install_id=<id>: admin login completed` to the WP debug log (if `WP_DEBUG_LOG` is on) and to PHP-FPM's `error_log`. No nonce, no uid, no IP — those are implicit in the surrounding webserver access log.

## Rollback

The rework supersedes the original M22 design. Rolling back means reverting all 8 rework steps in reverse order. The `magic_link_tokens` table is dropped by migration `000053`; the down migration recreates the schema but does NOT restore data. Concretely:

1. Revert merges of waves D, C, B, A from `main` (in that reverse order).
2. Run `migrate down 1` to restore the `magic_link_tokens` table schema (empty).
3. Re-run `install.sh install_jabali_wp_mu_plugin` and `install_magic_link_key`.
4. Re-deploy. Verify `/etc/jabali-panel/magic-link.key` exists (mode 0600 root:jabali) and the mu-plugin is at `/usr/local/lib/jabali/wp-mu-plugins/jabali-magic-link.php`.

The original M22 had the 5 connectivity gaps documented in the rework plan §"Why a rework". A rollback restores them; consider whether the original design is operationally tractable before doing this.

## Threat model summary

The full threat model lives in [ADR-0040](../docs/adr/0040-m22-sso-file.md). Top-level summary:

| ID | Threat | Mitigation |
|----|--------|------------|
| T1 | Filename leak in URL (browser history, Referer, log archive) | 256-bit nonce + single-use unlink + 60s TTL + `Referrer-Policy: no-referrer` |
| T2 | Webserver access log persistence | TTL means leaked filenames expire faster than any practical log-mining window |
| T3 | Browser prefetch / prerender / preview consumes file | PHP template short-circuits on any non-empty `Sec-Purpose` (covers prefetch, prerender, `prefetch;prerender`, and future speculation types) + `Purpose: prefetch` + `X-Moz: prefetch` + `X-Purpose: preview` + non-GET → 204 |
| T4 | Race: two requests hit the file at once | `flock(LOCK_EX | LOCK_NB)` — second loses |
| T5 | Reaper deletes a file mid-execution | Open fd survives `unlink`; PHP keeps reading the inode |
| T6 | Hostile user creates lookalike file in own webroot | Strict regex; reaper only sweeps managed install paths; user's own webroot was theirs already |
| T7 | `chown` failure leaves file with wrong owner | Deferred cleanup-on-error in agent's `CreateSSOFile` |
| T8 | Filesystem doesn't support `flock` (NFS) | `statfs` check in agent aborts mint with diagnostic |
| T9 | Audit trail gap | Mint handler logs SHA-256 prefix of filename + install_id; PHP logs install_id on success |

Filename-as-capability is the entire access-control mechanism. We accepted this trade-off explicitly in ADR-0040 §3 (rationale: 2^256 nonce space + 60s TTL + single-use makes brute-force, replay, and reuse all infeasible for any realistic threat model).

## Files / paths

| Path | Purpose |
|------|---------|
| `panel-api/internal/api/magic_link.go` | Mint handler |
| `panel-agent/internal/commands/wordpress_create_sso_file.go` | Writes the SSO file |
| `panel-agent/internal/commands/sso_template.php` | Embedded PHP template |
| `panel-agent/internal/commands/sso_template.go` | Template renderer + nonce generator |
| `panel-agent/internal/commands/sso_reaper.go` | Reaper handler (called by CLI) |
| `panel-api/cmd/server/sso_reap_cmd.go` | `jabali sso-reap` Cobra command |
| `install/systemd/jabali-sso-reaper.{service,timer}` | systemd unit pair |
| `panel-ui/src/hooks/useMagicLink.ts` | UI hook (unchanged wire contract) |
| `panel-ui/tests/e2e/magic-link.spec.ts` | E2E spec (asserts new URL shape) |

Removed by the rework: `panel-api/internal/magiclink/` (whole package), `panel-api/internal/repository/magic_link_token_repository.go`, `panel-api/internal/models/magic_link_token.go`, `install/wp-mu-plugins/jabali-magic-link.php`, `panel-agent/internal/commands/wordpress_magic_link.go`, `MagicLinkKeyPath` config field, `/etc/jabali-panel/magic-link.key` file, `magic_link_tokens` table.
