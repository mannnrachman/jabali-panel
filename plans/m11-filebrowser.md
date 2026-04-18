# M11 — File Manager via filebrowser (integration plan)

**Status:** Dispatched (Wave A in flight).
**Goal:** Users get a web file manager scoped to their homedir, launched from the panel via SSO, with no local file-browser code to maintain.

## 0. Decision recap

Integrate `filebrowser/filebrowser` (v2.63.2+, MIT, Go, upstream maintenance-only but stable). NOT the `gtsteffaniak` fork — we don't need OIDC/LDAP right now, and upstream is the lower-risk choice. Decision recorded as ADR-0027 (Step 1).

**Integration pattern — mirrors M7 phpMyAdmin SSO:**

1. Install filebrowser as a single shared system service listening on a local UNIX socket (no exposed TCP port).
2. Panel mints a short-lived SSO token (same `ssokey` HMAC infra as PMA).
3. Nginx reverse-proxies `/files/<user>/*` to the filebrowser socket, injecting `X-Forwarded-User: <username>` **at the nginx layer** so clients can't spoof it.
4. Filebrowser config: `auth.method=proxy`, `auth.header=X-Forwarded-User`; each user pre-created with `scope=/home/<username>`.
5. User's homedir already exists (per-user slice from M9.5). Filebrowser just reads/writes as the `filebrowser` service user — which needs group-write into each user's home.

**Open question — carried into ADR and closed by Step 3 spike:** how filebrowser runs as a user with access to every homedir. Three candidates:
- (a) run as `root` (ugly, but simplest — filebrowser enforces scope at the app layer so escape requires a bug).
- (b) run as a dedicated `filebrowser` user and put it in every per-user group (clean, matches M9.5 model).
- (c) one filebrowser process per user via template unit (like `jabali-fpm@<user>`; heavy at 100+ users).

Spike picks one; agent-b documents the call.

## 1. Steps / waves

| Step | Wave | Parallel? | Summary | Outputs |
|------|------|-----------|---------|---------|
| 1 — ADR-0027 | A | w/ 2 | Record the filebrowser-upstream-with-proxy-auth decision. Include 3 open questions surfaced by Wave A spike. | `docs/adr/0027-m11-filebrowser-integration.md` |
| 2 — install.sh + systemd | A | w/ 1 | Install filebrowser binary (pin version), create `/etc/jabali-panel/filebrowser/` config dir, systemd unit listening on UDS. Don't start yet. | `install.sh` additions, `install/systemd/filebrowser.service`, `install/filebrowser/config.json.tmpl` |
| 3 — user-provisioning spike | B | serial | Answer the "who does filebrowser run as?" question. Pick (a)/(b)/(c). Write a short memo in the ADR. | ADR update |
| 4 — panel SSO endpoint | B | w/ 5 | `POST /api/v1/sso/filebrowser` — mint token, store in `filebrowser_sso_tokens` table (mirror `phpmyadmin_sso_tokens`), return redirect URL. Validator UDS endpoint that returns `{user}` to nginx for subrequest auth. | migration, model, handler, sso service extension |
| 5 — nginx reverse proxy | B | w/ 4 | Nginx `location /files/` block with auth-request subrequest to the panel validator, strip inbound `X-Forwarded-User`, inject from subrequest response. | `install/nginx/jabali-files.conf` |
| 6 — reconciler: filebrowser users | C | — | Every user with a Linux username gets a filebrowser user created via `filebrowser users add ...` with `scope=/home/<username>`. Idempotent; runs alongside `ReconcilePHPPools`. | reconciler func |
| 7 — UI "Files" sidebar item | C | — | Sidebar entry below WordPress, opens new tab: `POST /sso/filebrowser` → navigate returned URL. Pattern from `UserDatabaseList.handleOpenPhpMyAdmin`. | Refine resource + button, no dedicated page |
| 8 — E2E + docs + blueprint status | D | — | Playwright happy path: login → Files → upload a file → rename → delete. Runbook. Flip M11 status. Memory pointer. | `tests/e2e/filebrowser.spec.ts`, `plans/m11-filebrowser-runbook.md`, docs/BLUEPRINT.md update |

## 2. Out of scope

- **Quotas / disk limits.** M11.5 or M12. Filebrowser has no quota enforcement; this is a separate concern (likely filesystem-level via `quota` package or per-user XFS project quotas).
- **Sharing / public links.** Filebrowser supports it; we disable it in config (`shareManagement=false`) for v1 because panel-level auth is the source of truth.
- **Command execution.** Filebrowser's `commands` feature is a full shell — **must be disabled** (security).
- **Multi-protocol (S3/SFTP/WebDAV).** Filestash handles this; not our use case.
- **Gtsteffaniak-fork migration.** Deferred until we need OIDC/LDAP.

## 3. Security invariants

- Filebrowser only ever listens on a UNIX socket in `/run/jabali-filebrowser/fb.sock`, never TCP.
- Nginx **always strips** inbound `X-Forwarded-User` before injecting the subrequest-authenticated value. Must be auth_request-gated — no unauthenticated path reaches filebrowser.
- `commands` feature disabled in config (`perm.execute=false` per-user; also strip global `commands` allowlist).
- Default permissions in config: `perm.delete=true, perm.rename=true, perm.modify=true, perm.create=true, perm.download=true, perm.share=false, perm.execute=false`.

## 4. Wave A dispatch (NOW)

- Agent 1 → Step 1 (ADR) — worktree isolation.
- Agent 2 → Step 2 (install.sh + systemd) — worktree isolation.

Wave B dispatched after Wave A is reviewed and merged. Wave B's Step 3 is a short spike (single agent) that informs Steps 4–7.

## 5. Open questions at plan time

1. **Filebrowser binary source:** do we pin a GitHub release tarball (like wp-cli) or use apt? Upstream does not publish a Debian package. → Pin to a specific GitHub release + SHA-256; mirror the wp-cli pattern. Step 2 decides final version.
2. **Users DB location:** filebrowser uses its own BoltDB or SQLite at `filebrowser.db`. Where does it live? → `/var/lib/jabali-filebrowser/filebrowser.db`. Step 2 picks path.
3. **User deletion cleanup:** when we delete a jabali user, we must also delete their filebrowser user. Covered by reconciler (Step 6).
