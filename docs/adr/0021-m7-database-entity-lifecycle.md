# ADR-0021: M7 Databases — Entity lifecycle (naming, quota, cascade, password)

**Date:** 2026-04-17
**Status:** Accepted
**Deciders:** Shuki

## Context

M7 introduces two new top-level entities — `databases` and `database_users` — plus a grants relation between them. The blueprint doesn't fully specify four operational aspects of their lifecycle: naming/namespace collisions, per-package quotas, cascade on owning-user delete, and db-user password storage. This ADR locks in all four together because they reference each other (the cascade path has to know about grants, the quota check has to know about naming, and password storage interacts with the SSO flow in ADR-0020).

## Decision

### Naming

**Database and database-user names are prefixed with the owning user's username.** Example: user `alice` creating a database called `wp` stores the row as `name = "alice_wp"`, and MariaDB sees `CREATE DATABASE \`alice_wp\``. A `UNIQUE(user_id, name)` constraint at the panel DB level prevents duplicates per user; MariaDB's global namespace is further protected by the prefix.

Rationale: MariaDB has one global database namespace. Prefixing is cheap, human-readable, and consistent with how cPanel/DirectAdmin have done it for 20 years.

### Quota

**`hosting_packages` grows two new columns: `max_databases` (already exists) and `max_database_users` (new).** Both default to 0 meaning "unlimited". Handlers check the quota before calling the agent; the check is a `SELECT COUNT(*)` against the owning user's non-deleted databases / db-users. Quota violation returns `HTTP 409` with `{ error: "quota_exceeded", resource: "databases"|"database_users", limit: N }`.

Rationale: the user asked explicitly for `max_database_users` as a package-level knob — keeps per-package plans coherent and matches the existing `max_domains` pattern.

### Cascade on user delete

**Cascade is inline and atomic inside the user-delete handler, not async via the reconciler.** The handler's flow on `DELETE /api/v1/users/:id`:

1. Load all `databases` owned by `user_id`.
2. Load all `database_users` belonging to those databases.
3. For each db-user: call `db_user.drop` on the agent (revokes + drops the MariaDB user).
4. For each database: call `db.drop` on the agent.
5. Delete the grant rows, db-user rows, database rows in a single GORM transaction.
6. Delete the user row.

Any agent failure aborts the whole handler with `HTTP 500`, rolls back the transaction, and leaves the Jabali user in place. No partial state.

Rationale: user-delete is a rare, admin-initiated operation; the cost of blocking for 2-10 seconds is negligible compared to the cost of orphaned MariaDB databases. Making it atomic means no orphan-detection complexity on the hot path.

### Password storage

**Database-user passwords are stored bcrypt-hashed at rest. The plaintext is returned to the API caller exactly once, at create-time and at rotate-time.**

- On `POST /api/v1/database-users`: the handler generates a random 32-char password, bcrypt-hashes it, stores the hash, and returns `{ id, username, password_plaintext }` in the response. The UI displays it once with a "copy to clipboard" button and a warning that it won't be shown again.
- On `POST /api/v1/database-users/:id/rotate-password`: same flow — generate new password, bcrypt-hash, agent `ALTER USER` to change it in MariaDB, return plaintext once.
- No reveal endpoint. No plaintext at rest.
- Rotate is a first-class button on every row of the db-users UI, so forgotten passwords cost one click, not a support ticket.

Rationale: "reveal once" is the industry norm for good reason (minimises blast radius of a panel DB dump). Making rotate one click mitigates the real-world pain of forgotten passwords, which otherwise becomes the #1 complaint in control-panel reviews.

## Alternatives Considered

Captured inline above under each sub-decision. Highlights:

- **ULID-only naming** (`01HK3…_wp`): unparseable to users; rejected.
- **Async cascade via reconciler:** tolerates agent failure gracefully but creates an orphan window; rejected in favour of atomic handler cascade for M7 scope.
- **Plaintext password storage with reveal endpoint:** forbidden by `~/.claude/rules/security.md` and common-sense threat model; rejected.
- **Per-user global quota instead of per-package:** less flexible; rejected in favour of package-based tiering that matches `max_domains`.

## Consequences

### Positive
- Naming collisions impossible under `UNIQUE(user_id, name)` + MariaDB prefix.
- Quota model is consistent across M2 (domains), M7 (databases), and future milestones (email mailboxes etc.).
- User-delete is atomic — no orphan-detection code needed in M7's scope.
- Password rotation is a trivial UX path; forgetting a password costs one click.

### Negative
- Inline cascade on user-delete can take multiple seconds on users with many databases. Mitigation: 30s handler timeout cap; document in runbook; most users have < 10 databases.
- Bcrypt-hashed DB passwords mean no "show password" for the user — must rotate to recover. Acceptable tradeoff.
- Prefix-based naming creates visibly long DB names (`alice_wordpress_staging`). Mitigation: cosmetic only; show the unprefixed form in the UI.

### Risks
- **Slow cascade on pathological user** (50+ databases × 5 users each = 250 agent calls). Mitigation: keep the agent commands fast; add a batch `db.purge_user` command later if this becomes painful in practice.
- **Password-plaintext only in the response body** — if the HTTP response is logged somewhere stupid, it leaks. Mitigation: panel's access log already redacts response bodies; verify this in Phase-1 review.
