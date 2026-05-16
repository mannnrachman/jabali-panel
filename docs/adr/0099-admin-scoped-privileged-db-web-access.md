# ADR-0099 — Admin-scoped privileged DB web access (phpMyAdmin/Adminer, all DBs)

**Date**: 2026-05-16
**Status**: accepted
**Deciders**: shuki (requested + chose admin all-DBs SSO), Claude (design)
**Related**: ADR-0020/0022 (phpMyAdmin SSO), ADR-0096 (root web terminal — comparable risk framing), M7, M46

## Context

Per-DB-user phpMyAdmin SSO (`POST /sso/phpmyadmin`) and the Postgres
Adminer equivalent (`POST /sso/adminer`) already ship: a hosting user
opens *their own* database, ownership-checked (`db.UserID ==
claims.UserID`), via a single-use token + a per-user shadow account
(`<user>_mysqladmin`, scoped `GRANT ALL ON `\``<user>\_%`\``.*`).

cPanel/WHM parity asks for the **operator** entry: log in to
phpMyAdmin/Adminer and see/edit **every** database on the box, including
ones created later. This is a different security profile and must be
named honestly.

## Decision

Ship a separate **admin-scoped** SSO path, distinct from the per-user one.

**Honest framing (non-negotiable):** the admin phpMyAdmin shadow is
effectively `ALL PRIVILEGES ON *.*` (minus account-management meta).
This **is a privileged web shell over every database on the server.**
That is inherent to "see and edit all databases incl. future ones" — it
is not a weakness to be grant-trimmed, and reviewers should not be told
the grant is meaningfully reduced. The Postgres path is Adminer as the
`postgres` superuser via the existing peer/secret. The threat model is
controlled by the **gating**, not the grant:

- `RequireAdmin` on `POST /api/v1/admin/databases/sso/{phpmyadmin,adminer}`.
- Same-origin (Origin/Referer) CSRF check — reused from the per-user handler.
- Single-use, short-TTL token (reuse the existing SSO token repo).
- A distinct audit line tagged `scope=admin` in `db_admin_audit`
  (separable from per-user SSO issuance).
- **No** per-DB ownership check (intentional — admin sees all).
- The MariaDB admin shadow (`jabali_pma_admin@localhost`) is created by
  the agent reusing the proven `db_mysqladmin_ensure` patterns
  (`panelUsernameRegex`, `EscapeMariaDBLiteral`, idempotent
  `CREATE USER IF NOT EXISTS` + `ALTER USER`, never echo mysql stderr);
  its password lives in a `0640 root:jabali` file written tmp+rename,
  never in the DB.

## Alternatives considered

- **Plain deep-link to `/phpmyadmin/` (manual login).** Lowest risk,
  least convenient; rejected — the box has no root DB password by
  design (ADR-0097), so there's nothing convenient to type.
- **Reuse per-user SSO from admin (pick a DB → open as its owner).**
  No all-DBs view; doesn't meet the parity ask. Rejected as the primary
  path (could be an additional convenience later).
- **Grant-trim the admin shadow to feel safer.** Rejected as dishonest:
  it still needs effectively global read+DML; pretending otherwise
  invites a rabbit-hole that doesn't change the threat model.

## Consequences

- A second high-privilege authenticated-admin surface (comparable to
  the ADR-0096 root terminal). Justified by RequireAdmin + CSRF +
  single-use token + audit; documented plainly in the runbook.
- New agent command `db.pma_admin.ensure`; one new secret file.
- If the admin account is ever abused, `db_admin_audit scope=admin`
  rows are the forensic trail.

## Amendment 2026-05-16 (live-VM smoke)

The sentinel-DatabaseID design assumed the SSO token tables would
accept an arbitrary `database_id`. They did not: `phpmyadmin_sso_tokens`
(`fk_sso_db`) and `adminer_sso_tokens` (auto-named) both had
`FOREIGN KEY (database_id) REFERENCES databases(id) ON DELETE CASCADE`,
so minting an admin token with `__M46_ADMIN_ALL__` failed the FK →
HTTP 500 ("mint failed"). Migration `000136` drops both FKs
(dynamically, via information_schema, so it is portable across
fresh installs). Per-user SSO safety is preserved: that path still
`FindByID`s the real database and 404s if absent, and every SSO token
is single-use with a ~5-min TTL — the ON-DELETE-CASCADE the FK
provided was marginal given that lifetime. Decision unchanged; only
the storage constraint was corrected.
