# ADR-0040: Self-Deleting SSO File for Panel→WordPress Admin Login (M22 Rework)

**Status**: accepted (after adversarial review folded into plan 2026-04-21)

**Date**: 2026-04-21

**Deciders**: shuki

**Related**:
- [ADR-0036: M16 Hydra Identity](./0036-m16-hydra-identity.md) — first attempt at panel→WP admin SSO, rolled back
- [ADR-0038: M16 Rollback](./0038-m16-rollback.md) — rationale for removing the OIDC stack
- [ADR-0039: M22 Magic-Link Token](./0039-m22-magic-link.md) — **superseded by this ADR**
- [Plan: M22 Rework — Self-Deleting SSO File](../../plans/m22-rework-sso-file.md) — 8-step implementation blueprint

## Context

M22 magic-link (ADR-0039) shipped end-to-end on 2026-04-21 — opaque single-use HMAC-SHA256 tokens, 60-second TTL, a WordPress mu-plugin in every install validating tokens via a panel callback. End-to-end verification on test VM `192.168.100.150` the same day exposed five separate connectivity / lifecycle gaps in one validation session, all stemming from the same root cause: the design requires a **persistent panel-side WordPress plugin** and an **HTTPS callback from WP back to the panel**.

The five gaps:

1. The mu-plugin's "did sed run?" guard contained the literal placeholder strings sed targets at install time. The first sed pass mutated the guard into a self-comparison that always evaluates true → the plugin silently no-ops on every request. Real bug, would have hit every fresh install too — the bug surfaced on a backfill but the design pattern was the failure.
2. `panel-api/cmd/server/update.go` doesn't mirror `install.sh`'s `install_jabali_wp_mu_plugin` step. `jabali update` rebuilds binaries but never deploys the canonical mu-plugin source, leaving operators with a panel that knows about magic-link but no plugin in `/usr/local/lib/jabali/wp-mu-plugins/` to copy from.
3. Existing pre-M22 WordPress installs never get the per-install plugin copy because `installMagicLinkMUPlugin` only runs during a fresh `wp install`. Backfill needs a reconciler step that didn't exist.
4. nginx's default vhost on `:443` returns 444 (silent drop) for any path it doesn't explicitly route. The `POST /applications/<id>/magic-link/validate` endpoint registered on the panel root engine is unreachable from the WP plugin without a new `proxy_pass` block that wasn't in `install.sh`.
5. The panel's self-signed cert isn't in the OS CA bundle, so `wp_remote_post` with `sslverify=true` fails with `X509_V_ERR_SELF_SIGNED_CERT_IN_CHAIN`. Either the plugin disables sslverify (security loss) or `install.sh` trusts the cert at OS level on every fresh install + every update.

All five disappear if the WordPress side has **no persistent panel code** and **no callback to the panel**. That is the Installatron / Softaculous pattern: the panel writes a self-contained, self-deleting `sso-<nonce>.php` file to the WordPress webroot per login. The file loads `wp-load.php`, claims its single-use slot via `flock` + `unlink`, signs the operator in via `wp_set_auth_cookie`, redirects to `/wp-admin`, then exits. No mu-plugin. No HMAC key. No nginx routing change. No CA trust change. Installatron and Softaculous have run this pattern at scale across millions of WordPress installs for ~15 years.

## Decision

We replace the M22 magic-link mechanism (kept the public mint endpoint and the wire contract) with a self-deleting SSO file.

1. **Capability mechanism.** The capability token is a 256-bit random nonce (43 base64url characters from `crypto/rand`) embedded in the filename `jabali-sso-<nonce>.php`. The filename **is** the access token. There is no HMAC, no server-side signing key, no database row containing the token, no key material to rotate. Brute force is computationally infeasible (`2^256` ≈ 10^77). The wire contract `{url, expires_in}` returned by `POST /api/v1/applications/:id/magic-link` is preserved — only the `url` shape changes.

2. **File location and permissions.** The file lives in the WordPress webroot at `<install_path>/jabali-sso-<nonce>.php` where `<install_path>` is the install row's web-served directory (e.g. `/home/shukivaknin/domains/123123.com/public_html/`). nginx serves the file via the existing PHP location block — no special routing required. File mode is `0440 owner:www-data` so PHP-FPM (group `www-data`) can read but the install owner cannot accidentally edit. Atomic write: write to `.<filename>.tmp` with `O_CREAT|O_EXCL|O_WRONLY`, fsync, rename, chown, chmod. **If chown fails, the file is removed before returning** (no partial-state file readable by the wrong user).

3. **PHP template (file content).** The file is self-contained — only `wp-load.php` is included. The execution sequence (top to bottom):
   1. **Speculative-fetch / non-GET guard**: short-circuit on **any non-empty `Sec-Purpose`** (covers `prefetch`, `prerender`, `prefetch;prerender`, and any future speculation types per the Fetch Metadata spec), `Purpose: prefetch`, `X-Moz: prefetch`, `X-Purpose: preview`, or any non-`GET` method → return 204 without entering single-use state. **2026-04-21 incident**: the initial implementation used `Sec-Purpose === 'prefetch'` which missed Chrome's `Sec-Purpose: prefetch;prerender` — a prerender silently consumed the SSO file before the user's real click arrived, which then 404'd. Changed to `$secPurpose !== ''` (forward-compatible with future speculation values).
   2. **`flock(LOCK_EX | LOCK_NB)`**: claim the file. Second concurrent open returns 409.
   3. **TTL check**: if `filemtime(__FILE__) + 60 < now`, return 410. Defence in depth alongside the systemd reaper.
   4. **`require_once wp-load.php`**: bootstrap WordPress.
   5. **Admin uid validation**: the agent baked `$admin_uid` in at write time, reading from the install row's `admin_user_id` column (populated during M10 install). The PHP file does NOT do a live `wp_capabilities` LIKE pattern-match. `get_userdata($uid)` + `user_can($user, 'manage_options')` is the runtime check; failure → 500.
   6. **`wp_set_current_user($uid)` + `wp_set_auth_cookie($uid, false, is_ssl())`**: sign the operator in. `false` = no "remember me" cookie; this is a one-shot session.
   7. **`error_log` audit**: log the install id, **NOT** the admin uid — `wp-content/debug.log` can be world-readable when `WP_DEBUG_LOG` is on.
   8. **`Referrer-Policy: no-referrer` header**: prevent the filename leaking via Referer to wp-admin's third-party assets.
   9. **`unlink(__FILE__)` LAST** — after the auth cookie has already been set. If the redirect itself fails (transient PHP fatal, web-server crash mid-request), the file is still on disk and the operator can retry within the TTL. The reaper bounds stranded files at 90s. Doing `unlink` first would make every transient failure require a new mint.
   10. **`wp_safe_redirect(admin_url())` + `exit`**.

4. **Single-use enforcement.** Filesystem only — `flock` (race-safe within the kernel) + `unlink` (gone from directory namespace; PHP's open file descriptor keeps the inode alive until script end). No database transaction, no panel callback, no signature check. `unlink` alone is insufficient because a second concurrent open before unlink completes can also acquire the inode; `flock` closes that race.

5. **TTL enforcement.** A `jabali-sso-reaper.timer` systemd unit runs every 30 seconds and sweeps files matching the strict regex `^jabali-sso-[A-Za-z0-9_-]{43}\.php$` whose mtime is older than 60 seconds. Worst-case stranded-file lifetime: 60s (TTL) + 30s (reaper interval) ≈ 90s. The strict regex prevents the reaper from accidentally deleting a user-created file with a similar prefix. The reaper queries the panel for the active install paths rather than relying on a fixed `/home/*/domains/*/public_html` glob, so subdirectory installs are covered.

6. **Wire contract preserved.** `POST /api/v1/applications/:id/magic-link` still returns `{url, expires_in: 60}`. The `url` shape changes from `https://<site>/?jabali_admin_login=<token>` to `https://<site>[/<subdir>]/jabali-sso-<43chars>.php`. The panel-ui `useMagicLink` hook is unchanged.

## Threat Model

### T1 — Filename leak in URL

**Attack**: The full URL containing the 43-char nonce is potentially logged in nginx access logs, browser history, the operator's clipboard, browser-to-server `Referer` headers, mobile browser link previews, intermediate proxies, or third-party APM systems if the operator pastes the URL anywhere.

**Mitigation**: Single-use enforcement means a leaked URL is dead the instant the legitimate request consumes it. 60-second TTL caps the maximum exposure window for an unconsumed URL. The PHP file sets `Referrer-Policy: no-referrer` before redirecting, so wp-admin's image / script / font requests do not leak the URL to third parties. 256-bit entropy makes brute-force search infeasible. No APM scrubbing rules required because the URL is dead within seconds — even if it appears in a log, it's not a working credential by the time anyone reads the log.

**Residual risk**: A leak captured **and acted on within 60s** before the operator's own browser consumes the file. Very small window, requires an attacker actively monitoring a leak vector.

### T2 — Race / double-use

**Attack**: Two requests open the file at the same moment, both pass the existence check, both `unlink` succeeds, and both proceed to set auth cookies — yielding two valid operator sessions for the same nonce.

**Mitigation**: `unlink` alone is insufficient — PHP holds the inode alive on its open file handle, so a concurrent open before the directory entry is removed still gets the file. The PHP template uses `flock(LOCK_EX | LOCK_NB)` at the top: the second concurrent request fails the non-blocking lock, returns 409, and exits without setting any cookie. Only one execution proceeds past the lock.

**Residual risk**: Operator clicking twice in rapid succession can result in two SSO files (each with a different nonce) being created back-to-back; both signing in the same admin user is not a security loss. Clicking the same URL twice within ~10ms might both pass `flock` on systems where flock is best-effort (NFS); we document ext4/xfs only.

### T3 — File survives past TTL

**Attack**: Web-server crash or PHP fatal mid-execution leaves the file on disk past its 60-second TTL.

**Mitigation**: The systemd reaper sweeps `^jabali-sso-[A-Za-z0-9_-]{43}\.php$` files older than 60s every 30s. Worst-case lifetime 90s. The PHP file also has an inline mtime check that returns 410 if executed after TTL — so even if the reaper is delayed, a request past TTL fails fast.

**Residual risk**: 90-second window during which a stale file is on disk and (in principle) executable by anyone who guesses the 256-bit nonce. Negligible — see T4.

### T4 — Filename guessing / brute force

**Attack**: An attacker tries to guess valid `jabali-sso-<nonce>.php` filenames by enumeration.

**Mitigation**: 256 bits of entropy from `crypto/rand`. The search space is 2^256 ≈ 10^77 — physically infeasible. Even at 10^12 guesses per second across all of Earth's compute, a successful guess takes longer than the heat death of the universe.

**Residual risk**: None at any practical scale. Cryptanalytic attacks against `crypto/rand` would invalidate this defence (and most others on the system).

### T5 — File content disclosure to other code on the WP host

**Attack**: Other PHP code on the same host (the install owner's other plugins, themes, custom scripts) reads the SSO file and learns its contents.

**Mitigation**: The file is `0440 owner:www-data` — readable by PHP-FPM (which runs as `www-data` per ADR-0029 / per-user slices) and the install owner. Not readable by other system users. The file content reveals: the install id (already discoverable via the WP REST API), the admin uid (already discoverable via `/wp-json/wp/v2/users`), and the WordPress sign-in code path (public knowledge). No secrets are present in the file.

**Residual risk**: An attacker who has already compromised the install owner's PHP execution environment can do anything the install owner could do, including reading the SSO file. But at that point, the SSO file is the least concern — they have direct WP DB access via the install's `wp-config.php` credentials.

### T6 — Disk-full DoS

**Attack**: An attacker repeatedly triggers SSO file creation to fill `/home/<user>/domains/<site>/public_html/` until it runs out of disk space.

**Mitigation**: Each file is < 4KB. The reaper sweeps every 30s. Sustained file-creation throughput is bounded by panel mint rate-limiting (operator-level, inherited from existing panel auth). Per-user filesystem quotas (ADR-0032) bound the total disk a user can consume. cgroups v2 memory accounting prevents fork-bomb-style amplification.

**Residual risk**: A panel compromise that lets the attacker mint files at unbounded rate could fill disk before the reaper catches up. But at that point — same as T5 — the panel is already compromised and SSO is the least concern.

### T7 — Audit trail loss

**Attack**: The original M22 magic-link recorded every mint and validate in the `magic_link_tokens` table — a centralised audit log queryable for "who logged in to install X over the last 90 days." This rework drops that table.

**Mitigation**: The panel-api logs every mint via `slog` (operator id, install id, file name hash, expires_at). The agent logs every file creation. The PHP file logs every successful execution to WordPress's `error_log`. Operators can correlate these three sources, but there is no single SQL-queryable table.

**Residual risk**: Forensics across many installs is more painful than a single SELECT. If centralised audit becomes a real requirement, a future ADR can add a lightweight `sso_audits` table (columns: id, application_install_id, panel_user_id, file_name_hash, created_at, status) — no key material, append-only, easy to add without reverting the rest of this design.

### T8 — Cross-install replay

**Attack**: An attacker captures a valid SSO URL for install A and tries to use it against install B (e.g., by editing the hostname in the URL).

**Mitigation**: The URL contains the site hostname (`https://<site-A>/jabali-sso-<nonce>.php`); the nonce is local to install A's webroot. Editing the hostname to `<site-B>` causes the request to hit install B's web server, which has no file at that filename → 404. There is no shared state between installs to replay against.

**Residual risk**: None for cross-install. Cross-host within the same install (e.g., an alias domain pointing at the same docroot) would work, but this is the same trust boundary as the install itself.

### T9 — Panel compromise blast radius

**Attack**: The panel-api is compromised. The attacker can mint SSO files for every WordPress install on the host.

**Mitigation**: Same as M22 — the panel is the trust root for hosted installs. A panel compromise was already an instant `/wp-admin` access path via the existing password-reveal-once mechanism + DB access via per-user MariaDB grants. This rework does not change that exposure surface.

**Residual risk**: Single panel-side trust root. A future ADR could add per-install signing keys to compartmentalise blast radius, but adds key-management complexity that is not currently warranted.

## Comparison vs ADR-0039

| Mechanism | M22 magic-link (ADR-0039) | M22 sso-file (this ADR) |
|---|---|---|
| Capability shape | `<22>.<43>` HMAC-signed token in URL query string | 43-char base64url nonce embedded in filename in URL path |
| Server-side secret | `/etc/jabali-panel/magic-link.key` (32 bytes, comma-rotatable) | None |
| Single-use enforcement | `SELECT ... FOR UPDATE NOWAIT` in panel `magic_link_tokens` table | `flock(LOCK_EX|LOCK_NB)` + `unlink(__FILE__)` in PHP |
| TTL enforcement | `expires_at` column check at validate | systemd timer reaper (every 30s) + inline mtime check in PHP (defence in depth) |
| Persistent WP-side code | Must-use plugin in every install | None |
| Panel callback path | WP plugin `wp_remote_post`s to panel `POST /applications/:id/magic-link/validate` | None |
| nginx routing requirement | New `proxy_pass` block in default vhost for `/applications/.../validate` | None |
| TLS trust requirement | Panel cert must be in OS CA bundle (`update-ca-certificates`) for `sslverify=true` | None (no callback) |
| Audit trail | `magic_link_tokens` DB rows (operator, install, mint, consume) | Panel `slog` + agent log + WP `error_log` (no centralised SQL table) |
| Key rotation | Comma-separated key list in panel config; rotation drill in runbook | Not applicable (no key) |
| Filename in URL | Short (`?jabali_admin_login=<65 chars>` query string) | Path component (`/jabali-sso-<43 chars>.php`) |
| WordPress-only? | Yes (mu-plugin is WP-specific) | Yes (PHP file, calls `wp_set_auth_cookie`) — same constraint |
| Boot-time guards | Panel-api `serve.go` FATAL if key file missing / bad mode / malformed | None (no key to guard) |
| Migration footprint | `magic_link_tokens` table (000052) | `magic_link_tokens` dropped (000053); no replacement table |

## Trade-offs Accepted

1. **No centralised SQL audit table.** Audit lives in three places (panel, agent, WP error log). Forensics requires log correlation. Acceptable for the current scale; future `sso_audits` table is straightforward to add if needed.
2. **Filesystem semantics as the coordination primitive.** `flock` + `unlink` instead of database transactions. ext4 / xfs only — NFS / CephFS / virtio-9p have weaker / non-portable flock semantics. Documented in the runbook; agent's `CreateSSOFile` should `statfs` and refuse on non-supported filesystems.
3. **WordPress-only** (no protocol portability across CMS types). Same constraint as M22. Adding Joomla / Drupal / phpBB / etc. would each need their own equivalent SSO file template that calls the CMS's session API.
4. **Reaper is defence in depth, not the primary single-use gate.** The PHP file does its own TTL check (returns 410 if mtime + 60s < now) so a delayed reaper does not let stale files execute. Worst-case stranded-file disk presence is 90s.

## Trade-offs Rejected

1. **Per-install signing keys.** Would compartmentalise the blast radius of a panel compromise: a compromise of install X's key only authorises SSO into install X. Adds significant key-management complexity (provisioning at install time, rotation per install, key storage scope). Deferred — a future ADR can add this if panel-compromise blast radius becomes a real driver.
2. **Centralised `sso_audits` table from the start.** See T7. Would re-introduce a cross-cutting DB dependency. Deferred until an audit query proves to be a recurring operator need.
3. **HMAC-signed nonce instead of pure-random nonce.** Would re-introduce the `magic-link.key` and the rotation drill — exactly what the rework eliminates. The 256-bit random nonce in a filename achieves the same access-control property (only the holder can use the file) without a key.

## Acceptance Criteria

1. Operator can click "Log in to admin" on any ready WP install row → new tab opens → lands signed in to `/wp-admin` as the install's admin user.
2. Same operator clicking "Log in to admin" twice within 60s gets two valid one-shot files; both work; the second click does not break the first.
3. Reaper sweeps stale files within 90s (60s TTL + 30s reaper interval) of expiry.
4. No `/etc/jabali-panel/magic-link.key` exists on a fresh install.
5. No `wp-content/mu-plugins/jabali-magic-link.php` exists in any WP install on a fresh install.
6. `magic_link_tokens` table is dropped after migration runs.
7. `panel-api` boot logs contain no "magic-link key load failed" messages.
8. ADR-0040 is `accepted`; ADR-0039 is `superseded by 0040`.
9. `BLUEPRINT.md` reflects the rework status (REWORK IN-FLIGHT during the work, SHIPPED-AS-SSO-FILE after).
10. Existing test VM (`192.168.100.150`) has the old M22 artefacts cleaned up per the teardown doc.
