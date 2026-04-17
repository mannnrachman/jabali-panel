# Plan: phpMyAdmin SSO for user databases

**Objective.** Give panel users a one-click "Open phpMyAdmin" button for each
of their databases that lands them inside phpMyAdmin, already logged in, scoped
to the clicked database — without typing a DB password, without plaintext
storage of their DB-user passwords, and without a shared phpMyAdmin identity.

**Mode.** Direct (no `gh` CLI, Gitea remote). Each step is one commit on `main`
with conventional-commit message; no branch/PR flow.

**Sequencing.** Steps run sequentially unless a step header says "parallel
with step N." Parallelism is opt-in per-file-scope only — reconciler work,
install.sh work, and UI work are file-disjoint and safe to run concurrently
once the API contract is frozen (step 4). Do not parallelize steps 1–4.

**Invariants.** After every step:
- `cd panel-api && go test ./... -race` passes.
- `cd panel-agent && go test ./... -race` passes.
- `cd panel-ui && npx tsc -b && npx vite build` passes.
- `bash install.sh --check` (lint) stays green if present; else `bash -n install.sh`.
- No file in the diff exceeds 800 lines; no function exceeds 50 lines.
- No plaintext `mysqladmin` password appears in logs, responses, or DB columns.

---

## Design decisions (committed in step 1)

These are the decisions the plan assumes. Step 1 writes them into ADR-0022.

1. **Shadow account model.** Each panel user gets one MariaDB user
   `<panel_username>_mysqladmin` with `GRANT ALL ON \`<panel_username>_%\`.*`
   and no `GRANT OPTION`. SSO always logs in as this shadow account, scoped
   to the clicked database via `only_db` in phpMyAdmin's cfgupdate.
   Rationale: existing `database_users.password_hash` is bcrypt'd (ADR-0021)
   and cannot be replayed; the shadow account is created once, password
   encrypted at rest, works for every existing and future user.
2. **phpMyAdmin install.** Tarball to `/opt/phpmyadmin/<version>/` with
   `/opt/phpmyadmin/current` symlink, served by nginx on the panel host at
   path `/phpmyadmin/` via a dedicated `php-fpm` pool `jabali-pma`. Version
   pinned in `install.sh`; checksum verified.
3. **SSO token.** 32 bytes from `crypto/rand`, base64url-encoded for the URL.
   DB stores `SHA-256(token)` only. TTL 5 minutes, single-use, deleted on
   consume. No IP binding.
4. **Validate transport.** A **separate** Go listener at
   `/run/jabali/sso.sock` (Unix domain socket, 0660, `jabali:www-data`).
   Public 8443 listener never handles `/sso/phpmyadmin/validate`. Loopback
   TCP on 127.0.0.1 explicitly rejected — UDS eliminates the IP trust
   question entirely.
5. **Password encryption.** AES-256-GCM, key at
   `/etc/jabali-panel/sso.key` (32 bytes, 0600 `jabali:jabali`). Generated
   by `install.sh` on first run; rotation procedure documented in the ADR.
6. **Provisioning trigger.** On-demand at first SSO click (if row missing,
   ensure + issue in one handler). Reconciler backfill step (step 6) walks
   all existing users once so the first click is fast.
7. **phpMyAdmin session scope.** `only_db = <clicked_db>` in cfgupdate. Even
   though the shadow account can see every `<panel_username>_%` database,
   phpMyAdmin's UI will only show the one they clicked.
8. **Supersedes.** ADR-0022 supersedes ADR-0020 where they disagree. ADR-0020
   stays accepted for the parts that carry forward (5-min TTL, SHA-256 hash,
   single-use deletion, UDS transport).

---

## Step 1 — Write ADR-0022 and update ADR-0020 status

**Parallel:** no (blocks everything).
**Model tier:** strongest.
**Agent:** `adr-architect`.
**Est. complexity:** LOW.

### Context brief (self-contained)

Jabali Panel ships with an accepted ADR-0020 for phpMyAdmin SSO via a
server-side `signon.php` proxy. The high-level approach is fine but three
specifics are wrong or under-specified:

- ADR-0020 implies signing in as the *DB user the panel knows about*. That
  user's password is bcrypt'd (ADR-0021 "reveal once") and cannot be
  replayed. Any signon flow needs a different credential source.
- ADR-0020 is silent on transport: "over loopback Unix socket" without naming
  which listener. We need to distinguish the public 8443 listener from a
  dedicated UDS listener that only exposes `/validate`.
- ADR-0020 does not specify how phpMyAdmin is installed, where it lives, or
  how it's version-pinned.

### Tasks

1. Read `docs/adr/0020-m7-phpmyadmin-sso-signon-proxy.md` end-to-end.
2. Create `docs/adr/0022-m7-phpmyadmin-sso-shadow-account-and-uds.md`:
   - Status: Accepted. Supersedes parts of ADR-0020.
   - Document the 8 decisions listed in "Design decisions" above, each as its
     own section with Alternatives Considered (min 2 alternatives per
     decision) and Consequences.
   - Explicit crypto envelope for mysqladmin passwords: `nonce (12 bytes) ||
     ciphertext || tag (16 bytes)`, stored as `VARBINARY(512)`.
   - Explicit `only_db` key usage. **Do not hard-code the exact PHP
     session key names in the ADR.** phpMyAdmin's signon protocol has
     changed session-key structure across versions (4.x vs 5.x and point
     releases); the ADR must say only "the session keys documented by the
     phpMyAdmin version pinned in `install.sh`." Step 6 is responsible for
     transcribing the correct keys by reading `examples/signon-script.php`
     from the pinned tarball verbatim.
3. Update `docs/adr/0020-...`: add a header banner
   `**Superseded in part by ADR-0022 (2026-04-17)** — see §§3, 5, 7.`
4. Update `docs/adr/README.md` index with ADR-0022.

### Verification

- `test -f docs/adr/0022-m7-phpmyadmin-sso-shadow-account-and-uds.md`
- `grep -q "Superseded in part by ADR-0022" docs/adr/0020-*.md`
- `grep -c "0022" docs/adr/README.md` > 0

### Exit criteria

- ADR-0022 exists, lists all 8 decisions, each with alternatives.
- ADR-0020 carries a supersession banner.
- Index updated.
- Commit: `docs(adr): ADR-0022 phpMyAdmin SSO shadow-account + UDS`.

---

## Step 2 — Schema: users.mysqladmin_* columns + phpmyadmin_sso_tokens

**Parallel:** no (blocks 3–7).
**Model tier:** default.
**Agent:** `tdd-guide` → `backend-dev`.
**Est. complexity:** LOW.

### Context brief

The panel uses `golang-migrate` under `panel-api/internal/db/migrations/`
with sequential numeric prefixes. Latest is `000025_widen_grant_level_enum`.
Models live under `panel-api/internal/models/`. Repositories under
`panel-api/internal/repository/` — see `database_user_repository.go` for the
pattern. GORM is the ORM; table names are pinned via `TableName()`.

Two new tables / columns:

1. Extend `users` with two nullable columns for the shadow account.
2. New table `phpmyadmin_sso_tokens` — token hash, user/db FKs, TTL.

### Tasks

1. Create `000026_add_mysqladmin_shadow_to_users.up.sql`:
   ```sql
   ALTER TABLE users
     ADD COLUMN mysqladmin_username VARCHAR(64) NULL,
     ADD COLUMN mysqladmin_password_enc VARBINARY(512) NULL,
     ADD COLUMN mysqladmin_provisioned_at DATETIME(6) NULL;
   ```
   `.down.sql` drops all three columns.
2. Create `000027_create_phpmyadmin_sso_tokens.up.sql`:
   ```sql
   CREATE TABLE phpmyadmin_sso_tokens (
     id CHAR(26) NOT NULL PRIMARY KEY,
     user_id CHAR(26) NOT NULL,
     database_id CHAR(26) NOT NULL,
     token_hash CHAR(64) NOT NULL,
     expires_at DATETIME(6) NOT NULL,
     created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
     UNIQUE KEY uniq_token_hash (token_hash),
     INDEX idx_expires_at (expires_at),
     INDEX idx_user_id (user_id),
     FOREIGN KEY fk_sso_user (user_id) REFERENCES users(id) ON DELETE CASCADE,
     FOREIGN KEY fk_sso_db (database_id) REFERENCES databases(id) ON DELETE CASCADE
   ) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
   ```
   `.down.sql` drops the table.
3. Extend `panel-api/internal/models/user.go` with the three fields
   (`*string` / `[]byte` / `*time.Time` — all pointer types so NULL maps cleanly).
4. Create `panel-api/internal/models/phpmyadmin_sso_token.go`.
5. Create repository `panel-api/internal/repository/phpmyadmin_sso_token_repository.go`
   with: `Create(ctx, token)`, `ConsumeByHash(ctx, hash) (token, error)`
   (deletes + returns in one txn), `PurgeExpired(ctx) (int64, error)`.
6. Tests first, table-driven: creation, consume-success, consume-expired,
   consume-missing, consume-twice (second call returns `ErrNotFound`).
7. Wire repository into `panel-api/internal/app/app.go` DI.

### Verification

- `cd panel-api && go test ./internal/repository/... -race -run SSO`
- `cd panel-api && go build ./...`
- `golang-migrate up` on a fresh DB succeeds; `down` reverses cleanly.

### Exit criteria

- Migrations apply and reverse.
- Repository tests pass including the race-tested consume-twice case.
- Commit: `feat(sso): migrations 026/027 + phpmyadmin_sso_tokens repo`.

---

## Step 3 — Crypto helper: AES-GCM envelope + sso.key loader

**Parallel:** no (step 4 and step 5 both import it).
**Model tier:** strongest.
**Agent:** `security-auditor` → `backend-dev`.
**Est. complexity:** MEDIUM.

### Context brief

We need one minimal, well-reviewed package for envelope encryption of the
`mysqladmin` password. Key lives at `/etc/jabali-panel/sso.key` (32 bytes,
0600 `jabali:jabali`). Path is configurable via `JABALI_SSO_KEY_PATH` but
defaults to the above. The key is loaded once at panel-api startup and kept
in memory.

### Tasks

1. Create `panel-api/internal/ssokey/ssokey.go`:
   - `type Key [32]byte` (value type, not pointer).
   - `func Load(path string) (Key, error)` — reads exactly 32 bytes; returns
     `ErrKeyMissing` if absent, `ErrKeyWrongSize` otherwise.
   - `func (k Key) Seal(plaintext []byte) ([]byte, error)` — returns
     `nonce(12) || ciphertext || tag(16)`; nonce from `crypto/rand`.
   - `func (k Key) Open(envelope []byte) ([]byte, error)` — rejects
     envelopes < 28 bytes.
2. Tests: round-trip, tampered tag rejection, tampered nonce rejection,
   short-envelope rejection, wrong-key rejection, **nonce-uniqueness:
   `Seal(k, "same-plaintext")` called twice must produce two envelopes
   whose first 12 bytes differ** (proves nonce is freshly random; guards
   against accidental nonce reuse that would break AES-GCM
   confidentiality).
3. Wire `Load` into `panel-api/internal/app/app.go`; if the key is missing
   log a warning and continue (SSO is a feature flag — absence disables it,
   not the whole panel).
4. Add `install.sh` helper `install_sso_key()` (don't call yet — step 5
   wires it into the install flow): generates 32 random bytes to
   `/etc/jabali-panel/sso.key`, `chmod 0600`, `chown jabali:jabali`.

### Verification

- `cd panel-api && go test -race ./internal/ssokey/...` passes.
- Corrupting one byte of the envelope causes `Open` to error.

### Exit criteria

- Package is importable, tested, and loaded at startup.
- Commit: `feat(sso): AES-GCM envelope helper and sso.key loader`.

---

## Step 4 — Agent command `db.mysqladmin.ensure`

**Parallel:** no (steps 5–8 depend on it).
**Model tier:** default.
**Agent:** `tdd-guide` → `backend-dev`.
**Est. complexity:** MEDIUM.

### Context brief

Agent commands live in `panel-agent/internal/commands/`. Registered in
`registry.go`. The agent speaks NDJSON over `/run/jabali/agent.sock` and
runs as root. See `db_user_create.go` for the canonical pattern: parse
params, validate input with strict regex, escape identifiers with
backticks, invoke `mysql` via `exec.CommandContext` with a script fed on
stdin. Never interpolate into the SQL string.

### Tasks

1. Create `panel-agent/internal/commands/db_mysqladmin_ensure.go`:
   - Params: `{ "panel_username": "<lower a-z0-9>_{1..32}" }`.
   - Behavior: if the MariaDB user `<panel_username>_mysqladmin@localhost`
     does not exist, create it with a fresh 32-byte password; if it exists,
     rotate its password. Grant
     `ALL PRIVILEGES ON \`<panel_username>\_%\`.*` (escaped percent).
     Explicitly strip `GRANT OPTION`. `FLUSH PRIVILEGES`.
   - Return: `{ "username": "<prefix>_mysqladmin", "password": "<raw>" }`.
     Password only traverses the UDS to the panel-api caller; panel-api
     encrypts + discards the plaintext before responding to HTTP.
2. Strict validation: `panel_username` matches `^[a-z][a-z0-9_]{0,31}$`; reject `..`, `\`, `'`, `"`, spaces.
3. Escape helper: reuse existing MariaDB identifier escape helper from
   `db_create.go` (look for `escapeIdent` or equivalent; if not present,
   add it in a separate tiny commit first).
4. Register in `registry.go` under name `db.mysqladmin.ensure`.
5. Unit tests: validate-only (don't exec `mysql`, matching
   `db_user_create_test.go` convention). Cover: valid prefix, invalid
   chars, too long, empty.

### Verification

- `cd panel-agent && go test -race ./internal/commands/...`
- Manual smoke on a scratch MariaDB:
  ```
  echo '{"id":"1","method":"db.mysqladmin.ensure","params":{"panel_username":"alice"}}' \
    | socat - UNIX-CONNECT:/run/jabali/agent.sock
  ```
  Expect JSON with `username` and 32-char `password`. Verify in MariaDB:
  `SHOW GRANTS FOR 'alice_mysqladmin'@'localhost';` includes
  `ON \`alice\_%\`.*`.

### Exit criteria

- Command registered, tests pass, manual smoke succeeds.
- Commit: `feat(agent): db.mysqladmin.ensure — shadow account for SSO`.

---

## Step 5 — Panel API + UDS validate listener

**Parallel:** no (steps 6–8 depend on the UDS contract).
**Model tier:** default.
**Agent:** `tdd-guide` → `backend-dev` → `security-reviewer`.
**Est. complexity:** HIGH.

### Context brief

API handlers live in `panel-api/internal/api/`. Routes are registered in
`panel-api/internal/app/app.go`. The existing 8443 listener is set up in
`panel-api/cmd/server/main.go`. We need:

- **Public handler:** `POST /api/v1/sso/phpmyadmin` on the 8443 listener.
  Auth: normal panel JWT. CSRF: same-origin check via `Origin` /
  `Referer` header (reject cross-origin since this endpoint performs a
  state change). Body: `{ "database_id": "<ulid>" }`. Steps:
  1. Verify the caller owns the database (join `databases.user_id` == JWT
     `sub`; 403 otherwise).
  2. **Ensure-and-persist** (all in one SQL transaction to avoid TOCTOU):
     - `BEGIN; SELECT mysqladmin_username, mysqladmin_password_enc FROM
       users WHERE id = ? FOR UPDATE;`
     - If `mysqladmin_username IS NULL`: call
       `db.mysqladmin.ensure(panel_username)`, encrypt the returned
       plaintext password with `ssokey`, `UPDATE users SET
       mysqladmin_username=?, mysqladmin_password_enc=?,
       mysqladmin_provisioned_at=NOW(6) WHERE id=?`.
     - `COMMIT;`
     - If the agent call fails after `FOR UPDATE`, `ROLLBACK` and return
       500; the row stays NULL so a retry will re-enter this branch.
     - If the UPDATE fails for any reason, `ROLLBACK`; the agent may have
       created the MariaDB user without the panel knowing — next handler
       call will hit the agent again and the agent will *rotate* the
       password (see step 4 agent semantics); the new plaintext will be
       persisted at that point.
  3. Expose the ensure-and-persist logic as a reusable service method
     `ssoService.EnsureShadow(ctx, userID)` so the reconciler (step 7) can
     call the exact same code path rather than duplicating it.
  4. Mint a 32-byte random token, compute `SHA-256(token)`, insert a row
     into `phpmyadmin_sso_tokens` with 5-minute TTL.
  5. Return `{ "redirect_url":
     "/phpmyadmin/sso.php?token=<base64url-raw>&db=<final_db_name>" }`.
  Rate limit: 10/min per user (use existing `middleware/ratelimit.go`
  if present; else document as a follow-up).

- **UDS listener:** new file
  `panel-api/internal/app/sso_uds.go` that starts a second `http.Server`
  on `/run/jabali/sso.sock` (0660, `jabali:www-data`). Mount path
  `POST /sso/phpmyadmin/validate`. No JWT; the socket's filesystem ACL is
  the boundary. Body: `{ "token": "<base64url-raw>" }`. Steps:
  1. Parse, hash, call `ConsumeByHash` (atomic delete-and-return).
  2. Load `users.mysqladmin_username` and `mysqladmin_password_enc`.
  3. Decrypt; build response `{ "user": "<u>_mysqladmin", "password":
     "<raw>", "host": "127.0.0.1", "port": 3306, "only_db":
     "<final_db_name>", "db": "<final_db_name>" }`.
  4. Return 200. Any error returns `{"error": "<code>"}` with HTTP 410
     (Gone) for expired/used, 404 for unknown, 500 otherwise.

### Tasks

1. Add handler files: `panel-api/internal/api/sso_phpmyadmin.go` and
   `panel-api/internal/api/sso_phpmyadmin_validate.go`.
2. Add UDS server wiring in `panel-api/internal/app/sso_uds.go` (shutdown
   cleanly on SIGTERM; remove the socket file on stop).
3. Expose config: `JABALI_SSO_SOCKET_PATH` (default `/run/jabali/sso.sock`).
4. Tests first:
   - Handler tests with `httptest` + mocked agent + sqlmock: covers
     ownership check, first-click ensure path, subsequent-click (no
     ensure), error surfaces.
   - UDS validate tests against a real `net.Listen("unix", ...)` in a temp
     dir; cover happy path, expired token, replay (second validate fails).
   - Redaction test: structured logger must never emit the password or
     the raw token.
5. Wire routes in `app.go`.

### Verification

- `cd panel-api && go test -race ./internal/api/... ./internal/app/...`
- Manual: curl issue → copy token → `curl --unix-socket
  /run/jabali/sso.sock http://x/sso/phpmyadmin/validate -d
  '{"token":"..."}'` returns creds; second call returns 410.

### Exit criteria

- Both endpoints live, tested, and reviewed by `security-reviewer`.
- No password or raw token in any log.
- Commit: `feat(sso): panel API + UDS validate listener`.

---

## Step 6 — install.sh: phpMyAdmin tarball + nginx + fpm pool + sso.php

**Parallel:** yes, with step 7 and step 8 (disjoint files).
**Model tier:** default.
**Agent:** `backend-dev`.
**Est. complexity:** HIGH.

### Context brief

`install.sh` is the single install entry point. See `install_powerdns` for
the third-party install pattern (apt install + systemd override + config
merge). phpMyAdmin is not in apt with a recent enough version; we fetch the
tarball and manage it ourselves.

We need:
- A pinned phpMyAdmin version (recommend 5.2.x LTS; check the latest at
  implementation time).
- A dedicated `php-fpm` pool `jabali-pma` running as user `jabali-pma`
  (new system user, uid in the 1200s, no shell) for isolation from the
  panel's jabali user.
- Nginx location `/phpmyadmin/` on the panel vhost (port 8443), upstream
  to the `jabali-pma` fpm socket.
- `sso.php` dropped inside the phpMyAdmin root (or its config dir) that
  calls the UDS validate and writes the signon session.
- `config.inc.php` with `auth_type='signon'`, `SignonURL='/phpmyadmin/sso.php'`,
  `SignonSession='SignonSession'`, `host='127.0.0.1'`.
- `/etc/jabali-panel/sso.key` generated on first install (see step 3).
- Nginx access log scrubs `token=` from the query string.

### Tasks

1. Add `install_phpmyadmin()` in `install.sh`:
   - Version pin constant at top of the function.
   - `curl -fsSL <tarball>` with SHA-256 checksum verification against a
     vendored checksum in the repo (`install/phpmyadmin.sha256`).
   - Extract to `/opt/phpmyadmin/<version>/`; symlink
     `/opt/phpmyadmin/current`.
   - Create system user `jabali-pma`.
   - Write `/etc/php/<detected-version>/fpm/pool.d/jabali-pma.conf`
     (listen on `/run/php/jabali-pma.sock`, owner `www-data:jabali-pma`,
     mode 0660; `user=jabali-pma`, `group=jabali-pma`).
   - Write `config.inc.php` under `/opt/phpmyadmin/current/config.inc.php`.
   - Deploy `sso.php` from `install/phpmyadmin/sso.php` (next task) into
     `/opt/phpmyadmin/current/sso.php`.
   - Add nginx snippet: `location ^~ /phpmyadmin/ { ... }` with `fastcgi_pass`
     to the fpm socket. **Log format must redact the query string**,
     because `access_log` alone cannot strip query params — nginx logs the
     raw request line before any rewrite. Use a custom log format that
     logs only `$uri` (path) and a redacted marker when a query string
     was present:
     ```
     map $args $jabali_pma_logargs {
         ""      "-";
         default "[REDACTED]";
     }
     log_format jabali_pma '$remote_addr - $remote_user [$time_local] '
                          '"$request_method $uri $server_protocol" '
                          '$status $body_bytes_sent '
                          'args=$jabali_pma_logargs "$http_referer" '
                          '"$http_user_agent"';
     ```
     Apply via `access_log /var/log/nginx/jabali-pma.access.log jabali_pma;`
     inside the phpMyAdmin location block. Verification: curl with a
     token → `grep token= /var/log/nginx/jabali-pma.access.log` must
     return nothing.
   - Add `jabali-pma` to `www-data` supplementary group so the fpm process
     can connect to `/run/jabali/sso.sock` (mode 0660, group `www-data`).
   - Call `install_sso_key` (from step 3).
   - Idempotency: detect existing version symlink, skip extraction.

2. Create `install/phpmyadmin/sso.php` (self-contained, ~80 lines):
   - Validate `token` matches `^[A-Za-z0-9_-]{32,128}$`.
   - Connect to `/run/jabali/sso.sock` via `stream_socket_client('unix:///run/jabali/sso.sock')`.
   - Write a minimal HTTP/1.1 POST request with JSON body manually (no curl,
     no Guzzle — keep dependency surface zero).
   - Parse response JSON, enforce 200.
   - **Session key names: read from the pinned tarball first.** Before
     writing this file, open
     `/opt/phpmyadmin/current/examples/signon-script.php` and copy the
     exact key structure it uses (phpMyAdmin 5.x expects the keys
     organized per `SignonSession` name; the leading structure has
     changed across 4.9 / 5.0 / 5.1 / 5.2 and must be verified against
     the pinned version rather than assumed).
   - `session_name('SignonSession'); session_start();` then populate
     `$_SESSION[...]` per the example script's contract. Include
     `'only_db'` inside the cfgupdate array and `'host'=>'127.0.0.1'`.
   - Output ONLY this (no HTML, no echo of validator response):
     `header('Location: /phpmyadmin/index.php?db=' . urlencode($resp['db']));`
   - `exit;` immediately after the Location header — do not let PHP
     fall through to any buffered output.
   - Cookie flags on `session_set_cookie_params`:
     `secure=true, httponly=true, samesite=Lax`.
   - On any error: `http_response_code(400); exit;` with a static message —
     never echo anything from the validator into the HTML.
   - **XSS defense:** every value that ends up in an HTTP header (only the
     Location header here) must be `urlencode`'d. Never use `$resp['db']`
     directly in output without encoding.

3. Create `install/phpmyadmin.sha256` with the pinned version's checksum.

4. Test the install on a throwaway VM or LXC container. Document the smoke
   sequence in `plans/phpmyadmin-sso-runbook.md`.

### Verification

- Fresh install → `https://panel/phpmyadmin/` serves phpMyAdmin login page.
- `sudo -u jabali-pma cat /run/jabali/sso.sock` readable (should succeed via
  the group). `sudo -u jabali-pma cat /etc/jabali-panel/sso.key` should fail
  (this key is jabali-only; fpm doesn't need it).
- `bash -n install.sh` clean.

### Exit criteria

- phpMyAdmin reachable, sso.php wired, idempotent re-install succeeds.
- Commit: `feat(install): phpMyAdmin tarball + nginx + fpm pool + sso.php`.

---

## Step 7 — Reconciler: backfill `mysqladmin_*` for existing users

**Parallel:** yes, with step 6 and step 8.
**Model tier:** default.
**Agent:** `backend-dev`.
**Est. complexity:** LOW.

### Context brief

The reconciler (`panel-api/internal/reconciler/reconciler.go`) runs every
30s and converges DB → system state (domains, DNS, SSL). Add a new pass
that ensures every `users` row with `username != ''` and
`mysqladmin_username IS NULL` has a shadow account.

This is a safety net so the first SSO click is fast; the API handler
itself still calls `ensure` lazily.

### Tasks

1. Add `reconcileMysqlAdminShadow(ctx)` to the reconciler. **Do not
   duplicate the ensure-and-persist logic** — call
   `ssoService.EnsureShadow(ctx, userID)` (defined in step 5) so both
   the handler path and the backfill path share the same transactional
   code. Agent error, encryption error, and UPDATE error are all handled
   identically by that method.
2. Query `SELECT id FROM users WHERE mysqladmin_username IS NULL AND
   username <> '' LIMIT 50` each tick; loop calling `EnsureShadow` per
   row; log per-row errors but continue the loop.
3. Hook into the existing 30s tick *as a separate pass* (don't block
   domain reconciliation on this).
4. **Crash-safety note:** if the agent creates the MariaDB user but the
   panel-api process crashes before UPDATE commits, the row stays NULL
   and the next tick re-enters `EnsureShadow`. The agent's ensure
   command will detect the existing user and *rotate* its password
   (documented in step 4 agent semantics), producing a fresh plaintext
   the reconciler can persist. Stale-password divergence is therefore
   impossible for longer than one reconciler tick.
5. Tests: sqlmock + mocked agent; verifies batch limit, per-row error
   resilience, and the rotate-on-exist recovery path (simulate a
   previous crash: MariaDB user exists, DB row is NULL → next tick
   rotates and persists cleanly).

### Verification

- `cd panel-api && go test -race ./internal/reconciler/...`
- On a DB with 3 users, after one reconciler tick all three have
  `mysqladmin_username` populated; none of them have plaintext passwords
  in the DB column.

### Exit criteria

- Backfill pass implemented, tested, and non-blocking.
- Commit: `feat(reconciler): backfill mysqladmin shadow for existing users`.

---

## Step 8 — UI: "Open phpMyAdmin" button per database row

**Parallel:** yes, with step 6 and step 7.
**Model tier:** default.
**Agent:** `mobile-dev` (React/AntD) → `typescript-reviewer`.
**Est. complexity:** LOW.

### Context brief

The user shell's database list is
`panel-ui/src/shells/user/databases/UserDatabaseList.tsx`. Rows have
action buttons already (Backup, Restore, Delete from step D work). Add one
more: "Open phpMyAdmin."

The button calls the panel API, receives a `redirect_url`, and navigates
the top-level tab to that URL. Do not open in a new tab by default (the
signon session cookie is per-origin and a same-tab nav is simpler); offer
a middle-click-opens-new-tab via `<a href>` rather than a JS click handler
where possible.

### Tasks

1. Add a typed API client call in
   `panel-ui/src/apiClient.ts`:
   `ssoPhpMyAdmin(databaseId: string): Promise<{ redirect_url: string }>`.
2. In `UserDatabaseList.tsx`, add the action. On click:
   - `const r = await ssoPhpMyAdmin(row.id); window.location.assign(r.redirect_url);`
   - Show `message.error(...)` on failure; never log the redirect URL (it
     contains a live token).
3. Button label: "Open in phpMyAdmin" (AntD `Button` with `LinkOutlined`).
4. Disable the button if the row's database has `engine='postgres'`
   (phpMyAdmin is MariaDB-only per ADR-0018).
5. No new test for now — the UI lands behind the same flow that E2E
   covers in step 9 (below) if you add it; otherwise document the manual
   test steps.

### Verification

- `cd panel-ui && npx tsc -b && npx vite build` passes.
- Manual: click the button on a real DB; phpMyAdmin opens logged in,
  showing only the clicked DB.

### Exit criteria

- Button wired, typed, handles error.
- Commit: `feat(ui): Open in phpMyAdmin action per database row`.

---

## Step 9 — Observability, rate limit, docs

**Parallel:** no (final consolidation).
**Model tier:** default.
**Agent:** `doc-updater` → `code-reviewer`.
**Est. complexity:** LOW.

### Context brief

Close the loop: audit log, rate limiter, blueprint + runbook, remove the
M7-Tranche-E TODO from BLUEPRINT.md.

### Tasks

1. Audit log: every `/api/v1/sso/phpmyadmin` issue and every UDS
   `/validate` emits a structured log line with
   `{user_id, database_id, token_hash_prefix (first 8 hex chars),
   outcome: issued|validated|expired|replay|unauthorized}`. Never log
   the raw token or password.
2. Rate limit: 10 issues per user per minute at the handler. Use the
   existing middleware if present; otherwise add a tiny in-memory limiter
   and flag it as "replace with Redis when multi-node."
3. `docs/BLUEPRINT.md`: move M7 from Planned to Shipped in the
   changelog table; add a new subsection 4.10 "phpMyAdmin SSO" with API
   paths, migrations, agent commands, install artifacts.
4. `docs/RUNBOOK.md`: add a phpMyAdmin section covering:
   - **Key rotation (detailed procedure, tested on staging first):**
     1. `openssl rand 32 > /etc/jabali-panel/sso.key.new && chmod 0600
        /etc/jabali-panel/sso.key.new && chown jabali:jabali
        /etc/jabali-panel/sso.key.new`.
     2. Stop the reconciler (`jabali-panel reconciler pause` — CLI added
        as part of this step if it doesn't exist) so no background
        writes race the rotation.
     3. Run `jabali-panel sso rotate-key` (new CLI subcommand):
        `BEGIN; SELECT id, mysqladmin_password_enc FROM users WHERE
        mysqladmin_password_enc IS NOT NULL FOR UPDATE; ` → decrypt each
        with old key, encrypt with new key → `UPDATE users SET
        mysqladmin_password_enc=? WHERE id=?` for each → `COMMIT;`
     4. On COMMIT success: `mv /etc/jabali-panel/sso.key.new
        /etc/jabali-panel/sso.key`.
     5. `systemctl kill -s SIGHUP jabali-panel` — panel-api reloads the
        key from disk. (This requires a SIGHUP handler, add as part of
        step 9 work.)
     6. `jabali-panel reconciler resume`.
     7. On any failure in step 3, `ROLLBACK` leaves the DB unchanged;
        delete `sso.key.new` and investigate.
   - How to revoke a leaked `_mysqladmin` account (`DROP USER`, NULL the
     three users columns, let reconciler recreate).
   - How to upgrade phpMyAdmin tarball.
5. Prune the `phpmyadmin_sso_tokens` table nightly — add a cron-on-boot
   goroutine inside the reconciler that calls `PurgeExpired` every 5
   minutes.
6. **`only_db` scope test** (required, not optional): add an integration
   test that (a) provisions two DBs for user `alice` (`alice_wp` and
   `alice_mail`) and one for `bob` (`bob_wp`), (b) executes an SSO flow
   for `alice_wp` end-to-end via a browser driver or a scripted
   phpMyAdmin session, (c) asserts the phpMyAdmin UI lists `alice_wp`
   only (not `alice_mail`, not `bob_wp`), (d) attempts a direct SQL
   `SHOW DATABASES` through phpMyAdmin and verifies `only_db` still
   hides everything outside `alice_wp`. If phpMyAdmin's `only_db`
   enforcement turns out to be cosmetic (not a privilege boundary), this
   test will fail and the plan must fall back to per-DB grants on a
   transient MariaDB user minted at SSO time — document that fallback in
   the ADR as "Plan B if `only_db` is not a boundary."

### Verification

- `grep -q "phpMyAdmin" docs/BLUEPRINT.md`
- `grep -q "rotate.*sso.key" docs/RUNBOOK.md`
- Inspect three days of logs on staging: `grep sso_phpmyadmin
  /var/log/jabali-panel.log` shows no password or raw token.

### Exit criteria

- Docs updated, audit log verified, purge ticker live.
- Commit: `docs(sso): phpMyAdmin runbook + BLUEPRINT update`.

---

## Dependency graph

```
1 ──► 2 ──► 3 ──► 4 ──► 5 ──┬─► 6
                            ├─► 7
                            ├─► 8
                            └─► 9
```

Steps 6 / 7 / 8 are file-disjoint and safe to parallelize once step 5 is
committed. Step 9 waits for 6, 7, 8.

## Rollback notes

- Steps 2 / 3 / 4 are self-contained and revert cleanly via `git revert`
  plus `migrate down` for the two new migrations.
- Step 5 revert also requires removing `/run/jabali/sso.sock` on deployed
  hosts.
- Step 6 revert requires `systemctl stop php<ver>-fpm@jabali-pma` and
  removing the nginx snippet before `nginx -s reload`.
- No step irreversibly modifies user data; `mysqladmin` accounts can be
  re-created, tokens can be re-issued.

## Review log

Plan reviewed by `security-architect` (Opus) on 2026-04-17. Verdict:
FIX-BEFORE-SHIP with 3 CRITICAL and 2 HIGH findings, all folded back
into the step bodies above:

- **C1: phpMyAdmin session key names** — ADR must not hard-code them;
  step 6 transcribes from the pinned tarball's `signon-script.php`.
- **C2: AES-GCM nonce reuse risk** — step 3 adds a nonce-uniqueness
  test.
- **C3: TOCTOU on shadow-account ensure** — step 5 wraps in
  `BEGIN ... SELECT FOR UPDATE ... COMMIT` and exposes
  `ssoService.EnsureShadow` for step 7 reuse.
- **H1: nginx log scrubbing** — step 6 defines an explicit
  `log_format` with `$uri` and a redacted-args token.
- **H2: reconciler crash-safety** — step 7 delegates to
  `EnsureShadow`; divergence cannot last longer than one tick.
- **M1: only_db not tested** — step 9 adds an integration test with
  a documented fallback to transient per-DB users if `only_db` turns
  out to be cosmetic.
- **M2: key rotation runbook** — step 9 RUNBOOK procedure now
  prescribes the full sequence including SIGHUP reload.

## Anti-patterns explicitly forbidden

- ❌ Storing `mysqladmin_password_enc` without a nonce in the envelope.
- ❌ Binding the token to an IP (breaks mobile/roaming, no added security
  given the UDS transport).
- ❌ Trusting a header as the panel user's IP.
- ❌ Using `127.0.0.1:<port>` instead of UDS for the validate listener.
- ❌ Writing `sso.key` into the repo, into a config file, or into any
  environment variable set by systemd (use the file at 0600 only).
- ❌ Reusing the 8443 router mux for `/validate` — must be a distinct
  listener.
- ❌ Running all of 6/7/8 in parallel from the main worktree. Either
  commit 5 and run them sequentially, or create one worktree per step —
  last parallel attempt wrote to main and produced three real bugs.
- ❌ Sharing a phpMyAdmin identity across panel users.

## Success criteria for the whole plan

- A first-time panel user `alice` with one database `alice_wp` clicks
  "Open in phpMyAdmin," is inside phpMyAdmin within 2 seconds, and sees
  exactly one database (`alice_wp`) in the left nav.
- A second user `bob` cannot reach alice's databases via SSO even by
  replaying alice's token (the token is single-use and IP-unbound — but
  `bob` has no ownership path in the handler).
- Nightly purge keeps `phpmyadmin_sso_tokens` at steady-state < 1 MB.
- No password or raw token in logs, nginx access logs, DB columns, or
  agent responses visible to the UI.
- `jabali-panel update` on prod succeeds end-to-end with a fresh install
  *and* with an upgrade from the pre-SSO commit (`e792952`).
