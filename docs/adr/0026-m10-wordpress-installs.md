# ADR-0026: M10 WordPress installations

**Date:** 2026-04-18
**Status:** Accepted
**Deciders:** Shuki

## Context

The Jabali Panel currently hosts domains and databases but offers no streamlined
path to provision WordPress installations. Manual setup is tedious and error-prone.
M10 adds WordPress as a first-class provisioning target, giving users a 1-click
install flow and enabling delete/clone operations to scale WordPress hosting.

An install is a durable record that links a domain, a database, and a set of
WordPress configuration. The record lets the reconciler and agent coordinate
without re-scraping the filesystem each time.

## Decision

We are adding a **wordpress_installs table** with 8 key design decisions that
lock the schema, model layer, and operational model.

---

## Design Decisions

### 1. One install per domain (domain_id UNIQUE)

**Decision:** Each domain can host at most one WordPress installation.
The `domain_id` column is UNIQUE. Multiple installs on the same domain
are not a Jabali use case; if they become one, a new ADR raises the cap.

**Rationale:**
- Simplifies the provisioning UX: "Install WordPress on this domain."
- Avoids namespace collisions in the docroot (`/home/<user>/domains/<name>/public_html/`).
- Reduces reconciler complexity (one install = one codepath).

**Consequences:**
- If a user wants to run two WordPress versions on the same domain, they must
  move one to a fresh domain. Acceptable for MVP.

---

### 2. Database foreign key with ON DELETE RESTRICT

**Decision:** The `db_id` column is a foreign key to the `databases` table
with `ON DELETE RESTRICT`. You cannot drop a database out from under a live
WordPress install.

**Rationale:**
- Prevents orphaning an install by deleting its backing database accidentally.
- Makes deletion explicit: delete the install first (which tears down the DB via
  the API), then the install's DB is gone.

**Consequences:**
- If an operator tries to delete a database directly via SQL, the FK constraint
  blocks it (good).
- The API's delete handler (Step 4) must drop the database as part of the
  delete flow, not assume it's already gone.

---

### 3. Status as VARCHAR CHECK (not ENUM type)

**Decision:** `status` is a VARCHAR(16) with a CHECK constraint, not a MySQL ENUM.
Allowed values: `pending`, `installing`, `ready`, `failed`, `deleting`, `cloning`.

**Rationale:**
- Matches the pattern used in other tables (`php_pools`, `php_pool_ini_overrides`).
- CHECK constraints are portable (ENUM is MySQL-specific and harder to alter).
- Explicit enum values in SQL are easier to grep than a separate ENUM type definition.

**Consequences:**
- Humans must remember the enum values; document in the runbook.
- Altering the enum requires an ALTER TABLE with a new CHECK; not a one-liner,
  but doable.

**Status lifecycle:**
- `pending` → `installing` (async agent task started)
- `installing` → `ready` (agent succeeded)
- `installing` → `failed` (agent failed or reconciler timeout)
- `ready` → `deleting` (user initiated delete)
- `deleting` → (row deleted from DB after teardown succeeds)
- `ready` → `cloning` (clone operation started)
- `cloning` → `ready` (clone succeeded) or `failed` (clone failed)

---

### 4. Version NULL until agent reports

**Decision:** The `version` column is VARCHAR(32) NULL. It remains NULL
until the agent calls back with the installed WordPress version string
(e.g., "6.5.3"). At creation time, the API does not know what version will
be installed; the agent discovers it post-install.

**Rationale:**
- True state: we don't know the version until installation completes.
- Avoids guessing or caching stale version info from an HTTP call.
- Allows the reconciler to backfill NULL versions on `ready` rows via a
  periodic probe (e.g., `wp core version` called by the agent).

**Consequences:**
- The UI must handle NULL version gracefully (show "–" or "unknown").
- Version bumps (plugin/core updates) are out of scope for M10; they happen
  via wp-admin.

---

### 5. Admin username (60 chars) vs OS user (32 chars POSIX limit)

**Decision:** The `admin_username` column is VARCHAR(60), which is the WordPress
admin login character limit. The OS user that owns the domain is a separate
entity, capped at 32 characters by POSIX. These are **two different namespaces
by design** and must not be conflated.

**Rationale:**
- WordPress admin usernames can be longer than OS usernames (60 vs. 32).
- Separation avoids accidental coupling between OS accounts and WordPress accounts.
- The domain is owned by the OS user; WordPress runs within that user's docroot.
- Admin password rotation goes through wp-admin, not the panel; the OS user
  password is separate.

**Consequences:**
- When provisioning, the handler must accept a 60-char admin_username from the
  user and pass it to the agent (which uses it for `wp core install`).
- The handler must not assume the admin username is a valid OS username.
- Documentation must clarify the asymmetry so operators don't try to SSH as
  the WordPress admin (they can't; that's not an OS account).

---

### 6. Admin password never stored

**Decision:** The admin password is **never stored** in the database or on disk.
It is generated (or provided by the user), used once to set up WordPress,
and immediately discarded. The API responds with the password once; thereafter,
password recovery goes through wp-admin's "Forgot Password" flow.

**Rationale:**
- Reduces attack surface: no password database to breach.
- Matches industry practice (WordPress hosters do not store admin passwords).
- Users expect to set their own password on first login, or recover via email.

**Consequences:**
- The API response to `POST /wordpress` includes the plain-text password.
  The UI must display it once and warn the user to save it.
- If the user loses the password, they use WordPress's built-in password reset.
- The agent receives the password via stdin (not argv or env) for the single
  `wp core install` call.

---

### 7. Locale defaults to en_US

**Decision:** The `locale` column is VARCHAR(16) NOT NULL DEFAULT 'en_US'.
When an install is created, the user can specify a locale; if not provided,
English (US) is the default.

**Rationale:**
- WordPress requires a locale; defaulting avoids a NULL that complicates the
  UX and agent logic.
- `en_US` is the most common default.
- Users can customize locale per install (Step 6 UI).

**Consequences:**
- The agent passes `--locale=<locale>` to `wp core download`.
- Locale changes after install are a WordPress admin task (Settings > General).

---

### 8. last_error is bounded (1024 chars, NOT NULL DEFAULT '')

**Decision:** The `last_error` column is VARCHAR(1024) NOT NULL DEFAULT ''.
It holds a truncated error message when an install enters `failed` state.
Empty string (not NULL) is the default for `ready` and `pending` rows.

**Rationale:**
- Errors are often long (full stack traces, stderr output); 1024 chars is
  enough for a one-line truncation and diagnostic clues.
- NOT NULL simplifies the GORM update path (`UpdateStatus` can always write
  to this field without checking for nil).
- Empty string is clearer than NULL for "no error yet" (NULL implies unknown).
- The API handler truncates agent stderr to 1024 chars before writing (Step 4).

**Consequences:**
- Error details beyond 1024 chars are lost; plan accordingly (e.g., ensure
  agent logs are available elsewhere for full debugging).
- The reconciler can display last_error in the UI as a tooltip on `failed` rows.

---

## Schema

```sql
CREATE TABLE `wordpress_installs` (
  `id` CHAR(26) NOT NULL PRIMARY KEY,          -- ULID
  `user_id` CHAR(26) NOT NULL,                 -- FK to users
  `domain_id` CHAR(26) NOT NULL UNIQUE,        -- FK to domains
  `db_id` CHAR(26) NOT NULL,                   -- FK to databases (ON DELETE RESTRICT)
  `version` VARCHAR(32) NULL,                  -- NULL until agent reports
  `admin_username` VARCHAR(60) NOT NULL,       -- WordPress admin login
  `admin_email` VARCHAR(320) NOT NULL,         -- WordPress admin email
  `locale` VARCHAR(16) NOT NULL DEFAULT 'en_US',
  `status` VARCHAR(16) NOT NULL DEFAULT 'pending',
  `last_error` VARCHAR(1024) NOT NULL DEFAULT '',
  `created_at` DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
  `updated_at` DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6),
  CHECK (`status` IN ('pending','installing','ready','failed','deleting','cloning')),
  FOREIGN KEY `fk_wpinstalls_user` (`user_id`) REFERENCES `users`(`id`) ON DELETE CASCADE,
  FOREIGN KEY `fk_wpinstalls_domain` (`domain_id`) REFERENCES `domains`(`id`) ON DELETE CASCADE,
  FOREIGN KEY `fk_wpinstalls_db` (`db_id`) REFERENCES `databases`(`id`) ON DELETE RESTRICT,
  KEY `idx_wpinstalls_user_id` (`user_id`),
  KEY `idx_wpinstalls_status` (`status`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
```

---

## Repository Interface

The data layer implements:

- `Create(ctx, install)` — insert a new record
- `FindByID(ctx, id)` — retrieve by install ID
- `FindByIDAndUserID(ctx, id, userID)` — retrieve with owner check (critical for API authorization)
- `FindByDomainID(ctx, domainID)` — check if a domain already hosts an install
- `ListByUserID(ctx, userID, opts)` — paginated user installs
- `List(ctx, opts)` — paginated all installs (admin view)
- `UpdateStatus(ctx, id, status, lastError, version)` — atomic status/error/version update
- `Delete(ctx, id)` — remove a record

The `UpdateStatus` method is a single atomic operation so two concurrent writers
(reconciler + agent callback) cannot race on status/error/version fields.
Passing nil for `lastError` or `version` leaves that column unchanged.

---

## Consequences

### Positive

- **Durable state:** WordPress installs are recorded in the panel's DB,
  enabling reconciliation and cross-user visibility.
- **Clear async model:** The API accepts a request, inserts a `pending` row,
  and returns 202 with the row. The agent and reconciler drive it to `ready`.
- **Fault-tolerant:** If the panel crashes during install, the reconciler
  re-drives stuck `installing` rows after a timeout.
- **Audit trail:** Each install has `created_at` and `updated_at`; admins can
  track when installs were provisioned.

### Negative

- **No soft-delete:** Installs are hard-deleted when torn down. Recoverability
  is limited to database backups. Acceptable for MVP; soft-delete can be
  added later if compliance requires an audit trail of deletions.
- **Version discovery lag:** After install, `version` remains NULL until the
  agent reports back. The UI must handle this gracefully.
- **Per-domain cap:** One install per domain is a design constraint. If a user
  needs multiple WordPress instances, they must use multiple domains.

---

## Open Questions [OPEN]

**[OPEN] #1: WordPress core version pinning.**
Should installs pin a specific WordPress version, or always update to latest?
Current plan: install latest stable, with an optional `version` field on the
install request (not in Step 1; deferred). Admin can override at install time.

**[OPEN] #2: Cloning across PHP pools.**
What if src and dst domains are on different PHP versions?
`wordpress.clone` (Step 3) runs on the agent's PHP, not the target site's.
Proposed: reject clone if src and dst domains point at different PHP pools.
Surface the conflict in the UI (Step 7).

**[OPEN] #3: Orphaned-DB cleanup policy.**
If an install's row goes `failed` and the user retries with a different domain/DB,
do we auto-drop the stranded DB? Proposed: no. Retries are user-driven; strand
remains until user deletes via the Databases page. Document in runbook.

---

## Cross-References

- **`plans/m10-wordpress.md`** — Full implementation blueprint with 9 steps.
- **ADR-0025** — Per-user systemd slices (WordPress processes run in user slices).
- **ADR-0023** — M9 PHP-FPM pools (each domain has a pool).
- **ADR-0007** — M7 database entity lifecycle (installs reuse M7's database
  provisioning and teardown).
- **`docs/BLUEPRINT.md` §6.10** — M10 scope and dependencies.
- **`docs/runbooks/wordpress.md`** — Operational guide (TBD Step 9).

---

## Related Artifacts

- Migration: `000033_create_wordpress_installs.{up,down}.sql`
- Model: `panel-api/internal/models/wordpress_install.go`
- Repository: `panel-api/internal/repository/wordpress_install_repository.go`
