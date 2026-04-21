# M22 Rework — Self-Deleting SSO File (Installatron / Softaculous Pattern)

**Date**: 2026-04-21
**Author**: shuki + Claude
**Status**: Blueprint — adversarial review folded in 2026-04-21, dispatchable
**Supersedes**: M22 magic-link (mu-plugin + HMAC-callback) shipped 2026-04-21
**Related ADRs**: 0039 (M22 magic-link, will be marked SUPERSEDED), 0040 (M22 SSO file design — this rework, NEW)

---

## Why a rework

M22 magic-link shipped end-to-end 2026-04-21 (Steps 9–11 on `main`). Verification on test VM `10.0.3.13` exposed five separate connectivity / lifecycle gaps that all stem from the **same root cause**: the design requires a persistent panel-side WordPress plugin and a callback over HTTPS from WP back to the panel.

Five gaps surfaced in one validation session:

1. The mu-plugin's "did sed run?" guard contained the literal placeholder strings sed targets. Sed mutated the guard into a self-comparison that always evaluates true → plugin silently no-ops. **Real bug, not just a deployment issue.**
2. `panel-api/cmd/server/update.go` doesn't mirror `install.sh`'s `install_jabali_wp_mu_plugin` step. `jabali update` rebuilds binaries but never deploys the canonical mu-plugin source.
3. Existing WordPress installs (any pre-M22) never get the per-install plugin copy because `installMagicLinkMUPlugin` only runs during a fresh `wp install`. Backfill needs a reconciler step.
4. nginx default vhost on `:443` returns 444 (silent drop) for any path it doesn't explicitly route. The `POST /applications/<id>/magic-link/validate` endpoint registered on the panel root engine is unreachable from the WP plugin without a new `proxy_pass` block.
5. The panel's self-signed cert isn't in the OS CA bundle, so `wp_remote_post` with `sslverify=true` fails with `X509_V_ERR_SELF_SIGNED_CERT_IN_CHAIN`. Either disable sslverify (security loss) or trust the cert at OS level on every fresh install + every update.

All five gaps disappear if the WordPress side has **no persistent panel code** and **no callback to the panel**. That's the Installatron/Softaculous pattern: panel writes a self-contained, self-deleting `sso-<nonce>.php` file to the WP webroot; the file loads `wp-load.php`, claims its single-use slot via `flock` + `unlink`, signs the operator in via `wp_set_auth_cookie`, redirects to `/wp-admin`, then exits. No callback. No mu-plugin. No HMAC key. No nginx routing changes. No CA trust setup.

That model has run at scale across millions of WP installs for ~15 years.

## Goals

1. **Replace** M22 mu-plugin + HMAC-callback design with a self-deleting `jabali-sso-<nonce>.php` file written to the WP webroot per login.
2. **Preserve** the operator UX exactly: same panel button, same wire contract `{url, expires_in}`, same e2e behaviour (operator clicks → new tab → lands in `/wp-admin`).
3. **Delete** the entire validate-callback path: panel-api `magiclink` package, `magic_link_tokens` table, validate handler, mu-plugin, agent's `installMagicLinkMUPlugin`, `install.sh` `install_jabali_wp_mu_plugin` + `install_magic_link_key`, panel-api `MagicLinkKeyPath` config, `/etc/jabali-panel/magic-link.key`.
4. **Add** a panel-agent reaper (systemd timer, every 30s) that prunes `jabali-sso-*.php` files older than 60s as a defence-in-depth against missed self-deletes.
5. **Drop** the entire 5-item M22 follow-up list — every gap becomes moot under the new design.
6. **Document** the rework with ADR-0040 (new) superseding ADR-0039, and a new operator runbook at `plans/m22-sso-file-runbook.md`.

## Non-goals

- **Multi-CMS support.** Like the original M22, this ships only the WordPress path. Joomla/Drupal/etc. remain admin-via-credentials until each gets its own equivalent.
- **Cross-host SSO files.** The agent writes the file to local disk; only works for installs the panel itself manages on the same VM.
- **Per-install signing keys / cryptographic capability separation.** The filename IS the capability; 256 bits of entropy is the only access-control primitive. We accepted this trade-off in the design discussion (Step 1 ADR documents the rationale).
- **Migrating existing magic-link audit data.** The `magic_link_tokens` table is dropped, not preserved. Step 4 includes the migration; M22 was never deployed to real customers, so there's nothing to preserve.

## Branching + commit conventions

Per `CLAUDE.md`: agents commit to feature branches; the dispatcher merges to `main` and pushes. Use the per-step branch slug noted under each step (`m22r/01-adr-sso-file`, `m22r/02-php-template`, etc.). Rebase onto latest `origin/main` before final report; re-run tests post-rebase.

`gh` is unavailable on this Gitea remote — there's no PR command. After commit, the report is just the branch name + commit SHAs + `git log main..<branch>` summary.

## Steps overview

```
Step 1 (ADR + threat model) ──┐
                              │
Step 2 (PHP template + Go     │
        embedding)            ├─→ Step 3 (Agent CreateSSOFile + Reaper)
                              │       │
Step 5 (rm mu-plugin code) ───┘       │
                                      ↓
                                 Step 4 (Mint rewrite — wire contract preserved)
                                      │
                                      ├─→ Step 6 (Delete legacy magiclink pkg + migration)
                                      ├─→ Step 7 (install.sh + update.go cleanup, reaper timer install)
                                      └─→ Step 8 (UI hook + e2e + runbook + VM teardown)
```

Wave plan (revised after adversarial review — Step 5 now depends on Step 2 to avoid `sedSafeULID` race):
- **Wave A** (parallel, no shared files): Steps 1, 2
- **Wave B** (after Wave A): Steps 3, 5 (parallel — Step 2 has copied `sedSafeULID` so Step 5 can delete the source)
- **Wave C** (after Step 3): Step 4
- **Wave D** (after Step 4, parallel): Steps 6, 7, 8

Total: 8 steps, 4 dispatch waves with parallelism within waves.

---

## Step 1 — ADR-0040 + threat model + supersede ADR-0039

**Branch**: `m22r/01-adr-sso-file`
**Model tier**: strongest (design + threat model needs deep reasoning)
**Depends on**: nothing
**Files**:
- `docs/adr/0040-m22-sso-file.md` (NEW)
- `docs/adr/0039-m22-magic-link.md` (status flip: `accepted` → `superseded by 0040`)
- `docs/BLUEPRINT.md` (M22 entry: SHIPPED → IN-FLIGHT-REWORK with link to ADR-0040)

### Context brief

The plan's "Why a rework" section above is the executive summary. The ADR needs to formalise:

1. **Design**: nonce generation (32 bytes from `crypto/rand`, base64url no-padding = 43 chars), filename format (`jabali-sso-<nonce>.php`, total 53 chars), file template (load `wp-load.php`, `flock` claim, `unlink(__FILE__)`, look up admin uid from DB or `get_users(['role'=>'administrator'])`, `wp_set_auth_cookie($uid, false, is_ssl())`, `wp_safe_redirect(admin_url())`, `exit`), file mode (`0440 owner:www-data` — readable by FPM, not writable from PHP), TTL (60s enforced by reaper sweeping older files), single-use enforcement (filesystem inode + flock at top of file).

2. **Threat model** — at minimum:
   - **T1 Filename leak via URL** (Referer, browser history, proxy logs, APM): mitigated by single-use + 60s TTL + Referrer-Policy header set by file before redirect + 256-bit entropy makes brute-force infeasible, no APM/log scrubbing required because the filename is dead within seconds of mint.
   - **T2 Race / double-use** (two simultaneous opens before unlink completes): mitigated by `flock(LOCK_EX | LOCK_NB)` at top of file; second request fails the lock and `exit`s without side effects. Document that `unlink` alone is insufficient because PHP keeps the inode alive on open file handles.
   - **T3 File survives past TTL** (web server crash mid-execution leaves the file on disk): mitigated by reaper sweeping `jabali-sso-*.php` older than 60s every 30s. Defence in depth.
   - **T4 Filename guessing**: 256-bit entropy = 2^256 possibilities; brute-force is computationally infeasible.
   - **T5 Privilege escalation via file content disclosure**: file contents are readable by `www-data` (FPM user) but reveal only the admin uid (already discoverable via `wp-json/wp/v2/users`) and the WP auth code path (public knowledge). No secrets in the file.
   - **T6 Disk-full DoS**: each file is < 4KB; reaper bounds the total at < O(active operators × installs); defence in depth via systemd `MemoryAccounting` + filesystem quotas already in place per ADR-0032.
   - **T7 Operator audit trail loss**: M22's `magic_link_tokens` table provided centralised audit. The new design has agent logs ("created sso file <name> for install <id> at <time>") and panel logs ("operator <user> minted sso file for install <id>"). No DB row. Document that this is a deliberate trade-off; if centralised audit becomes important, add a lightweight `sso_audits` table in a follow-up (file_name + created_at + consumed_at, no key material).
   - **T8 Cross-install replay** (operator clicks "Log in" on install A but the URL points at install B): the URL contains the site hostname; nonce is local to install B's webroot; opening the URL hits install B's web server which finds no matching file → 404. Cross-install replay is impossible because there's no shared state.
   - **T9 Panel compromise blast radius**: same as M22 (panel = single point of failure). Not changed by this rework. Documented for completeness.

3. **Comparison vs ADR-0039**: list every M22 mechanism and either (a) "kept", (b) "removed because moot under new design", or (c) "replaced with X". Make it grep-friendly.

4. **Trade-offs accepted**: no centralised audit log (T7); reliance on filesystem semantics (`flock`, `unlink`) instead of database transactions; 53-char filenames in URLs (vs the 65-char base64 token in M22's URL — slight win); WordPress-only, no protocol portability.

5. **Trade-offs rejected**: per-install signing keys (would compartmentalise panel-compromise blast radius but adds key-management complexity; deferred to a future ADR if compromise concerns drive it).

### Tasks

1. Read ADR-0039 in full to understand what's being superseded.
2. Read ADR-0036 (M16 OIDC, the predecessor before M22) for the supersede chain pattern.
3. Write ADR-0040 with sections: Status, Date, Deciders, Related (links to 0036/0038/0039), Context, Decision, Threat Model (T1–T9 above, each with mitigation + residual risk), Comparison vs ADR-0039 (table form), Trade-offs Accepted, Trade-offs Rejected, Acceptance Criteria.
4. Edit ADR-0039: change status line from `accepted (...)` to `superseded by [ADR-0040](./0040-m22-sso-file.md)` and add a one-paragraph "Why superseded" preamble at the top.
5. Edit `docs/BLUEPRINT.md`: M22 entry status flips from "SHIPPED 2026-04-21" to "IN-FLIGHT-REWORK 2026-04-21 (see ADR-0040)".

### Verification

```bash
# ADR exists and is well-formed
test -f docs/adr/0040-m22-sso-file.md
grep -q '^**Status**:.*accepted' docs/adr/0040-m22-sso-file.md
grep -q 'Supersedes.*0039' docs/adr/0040-m22-sso-file.md

# 0039 marked superseded
grep -q 'superseded by.*0040' docs/adr/0039-m22-magic-link.md

# BLUEPRINT updated
grep -q 'IN-FLIGHT-REWORK' docs/BLUEPRINT.md
```

### Exit criteria

- ADR-0040 exists with all 9 threats documented, each with a mitigation paragraph + residual-risk note.
- ADR-0039 is marked superseded with a forward link to ADR-0040.
- `docs/BLUEPRINT.md` reflects the rework status.
- A grep for the table mapping M22 mechanisms to "kept/removed/replaced" returns hits for at least: mu-plugin, magic_link_tokens table, validate endpoint, magic-link.key, HMAC signer, validate proxy, OS CA trust.

### Rollback

Revert the three file edits. No code or data changed.

---

## Step 2 — PHP file template (Go embed) + nonce generator

**Branch**: `m22r/02-php-template`
**Model tier**: default
**Depends on**: nothing (pure new code, no removals)
**Files**:
- `panel-agent/internal/commands/sso_template.go` (NEW — Go file with `//go:embed` + nonce helper)
- `panel-agent/internal/commands/sso_template.php` (NEW — the actual PHP source, embedded)
- `panel-agent/internal/commands/sso_template_test.go` (NEW — unit tests)

### Context brief

The PHP file written to the WP webroot must be:

1. **Single file**, no includes other than `wp-load.php`.
2. **Self-deleting on first execution** before any side effects.
3. **Race-safe** via `flock(LOCK_EX | LOCK_NB)` — two simultaneous opens won't both log in.
4. **TTL-aware** — checks `filemtime(__FILE__) + 60 < time()` and refuses if expired (defence in depth alongside the reaper).
5. **Privacy-conscious** — sets `Referrer-Policy: no-referrer` before redirect to prevent the filename leaking via Referer to wp-admin's third-party assets.
6. **Audit-loud server-side** — calls `error_log("jabali-sso: signed in admin uid=$uid for install <ID>")` so the WP error log retains a record even though the file vanishes.

Embed the PHP via Go's `//go:embed` directive so the agent ships one binary with the template baked in. No filesystem dependency for the template at runtime.

### PHP template skeleton (target shape)

```php
<?php
// jabali-sso-<NONCE>.php — auto-generated by jabali-agent at <TIMESTAMP>.
// Self-deleting single-use admin login. See ADR-0040.

declare(strict_types=1);

// 1. Reject browser prefetch / link preview before doing anything that
//    consumes single-use state. (Adversarial review finding #11.)
$secPurpose = $_SERVER['HTTP_SEC_PURPOSE'] ?? '';
$purpose    = $_SERVER['HTTP_PURPOSE']     ?? '';
$xMoz       = $_SERVER['HTTP_X_MOZ']       ?? '';
$xPurpose   = $_SERVER['HTTP_X_PURPOSE']   ?? '';
if ($secPurpose === 'prefetch'
 || $purpose === 'prefetch'
 || stripos($xMoz, 'prefetch') !== false
 || $xPurpose === 'preview'
 || ($_SERVER['REQUEST_METHOD'] ?? 'GET') !== 'GET') {
    http_response_code(204);
    exit;
}

// 2. Atomic lock claim. Second concurrent request fails LOCK_NB and exits
//    without side effects. Releasing the lock happens implicitly at script end.
$lock = @fopen(__FILE__, 'r');
if ($lock === false || !flock($lock, LOCK_EX | LOCK_NB)) {
    http_response_code(409);
    exit;
}

// 3. TTL check (defence in depth — reaper also sweeps every 30s, so worst
//    case the file lives 90s before unlink, but the inline guard fails the
//    request immediately if mtime is already past TTL).
if (filemtime(__FILE__) + __TTL_SECONDS__ < time()) {
    http_response_code(410);
    exit;
}

// 4. Load WordPress. wp-load.php is one directory up from the webroot when
//    WP is installed at the docroot; if a subdirectory install, the agent
//    substitutes __WP_LOAD_PATH__ accordingly.
define('SHORTINIT', false);
require_once __WP_LOAD_PATH__;

// 5. Look up the install's admin user. The agent embeds the uid at write
//    time (read from the install row's `admin_user_id` column, populated
//    during M10 wordpress install — NOT a live SQL pattern-match against
//    serialised wp_capabilities).
$admin_uid = __ADMIN_UID__;
$user = get_userdata($admin_uid);
if (!$user || !user_can($user, 'manage_options')) {
    error_log('jabali-sso: admin lookup failed for install __INSTALL_ID__');
    http_response_code(500);
    exit;
}

// 6. Sign in. Don't set "remember me" — this is a one-shot operator session.
wp_set_current_user($admin_uid);
wp_set_auth_cookie($admin_uid, false, is_ssl());

// 7. Audit on the WP side. Don't log the admin uid — wp-content/debug.log
//    can be world-readable when WP_DEBUG_LOG is on. The panel + agent
//    already capture the mint with full context server-side.
//    (Adversarial review finding #7.)
error_log('jabali-sso: admin login completed for install __INSTALL_ID__');

// 8. Don't leak the filename to wp-admin's third-party assets via Referer.
header('Referrer-Policy: no-referrer');

// 9. Unlink LAST — after we've successfully set the auth cookie. If a
//    transient failure (PHP fatal mid-script, web server crash) interrupts
//    the request between now and the redirect, the file is still on disk
//    and the operator can retry. The reaper sweeps the file within 90s
//    even if no retry happens. (Adversarial review finding #12.)
@unlink(__FILE__);

// 10. Redirect to /wp-admin and exit.
wp_safe_redirect(admin_url());
exit;
```

Substituted placeholders at write time (Go `text/template` or simple string replace):
- `__TTL_SECONDS__` → integer (60)
- `__WP_LOAD_PATH__` → string literal (PHP-escaped path to `wp-load.php`)
- `__ADMIN_UID__` → integer
- `__INSTALL_ID__` → string literal (ULID, sed-safe)
- `__NONCE__` → embedded in the comment line for grep-ability

The placeholder names use double-underscore so a hostile WP plugin can't shadow them via `define()` collision.

### Nonce generator

```go
// GenerateNonce returns 32 bytes of crypto/rand encoded as base64url
// no-padding (43 chars). 256 bits of entropy.
func GenerateNonce() (string, error) {
    var b [32]byte
    if _, err := rand.Read(b[:]); err != nil {
        return "", fmt.Errorf("crypto/rand: %w", err)
    }
    return base64.RawURLEncoding.EncodeToString(b[:]), nil
}
```

### Tasks

1. Create `panel-agent/internal/commands/sso_template.php` with the skeleton above.
2. Create `panel-agent/internal/commands/sso_template.go` that:
   - Embeds the PHP via `//go:embed sso_template.php`
   - Exports `GenerateNonce() (string, error)`
   - Exports `RenderSSOTemplate(nonce, wpLoadPath, installID string, adminUID int) (string, error)` that does the placeholder substitution (use `strings.Replace` with explicit count = -1; do NOT use `text/template` because `{{` syntax could collide with PHP echo blocks).
3. Validate inputs in `RenderSSOTemplate`:
   - `nonce` must match `^[A-Za-z0-9_-]{43}$`
   - `wpLoadPath` must be an absolute path ending in `wp-load.php` and contain only `[A-Za-z0-9_/.\-]` (PHP string-literal-safe)
   - `installID` must be a 26-char Crockford ULID (reuse `sedSafeULID` from the soon-to-be-deleted `wordpress_magic_link.go` — copy the validator before the deletion lands)
   - `adminUID` must be > 0 and < 2^31
4. Unit tests:
   - `GenerateNonce` returns 43 chars, base64url alphabet, distinct on repeat calls.
   - `RenderSSOTemplate` rejects each invalid input with a clear error.
   - Rendered output contains the substituted values and no remaining `__*__` markers.
   - Rendered output passes a basic syntax check via `php -l` (skip the test if `php` isn't on PATH — `t.Skip` with reason).

### Verification

```bash
cd panel-agent
go test ./internal/commands/ -run SSOTemplate -v
go vet ./internal/commands/
```

### Exit criteria

- `sso_template.php` is syntactically valid PHP (verified via `php -l` if available, or via the embed compiling in Go).
- `GenerateNonce` produces 43-char base64url strings.
- `RenderSSOTemplate` enforces all four input validators.
- All unit tests pass.
- No imports of the soon-to-be-deleted `magiclink` package.

### Rollback

Delete the three new files. No edits to existing code.

---

## Step 3 — Agent commands: CreateSSOFile + Reaper

**Branch**: `m22r/03-agent-create-sso-and-reaper`
**Model tier**: default
**Depends on**: Step 2 (uses `RenderSSOTemplate` + `GenerateNonce`)
**Files**:
- `panel-agent/internal/commands/wordpress_create_sso_file.go` (NEW)
- `panel-agent/internal/commands/wordpress_create_sso_file_test.go` (NEW)
- `panel-agent/internal/commands/sso_reaper.go` (NEW)
- `panel-agent/internal/commands/sso_reaper_test.go` (NEW)
- `panel-agent/internal/agent/dispatch.go` or wherever commands register (EDIT — wire the two new commands)

### Context brief

Two new agent commands.

**`wordpress.create_sso_file`** — input: `{install_path, os_user, install_id, admin_uid}`. Output: `{file_name, expires_at_unix}`. Steps:

1. Resolve `wp_load_path`: if `<install_path>/wp-load.php` exists, use that. Otherwise, walk up to find it (handles WP-in-subdirectory installs where `install_path` is the docroot like `/home/<user>/domains/<site>/public_html/blog/`).
2. `nonce, _ := GenerateNonce()`
3. `body, _ := RenderSSOTemplate(nonce, wpLoadPath, installID, adminUID)`
4. `fileName := "jabali-sso-" + nonce + ".php"`
5. Write the file atomically: write to `<install_path>/.<fileName>.tmp` with `O_CREAT|O_EXCL|O_WRONLY`, fsync, then `rename` to `<install_path>/<fileName>`. Atomic rename guarantees no half-written file is ever visible.
6. `chown osUser:www-data` and `chmod 0440` on the renamed file. Mode 0440 = owner read, group read, no write, no execute. PHP-FPM (group www-data) can read; even the user can't accidentally edit it.
7. **Cleanup-on-error guarantee** (adversarial review finding #2): wrap steps 5–6 in a deferred `os.Remove` that fires unless an explicit "success" flag is set after step 6 completes. If `os.Chown` fails (user removed mid-mint, EPERM, etc.), the file is removed before returning — never leave a partial / wrong-owner file on disk. Test: pass `os_user="nonexistent"` → expect error AND no file at expected path.
8. Return `{file_name: <fileName>, expires_at_unix: <now + 60>}`.

**`wordpress.reap_sso_files`** — input: `{}`. Output: `{deleted_count, scanned_count}`. Steps:

1. **Path discovery** (adversarial review finding #1): the agent loads the list of WP install paths from `application_installs` rows where `app_type='wordpress' AND status IN ('ready','installing','failed')`. Build path = `<install row's web_root>` for each. This handles subdirectory installs natively (the install row's path is the actual docroot the SSO file lives in) and avoids relying on a fixed `/home/*/domains/*/public_html` glob that misses subdir installs. Fallback for installs whose row was deleted but files lingered: also walk `/home/*/domains/*/public_html/` non-recursively as a second pass and apply the strict regex below.
2. For each install path, walk the directory non-recursively and filter filenames against the **strict regex** `^jabali-sso-[A-Za-z0-9_-]{43}\.php$` (adversarial review finding #6 — prevents the reaper from deleting hostile lookalike files like `jabali-sso-evil.php` a user might create).
3. For each match: stat, check `mtime + 60 < now`, if yes `os.Remove`. Skip with a logged warning on permission errors (don't fail the whole sweep).
4. Bounded: log a warning if the sweep finds more than 100 stale files (something is wrong — operator should look).
5. Return `{deleted_count: N, scanned_count: M}` for observability.

How the agent gets the install list without re-implementing the panel: either (a) the agent caches the list in memory refreshed every 5 min from a panel-api `/internal/installs/paths` endpoint, or (b) the reaper command takes the list as an input parameter and the panel-api passes it in via the existing dispatch socket. Option (b) is simpler — adopt that. The reaper systemd timer triggers a tiny panel-side script that calls `panel-api → enumerate paths → agent.reap_sso_files(paths=[...])`. Document this in Step 7's timer wiring.

### Tasks

1. Add the two commands as Go functions in their own files.
2. Wire them into the agent's command dispatch table.
3. Unit tests for `CreateSSOFile`:
   - Happy path: tmpdir + fake `wp-load.php`, command runs, file exists at expected path, mode is 0440, content contains the substituted ULID, fileName matches `^jabali-sso-[A-Za-z0-9_-]{43}\.php$`.
   - `wp-load.php` missing: returns clear error.
   - `install_path` not absolute: returns clear error.
   - chown failure (simulated by passing `os_user="nobody-doesnotexist"`): returns clear error and leaves no partial file behind.
4. Unit tests for `Reaper`:
   - Empty glob: returns `{deleted_count: 0}`.
   - Mix of stale + fresh files: only stale are deleted.
   - Permission denied on one file: logs warning, sweep continues.
5. Update the agent's `system.info` or `agent_status` command (if such a thing exists) to expose "last reaper run" timestamp for debugging — optional, low priority, can defer.

### Verification

```bash
cd panel-agent
go test ./internal/commands/ -run 'CreateSSOFile|Reaper' -v
go vet ./...
go build ./cmd/jabali-agent
```

### Exit criteria

- Both commands compile, unit tests green.
- `CreateSSOFile` writes a file with the exact name format the panel-api expects (Step 4 mints the URL using this format).
- `Reaper` is idempotent and handles permission errors gracefully.
- No new dependencies introduced (use stdlib only — `os`, `path/filepath`, `crypto/rand`, `encoding/base64`).

### Rollback

Delete the four new files + revert the dispatch.go edit.

---

## Step 4 — Panel-api mint handler rewrite (wire contract preserved)

**Branch**: `m22r/04-mint-rewrite`
**Model tier**: default
**Depends on**: Step 3 (calls the new agent commands)
**Files**:
- `panel-api/internal/api/magic_link.go` (REWRITE — keep file, replace mint impl, delete validate handler + RegisterMagicLinkRoutes' second call, delete handlers struct fields no longer needed)
- `panel-api/internal/app/app.go` (EDIT — remove `MagicLinkKeys` and `MagicLinkTokens` from deps, drop validate route registration if separately wired)
- `panel-api/internal/api/magic_link_test.go` (REWRITE — test new mint behaviour)

### Context brief

The mint endpoint stays at `POST /api/v1/applications/:id/magic-link`. The wire response stays `{url, expires_in}`. Everything else changes.

New mint handler:

1. **Auth**: Kratos cookie (existing middleware on the v1 group).
2. **Ownership**: load the install row by `:id`, verify `panel_user_id == current_user.id` OR caller is admin (existing pattern in the codebase — copy from the previous mint).
3. **Resolve site URL** (adversarial review finding #13): load the install's domain row. If `application_installs.subdirectory` is non-empty, the URL must be `https://<domain>/<subdirectory>/<file_name>`; otherwise `https://<domain>/<file_name>`. Apply `path.Clean` defensively; the install row's subdirectory column is operator-supplied and went through validation at install time but defence in depth is cheap. Document the subdirectory branch in the test cases.
4. **Resolve admin uid** (adversarial review finding #5): the install row's `admin_user_id` column was populated during M10 WordPress install. Read it directly — do NOT pattern-match the serialised `wp_capabilities` meta at mint time (multisite uses `<prefix>_<blog_id>_capabilities`, custom prefixes break LIKE patterns, and serialisation format is fragile). If the column is NULL on legacy installs, fall back to a one-time live lookup that respects `db_prefix` from the install row, handles multisite via the install row's `is_multisite` + `blog_id` columns if present, persists the result back to `admin_user_id`, then proceeds. If the live lookup also returns nothing, return 502 with a specific error code `admin_user_unresolved` and an actionable detail message naming the install — runbook documents the manual recovery (operator sets the install row's `admin_user_id` column to the right uid and retries the mint). Add a unit test for the NULL-column fallback path.
5. **Call agent**: `wordpress.create_sso_file` with `{install_path, os_user, install_id, admin_uid}`. Agent returns `{file_name, expires_at_unix}`.
6. **Build URL**: per step 3 above (subdirectory-aware).
7. **Audit log**: panel-side `slog.Info("sso_file minted", "operator", ..., "install_id", ..., "file_name", "<redacted>", "expires_at", ...)`. Don't log the full filename — log a hash prefix or just the install_id.
8. **Return**: `{url: "<full URL>", expires_in: 60}`.

Delete:
- The validate handler (`POST /applications/:id/magic-link/validate` mounted on root engine).
- All references to `magiclink.Keys`, `magiclink.Verify`, `MagicLinkTokenRepository`, `MagicLinkKeys`, `MagicLinkTokens` in `MagicLinkHandlerConfig`.
- The `RegisterMagicLinkRoutes` second argument (`root *gin.Engine`) — only needs the `v1` group now.

### Tasks

1. Rewrite `magic_link.go`: new `mint` handler, new minimal `MagicLinkHandlerConfig` (just the agent client + install repo + WP-DB-credentials lookup), simplified `RegisterMagicLinkRoutes(v1 *gin.RouterGroup, cfg MagicLinkHandlerConfig)`.
2. Update `app/app.go` to wire the new config (remove the two old deps).
3. Rewrite tests:
   - Happy path: returns `{url, expires_in: 60}` with URL matching `^https://.+/jabali-sso-[A-Za-z0-9_-]{43}\.php$`.
   - Auth missing: 401.
   - Cross-user mint: 404 (don't leak existence).
   - Install not in `ready` status: 409.
   - Agent returns error: 502 with no leaked details.
4. Verify wire contract still matches `panel-ui/src/hooks/useMagicLink.ts`'s `MagicLinkResponse` type (`{url, expires_in}`).

### Verification

```bash
cd panel-api
go test ./internal/api/... -run MagicLink -v
go vet ./...
go build ./cmd/server
```

### Exit criteria

- Mint returns the new URL shape.
- Validate endpoint is gone (no route registered, returns 404).
- No imports of `magiclink` package or `MagicLinkTokenRepository` remain in `panel-api/internal/api/` or `app/`.
- All tests green including the existing identity / auth tests.
- `go build` produces a clean binary.

### Rollback

Revert the three files. The `magiclink` package and `MagicLinkTokenRepository` are still in the tree at this point (Step 6 deletes them), so a revert restores the original behaviour.

---

## Step 5 — Delete mu-plugin code (parallel to Steps 2–4)

**Branch**: `m22r/05-delete-mu-plugin`
**Model tier**: default
**Depends on**: nothing (deletes only — and the deleted code has no callers from the new path)
**Files**:
- `install/wp-mu-plugins/jabali-magic-link.php` (DELETE)
- `install/wp-mu-plugins/` (DELETE if empty after — likely yes)
- `panel-agent/internal/commands/wordpress_magic_link.go` (DELETE)
- `panel-agent/internal/commands/wordpress_install.go` (EDIT — remove lines 455–470 that call `installMagicLinkMUPlugin` + the `PanelHost`/`InstallID` request fields if used only there)
- `panel-api/internal/api/wordpress.go` (EDIT — remove the `payload["panel_host"]` and `payload["install_id"]` lines around 1009–1011 + the `PanelHost` field on `WordPressHandlerConfig` if unused after)
- `panel-api/internal/app/app.go` (EDIT — remove `PanelHost: cfg.Server.Hostname` plumbing on the WordPressHandlerConfig if no other consumer)

### Context brief

The mu-plugin is dead code under the new design. Two callsites need to go:

1. The agent's `wordpress.install` command calls `installMagicLinkMUPlugin` after the WP install completes.
2. The panel-api's `createAndKickAgent` plumbs `PanelHost` + `InstallID` into the `wordpress.install` agent payload specifically for the mu-plugin step.

Both go away. **However**, `sedSafeULID` from `wordpress_magic_link.go` may still be needed by Step 2's `RenderSSOTemplate` validator. Step 2 already copies it, so the deletion in this step is safe.

Check before deleting `WordPressHandlerConfig.PanelHost` — `grep -rn "PanelHost" panel-api/` to confirm no other consumer. If something else uses it, leave it.

### Tasks

1. `git rm install/wp-mu-plugins/jabali-magic-link.php` and `rmdir install/wp-mu-plugins` if empty.
2. `git rm panel-agent/internal/commands/wordpress_magic_link.go`.
3. Edit `wordpress_install.go`:
   - Remove the `installMagicLinkMUPlugin` call (~lines 455–470).
   - Remove the `PanelHost` and `InstallID` fields from the request struct if M22-only — `grep` for other consumers first.
4. Edit `panel-api/internal/api/wordpress.go`:
   - Remove `payload["panel_host"]` and `payload["install_id"]` plumbing (~lines 1009–1011).
   - Remove `WordPressHandlerConfig.PanelHost` if no other consumer.
5. Edit `panel-api/internal/app/app.go`:
   - Remove `PanelHost: cfg.Server.Hostname` from the WordPressHandlerConfig initializer if the field was removed.
6. Run `go build ./...` to catch any missed references.

**Wave dependency note** (folded from adversarial review finding #8): Step 5 depends on Step 2 having committed `sedSafeULID` into the new `sso_template.go` first. Step 5 deletes the original definition in `wordpress_magic_link.go`; without Step 2's copy landing first, Step 5's deletion would leave the soon-to-be-deleted `sedSafeULID` callers in a half-state. Per the revised wave plan, Step 2 is in Wave A and Step 5 is in Wave B — this ordering is enforced by the dispatcher.

### Verification

```bash
test ! -e install/wp-mu-plugins/jabali-magic-link.php
test ! -e panel-agent/internal/commands/wordpress_magic_link.go
cd panel-api && go build ./... && go vet ./...
cd ../panel-agent && go build ./... && go vet ./...
grep -rn "installMagicLinkMUPlugin\|jabali-magic-link" panel-agent/ panel-api/ install/ || echo "ok: no references"
```

### Exit criteria

- All grep references to the mu-plugin and its install command are gone (except the deletion entries in `git log`).
- Both Go modules build cleanly.
- `wordpress_install.go` no longer calls the deleted command.
- `WordPressHandlerConfig` is smaller by one field (or unchanged if `PanelHost` had other consumers).

### Rollback

`git revert <commit>` restores both files and the callsite.

---

## Step 6 — Delete legacy magiclink Go package + drop magic_link_tokens migration

**Branch**: `m22r/06-delete-legacy-magiclink`
**Model tier**: default
**Depends on**: Step 4 (mint must already be migrated off these symbols)
**Files**:
- `panel-api/internal/magiclink/key.go` (DELETE)
- `panel-api/internal/magiclink/signer.go` (DELETE)
- `panel-api/internal/magiclink/verifier.go` (DELETE)
- `panel-api/internal/magiclink/*_test.go` (DELETE)
- `panel-api/internal/magiclink/` (DELETE entire directory)
- `panel-api/internal/repository/magic_link_token_repository.go` (DELETE)
- `panel-api/internal/repository/magic_link_token_repository_test.go` (DELETE)
- `panel-api/internal/models/magic_link_token.go` (DELETE)
- `panel-api/cmd/server/serve.go` (EDIT — remove `magiclink.Load` call + the `MagicLinkKeys` and `MagicLinkTokens` field plumbing on `deps`)
- `panel-api/internal/config/config.go` (EDIT — remove `MagicLinkKeyPath` field from `SSOConfig` and any references)
- `panel-api/internal/db/migrations/000053_drop_magic_link_tokens.up.sql` (NEW — `DROP TABLE IF EXISTS magic_link_tokens;`)
- `panel-api/internal/db/migrations/000053_drop_magic_link_tokens.down.sql` (NEW — recreate the table from migration 000052 verbatim, even though we won't roll back; keeps the migration framework happy)

### Context brief

After Step 4, the panel-api mint handler no longer references the `magiclink` package, the `MagicLinkTokenRepository`, or the `MagicLinkToken` model. Now they can be deleted.

The migration drops the table. Down migration recreates it for completeness (`golang-migrate` requires both files), but a real rollback would require also reverting the Step 4 mint handler — the down migration alone wouldn't restore the system. Concretely: `000053.down.sql` is byte-identical to `000052.up.sql` (the original CREATE TABLE statement). Implementer should `cp 000052_magic_link_tokens.up.sql 000053_drop_magic_link_tokens.down.sql`.

**TOML config field removal note** (adversarial review finding #3): the project uses BurntSushi TOML which silently ignores unknown fields by default. Operators with `magic_link_key_path = "..."` left in `/etc/jabali/config.toml` post-deployment will not see any error — the field is silently ignored. No operator action is required, but the runbook (Step 8) should mention that operators may optionally clean up the dead config line.

**Migration ordering note** (adversarial review finding #4): there is no deployment ordering constraint. Step 6's panel-api works whether `magic_link_tokens` is present or absent — it never queries the table. Migrations can run before or after Step 6's panel-api deploys. The teardown doc (Step 8) makes this explicit.

### Tasks

1. `git rm` the entire `panel-api/internal/magiclink/` directory.
2. `git rm` the repository + model files.
3. Edit `serve.go`: remove the `if cfg.SSO.MagicLinkKeyPath != ""` block that loaded keys at boot, remove `deps.MagicLinkKeys` + `deps.MagicLinkTokens` assignments.
4. Edit `config.go`: remove `MagicLinkKeyPath string` from `SSOConfig`.
5. Write the migration pair. The up file is one line (`DROP TABLE IF EXISTS magic_link_tokens;`). Copy the down file verbatim from `000052_magic_link_tokens.up.sql`.
6. Run `go build ./...` and `go test ./...` to catch any missed references.

### Verification

```bash
test ! -d panel-api/internal/magiclink
test ! -e panel-api/internal/repository/magic_link_token_repository.go
test ! -e panel-api/internal/models/magic_link_token.go
grep -rn "magiclink\.\|MagicLinkKey\|MagicLinkToken\|magic_link_token" panel-api/ || echo "ok: no references"

cd panel-api
go build ./... && go vet ./... && go test ./...
```

### Exit criteria

- The `magiclink` package directory is gone.
- The repository, model, and config field are gone.
- `serve.go` no longer loads keys at boot — no `magiclink.Load` call survives.
- `panel-api` builds and tests pass.
- Migration 000053 is paired (up + down), `up.sql` is a single `DROP TABLE` statement.

### Rollback

`git revert <commit>` restores all files. The migration up needs a manual re-run.

---

## Step 7 — install.sh + update.go cleanup, reaper systemd timer install

**Branch**: `m22r/07-install-sh-reaper-timer`
**Model tier**: default
**Depends on**: Step 3 (reaper command exists in the agent), Step 5 (mu-plugin removed so install.sh staging is moot)
**Files**:
- `install.sh` (EDIT — delete `install_jabali_wp_mu_plugin` (~line 2334), delete `install_magic_link_key` (~line 2304), delete both calls in `main` (~line 2628), add `install_sso_reaper_timer`)
- `panel-api/cmd/server/update.go` (EDIT — make sure no stale references to the deleted install steps; nothing to add for the reaper because systemd timer enable is one-shot)
- `install/systemd/jabali-sso-reaper.service` (NEW — systemd unit running `jabali-agent` with the reap-sso-files command)
- `install/systemd/jabali-sso-reaper.timer` (NEW — `OnUnitActiveSec=30s`, `OnBootSec=30s`, `Persistent=true`)
- `/etc/jabali-panel/magic-link.key` cleanup is documented in Step 8's runbook (operator removes it on existing VMs)

### Context brief

`install.sh` currently has two M22-specific functions that need to go: `install_jabali_wp_mu_plugin` (stages mu-plugin source) and `install_magic_link_key` (provisions HMAC key). Both are dead under the new design.

Add a third function: `install_sso_reaper_timer` that:
1. Writes `install/systemd/jabali-sso-reaper.{service,timer}` to `/etc/systemd/system/`.
2. `systemctl daemon-reload`.
3. `systemctl enable --now jabali-sso-reaper.timer`.
4. Logs success with `_ok`.

The reaper service runs as `jabali` (the panel service user) since the agent already runs as root via privileged socket — actually the reaper deletes files in `/home/<user>/domains/`, so it needs root or appropriate capabilities. Run as `root` since the agent is root anyway and the reaper is just a glob+stat+unlink loop.

The reaper service spec:
```ini
[Unit]
Description=Jabali SSO file reaper (sweeps stale jabali-sso-*.php files)
After=jabali-agent.service
Requires=jabali-agent.service

[Service]
Type=oneshot
ExecStart=/usr/local/bin/jabali-agent reap-sso-files
# Or, if the agent is socket-only: ExecStart=/usr/local/bin/jabali-cli reap-sso-files
StandardOutput=journal
StandardError=journal
```

The timer spec:
```ini
[Unit]
Description=Run jabali SSO file reaper every 30s

[Timer]
OnBootSec=30s
OnUnitActiveSec=30s
Persistent=true
Unit=jabali-sso-reaper.service

[Install]
WantedBy=timers.target
```

### Tasks

1. Edit `install.sh`: remove `install_jabali_wp_mu_plugin` function + its call in `main`. Remove `install_magic_link_key` + its call.
2. Add `install_sso_reaper_timer` function near the other systemd-installing helpers. Wire it into `main` after the agent is installed.
3. Write the two systemd unit files in `install/systemd/`.
4. Edit `update.go`: ensure no references to the removed install.sh functions remain (search the static-assets sync block + any "Re-run install dependencies" comments). Add a one-liner to refresh the systemd timer files on update so the reaper config is updateable.
5. Test on the test VM: re-run `jabali update` (or `bash install.sh`), verify `systemctl status jabali-sso-reaper.timer` is active, journalctl shows reaper firing every 30s.

### Verification

```bash
# install.sh sanity (run on a real or staging host)
grep -c 'install_jabali_wp_mu_plugin\|install_magic_link_key' install.sh
# expected: 0

grep -c 'install_sso_reaper_timer' install.sh
# expected: 2 (function definition + main call)

# Systemd units exist
test -f install/systemd/jabali-sso-reaper.service
test -f install/systemd/jabali-sso-reaper.timer

# On the test VM:
ssh root@10.0.3.13 'systemctl status jabali-sso-reaper.timer'
# expected: active (waiting)

ssh root@10.0.3.13 'journalctl -u jabali-sso-reaper.service --since "2 min ago" | grep -c "reaper"'
# expected: > 0
```

### Exit criteria

- `install.sh` no longer references either deleted function.
- `install.sh` provisions the reaper timer on fresh install.
- `update.go` re-installs the timer on existing hosts.
- On the test VM, the reaper timer is active and fires on schedule.

### Rollback

`git revert` the install.sh + systemd units. Disable the timer manually: `systemctl disable --now jabali-sso-reaper.timer && rm /etc/systemd/system/jabali-sso-reaper.{service,timer} && systemctl daemon-reload`.

---

## Step 8 — Panel-UI hook + e2e + runbook + VM teardown doc

**Branch**: `m22r/08-ui-docs-runbook`
**Model tier**: default
**Depends on**: Step 4 (URL shape known)
**Files**:
- `panel-ui/src/hooks/useMagicLink.ts` (no change expected — wire contract preserved; add a one-line jsdoc comment noting the underlying mechanism changed)
- `panel-ui/src/hooks/useMagicLink.test.ts` (EDIT — update the mocked URL in success cases to match the new shape)
- `panel-ui/tests/e2e/magic-link.spec.ts` (EDIT — assert URL ends with `/jabali-sso-<43chars>.php`)
- `plans/m22-magic-link-runbook.md` (DELETE)
- `plans/m22-sso-file-runbook.md` (NEW — operator runbook)
- `plans/m22-rework-vm-teardown.md` (NEW — steps to clean up the old M22 design on the existing test VM)

### Context brief

The panel UI shouldn't need behavioural changes — the hook posts to the same endpoint and gets back the same `{url, expires_in}` shape. Only the URL it opens looks different.

Tests need to be updated to match. E2E spec needs to assert the new URL shape.

The runbook documents:
- How the mint flow works (operator clicks → panel mints → agent writes → operator gets URL → opens → file deletes itself → operator in `/wp-admin`).
- How the reaper works (every 30s, sweeps `jabali-sso-*.php` older than 60s).
- Troubleshooting: what to do if the file doesn't get cleaned up; what to do if mint fails; how to spot suspicious activity in panel logs.
- Rollback procedure (revert all 8 steps in reverse order; the down migration recreates the table but the system still won't work without a Step 4 revert).

The VM teardown doc covers the existing `10.0.3.13` test VM. Order matters (adversarial review finding #9 — deploy first, then clean up orphaned files):

1. **Deploy first**: `jabali update` on the VM. This pulls the post-rework binaries (no magiclink package, no boot-time key load) and runs migration 000053 which drops the `magic_link_tokens` table. The new panel-api works fine whether the table is present or absent.
2. After the deploy completes, verify boot logs show no errors related to magic-link or sso: `journalctl -u jabali-panel --since "5 min ago" | grep -iE 'magic.link|sso' | head`.
3. `rm -f /etc/jabali-panel/magic-link.key` (was world-unreadable; rm to clean up).
4. `rm -rf /usr/local/lib/jabali/wp-mu-plugins/` (canonical mu-plugin source).
5. `find /home -path '*/wp-content/mu-plugins/jabali-magic-link.php' -delete` (per-install mu-plugin copies — each install that was created post-M22-original may have one; the find is non-recursive across users by design).
6. Optional: `sed -i '/magic_link_key_path/d' /etc/jabali/config.toml` (the dead config line; BurntSushi TOML ignores it but cleanup is hygienic).
7. Verify the reaper timer is active: `systemctl status jabali-sso-reaper.timer` → `active (waiting)`.
8. Smoke test: click "Log in to admin" on a ready WP install row, verify new tab opens at `https://<site>/jabali-sso-<43chars>.php`, verify redirect to `/wp-admin`.

**E2E verification scope** (adversarial review finding #10): the Playwright spec must do more than assert URL shape. Required assertions:
- POST mint endpoint, get `{url, expires_in: 60}`.
- Verify URL matches `^https://[^/]+(/.+)?/jabali-sso-[A-Za-z0-9_-]{43}\.php$`.
- In an incognito browser context, navigate to the URL.
- Assert HTTP redirect (3xx) to `/wp-admin`.
- Assert the `wordpress_logged_in_*` cookie is set after the redirect.
- Assert a request to `/wp-admin` returns the dashboard (200, contains "Dashboard" string or admin nav markup).
- E2E setup note: requires a running panel-api + a ready WordPress install. Local devs can use the test VM (10.0.3.13) once teardown is complete; CI uses the existing Docker fixture (cross-reference `.gitea/workflows/ci.yml` for the WP+panel container setup).

### Tasks

1. Update `useMagicLink.test.ts`: change the mocked URLs in success-case tests from `https://example.com/?jabali_admin_login=token123` to `https://example.com/jabali-sso-<43charstub>.php`.
2. Update `magic-link.spec.ts`: change the URL assertion to match the new shape (regex `/\/jabali-sso-[A-Za-z0-9_-]{43}\.php$/`).
3. Delete `plans/m22-magic-link-runbook.md`.
4. Write `plans/m22-sso-file-runbook.md` with sections: Architecture, Operator UX, Reaper, Troubleshooting, Rollback, Threat Model Summary (link to ADR-0040).
5. Write `plans/m22-rework-vm-teardown.md` with the 6 cleanup steps above.
6. Run vitest + the e2e spec headlessly to confirm tests pass.

### Verification

```bash
cd panel-ui
npm run test -- --run useMagicLink
# expected: all green

npx tsc --noEmit
# expected: clean

# E2E: requires running panel + a WP install — can defer to manual verification
npx playwright test tests/e2e/magic-link.spec.ts --headed
# expected: green if a WP install is reachable
```

### Exit criteria

- vitest green for `useMagicLink.test.ts`.
- `tsc --noEmit` clean.
- New runbook + VM teardown doc exist.
- Old runbook deleted.
- E2E spec asserts the new URL shape.

### Rollback

Revert the test + doc edits. The hook itself didn't change.

---

## Cross-step invariants (verified after EVERY step)

1. **`go build ./...` + `go vet ./...` clean** in both `panel-api` and `panel-agent`.
2. **`npx tsc --noEmit` clean** in `panel-ui`.
3. **No reference to deleted symbols** from the still-living code: after each step, run `grep -rn '<deleted symbol>' panel-api/ panel-agent/ install/` and confirm zero hits in non-test, non-doc code.
4. **`gitnexus_detect_changes` reports only the expected affected symbols** — the per-step files-touched manifest is the authoritative list.
5. **No `magic-link.key` or `mu-plugins/jabali-magic-link.php` reference survives in any file other than ADR-0039 (which becomes a historical record)**.

## Risk register (revised after adversarial review)

| Risk | Likelihood | Impact | Mitigation |
|---|---|---|---|
| `flock` semantics differ between filesystems (NFS vs ext4) | low | medium | Doc says ext4/xfs only; abort if FS is NFS-mounted. Add a check in the agent's `CreateSSOFile` (`statfs` for `f_type`). |
| Reaper deletes a file an in-flight request is still using | very low | low | The in-flight PHP process holds an open fd; `unlink` removes the directory entry but PHP keeps reading the inode. Reaper is harmless mid-execution. |
| `wp_load.php` not at expected path (subdirectory installs) | medium | high | Step 3's `CreateSSOFile` walks up from `install_path` to find it. Step 4's URL builder consults the install row's `subdirectory` column. Document the search algorithm in the agent's logs. |
| Admin uid lookup fails (custom roles, plugins remove `manage_options`) | low | high | Step 4 reads `admin_user_id` from the install row first (populated at install time). Live fallback is multisite-aware. Final fallback returns 502 with `admin_user_unresolved` error code; runbook documents the manual recovery (set the install row's `admin_user_id` column). |
| Operator hits "Log in" twice rapidly → two SSO files exist briefly | low | low | Each is a separate one-time login; the second click signs them in again (same admin), no security loss. Reaper sweeps both within 60s. |
| Hostile user creates `jabali-sso-<43chars>.php` in their own webroot, lookalike to confuse reaper | very low | medium | Strict regex `^jabali-sso-[A-Za-z0-9_-]{43}\.php$` (Step 3) means a real lookalike would have to be valid base64url and exactly 43 chars — even then, reaper only deletes files older than 60s, and any file in the webroot is the user's own anyway. Bounded-impact case. |
| Browser prefetch / link preview consumes the file before operator clicks | medium | high | Step 2's PHP template short-circuits on `Sec-Purpose: prefetch`, `Purpose: prefetch`, `X-Moz: prefetch`, `X-Purpose: preview`, and any non-GET method. Returns 204 without entering the lock/unlink path. |
| `chown` failure leaves a malformed file readable by the wrong user | very low | high | Step 3's `CreateSSOFile` cleanup-on-error: deferred `os.Remove` fires unless explicit success flag is set after both chown + chmod succeed. Test asserts no file remains on chown failure. |
| Filesystem quota exhausted in `/home/<user>/domains/<site>/public_html` | very low | medium | Each file is < 4KB; reaper sweeps every 30s. Quota would have to be near zero already to trigger. |
| Audit trail loss matters more than expected | medium | low | Add a `sso_audits` table in a follow-up: `id, application_install_id, panel_user_id, file_name_hash, created_at, status`. Lightweight, no key material. Out of scope here. |

## Memory entries to write after merge

- `project_m22_rework_sso_file.md` — replaces `project_m22_magic_link.md` after the rework ships
- `feedback_industry_pattern_first.md` — capture the lesson that we should have looked at how Installatron/Softaculous solve this *before* designing M22 from scratch

## What this rework does NOT touch

- Kratos auth on the mint endpoint — unchanged.
- Panel UI button copy / placement — unchanged.
- Wire contract — unchanged.
- M16 OIDC artefacts — already removed.
- The `application_installs` table — unchanged.
- WordPress theme / plugin installation flow — unchanged.

## Acceptance criteria for the whole rework

1. Operator can click "Log in to admin" on any ready WP install row → new tab opens → lands signed in to `/wp-admin` as the install's admin user.
2. Same user clicking "Log in to admin" twice within 60s gets two valid one-shot files; both work; the second click doesn't break the first.
3. Reaper sweeps stale files within 60s of expiry.
4. No `/etc/jabali-panel/magic-link.key` exists on a fresh install.
5. No `wp-content/mu-plugins/jabali-magic-link.php` exists in any WP install on a fresh install.
6. `magic_link_tokens` table is dropped after migration runs.
7. `panel-api` boot logs contain no "magic-link key load failed" messages.
8. ADR-0040 is `accepted`; ADR-0039 is `superseded by 0040`.
9. `BLUEPRINT.md` says M22 is SHIPPED-AS-SSO-FILE (or similar status indicating the rework completed).
10. Existing test VM (`10.0.3.13`) has the old M22 artefacts cleaned up per the teardown doc.

---

**Plan status**: Dispatchable. Adversarial review by Plan agent (Opus, 2026-04-21) folded in — 2 CRITICAL + 4 HIGH + 5 MEDIUM findings addressed inline. Wave A (Steps 1, 2) ready for dispatch.

## Adversarial review fold-in summary

| # | Severity | Finding | Where folded |
|---|---|---|---|
| 1 | CRITICAL | Reaper glob misses subdirectory installs | Step 3: panel-supplied install paths + non-recursive walk + strict regex |
| 2 | CRITICAL | `chown` failure leaves malformed file | Step 3: deferred cleanup-on-error + test |
| 5 | HIGH | Admin UID lookup fragile (multisite, custom prefixes) | Step 4: read `install_row.admin_user_id` first; multisite-aware fallback; 502 `admin_user_unresolved` |
| 6 | HIGH | Reaper glob allows user-owned file deletion | Step 3: strict regex `^jabali-sso-[A-Za-z0-9_-]{43}\.php$` |
| 8 | HIGH/MED | Step 2/5 race on `sedSafeULID` | Wave plan: Step 5 moved to Wave B (after Step 2) |
| 11 | MEDIUM | Browser prefetch consumes file early | Step 2 PHP template: Sec-Purpose / Purpose / X-Moz / X-Purpose / non-GET checks → 204 |
| 12 | MEDIUM | `unlink` before redirect breaks retry on transient failure | Step 2 PHP template: `unlink` moved to last (after auth cookie set, before redirect) |
| 13 | MEDIUM | Subdirectory install URL missing subdir | Step 4: explicit subdirectory branch + tests |
| 7 | MEDIUM | PHP `error_log` leaks admin uid | Step 2 template: log says "admin login completed" without uid |
| 3 | MEDIUM | TOML field removal boot behaviour | Step 6: documented BurntSushi ignores unknown fields |
| 9 | MEDIUM | Teardown ordering unclear | Step 8: explicit deploy → migrate → cleanup ordering |
| 10 | MEDIUM | E2E doesn't verify sign-in | Step 8: full incognito open → cookie + dashboard assertions |
| 4 | LOW | Migration ordering | Step 6: documented as flexible |
| 14 | LOW | 90s reaper-window worst case | Step 2 template comment + Step 3 doc |
| 15 | LOW | E2E env setup | Step 8: cross-reference to gitea CI Docker fixture |
