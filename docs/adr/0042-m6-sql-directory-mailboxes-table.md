# ADR-0042: M6 SQL directory — Stalwart reads `jabali_panel.mailboxes`

**Date**: 2026-04-21
**Status**: accepted (amended 2026-04-21 by ADR-0045 — cache model clarified: v0.16's SqlDirectory syncs on every auth, no TTL window; no explicit invalidation API needed for credential/quota changes)
**Deciders**: shuki + Claude
**Related**: ADR-0002 (DB as source of truth), ADR-0003 (one write path = API), ADR-0013 (inline best-effort), ADR-0041 (RocksDB + Bulwark), ADR-0045 (v0.16 management surface)

## Context

Stalwart v0.16.0 supports four directory backend types for account/password/quota lookups: `internal` (its own RocksDB-stored directory), `sql`, `ldap`, and `oidc`. ADR-0041 already established that mail *content* lives in RocksDB. Account *identity* — who can log in, with what password hash, up to what quota — needs a home that also:

1. Participates in the panel's one-write-path invariant (ADR-0003) so provisioning is a single SQL transaction.
2. Stays authoritative on the panel side so the reconciler can drive domain-level email state without asking Stalwart.
3. Survives `systemctl stop jabali-stalwart` without the operator having to back up a second store.

Stalwart's `internal` directory would break all three — it's RocksDB-backed, owned by the Stalwart process, and has its own migration/backup surface. `ldap` and `oidc` add a separate daemon the panel would have to ship.

Only `sql` meets the constraints: Stalwart reads the panel's MariaDB directly on every auth, reusing our existing backup, migration, and HA posture.

### v0.16.0 filter requirement

Stalwart v0.16.0 changed the SQL-directory contract: filter queries must match on **full email address** (`alice@example.com`), not bare account name. Our schema provides `email_cached` (a denormalised `local_part + '@' + domains.name`) maintained by triggers, so Stalwart's parameterised lookup is a single indexed equality match — no join cost at auth time.

## Decision

### 1. A new `mailboxes` table in the existing `jabali_panel` MariaDB schema

```sql
CREATE TABLE mailboxes (
  id CHAR(26) NOT NULL PRIMARY KEY,                        -- ULID, Crockford base32 (internal/ids)
  domain_id CHAR(26) NOT NULL,
  local_part VARCHAR(64) NOT NULL,
  email_cached VARCHAR(320) NOT NULL,                      -- populated by trigger, kept in sync
  password_hash VARCHAR(255) NOT NULL,                     -- bcrypt cost 12; prefix-ready for {scheme}hash
  quota_bytes BIGINT UNSIGNED NOT NULL DEFAULT 1073741824, -- 1 GiB default
  is_disabled TINYINT(1) NOT NULL DEFAULT 0,
  last_usage_bytes BIGINT UNSIGNED NOT NULL DEFAULT 0,     -- sampled from Stalwart JMAP every 5 min
  last_usage_at DATETIME(6) NULL,
  created_at DATETIME(6) NOT NULL,
  updated_at DATETIME(6) NOT NULL,
  UNIQUE KEY ux_mailboxes_email_cached (email_cached),
  KEY ix_mailboxes_domain (domain_id),
  CONSTRAINT fk_mailboxes_domain FOREIGN KEY (domain_id)
    REFERENCES domains(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin;
```

- `id` is `CHAR(26)` — matches every other panel table (see `users.id`, `domains.id` etc.; `internal/ids` owns ULID generation).
- `local_part` is authoritative (user input, case-preserved-for-display but stored lowercase per `internal/mailaddr`). `email_cached` is derived.
- `password_hash` is bcrypt cost 12 (Stalwart's SQL-directory default scheme). `VARCHAR(255)` leaves room for a future `{argon2id}…` prefix without a migration.
- No `password_enc` AES-GCM column. The adversarial review (plan §7) established that storing both a reversible envelope and a verify hash doubles the secret surface without buying anything — user sees the plaintext once in the `POST /api/v1/mailboxes` response, panel bcrypts, plaintext is never persisted.

### 2. Trigger-maintained `email_cached` (no STORED generated column)

`BEFORE INSERT` + `BEFORE UPDATE` triggers set `email_cached = CONCAT(local_part, '@', (SELECT name FROM domains WHERE id = NEW.domain_id))`. A third trigger on `domains` resyncs `email_cached` if a domain is renamed (defensive — no panel path renames today, but direct-SQL is possible).

Portability trade-off: MariaDB's `STORED` generated columns **cannot** reference subquery-derived values in a cross-table join expression. Two options considered:

- STORED generated column on just `(local_part, domain_id)` → forces Stalwart's filter to JOIN to `domains` on every auth. Unnecessary latency + lock contention.
- Triggers writing into a regular column → one-time cost on insert/update, no JOIN at auth. Chosen.

### 3. Domain-level email state on `domains`

Four new columns:

```sql
ALTER TABLE domains
  ADD COLUMN email_enabled TINYINT(1) NOT NULL DEFAULT 0,
  ADD COLUMN dkim_selector VARCHAR(64) NULL,
  ADD COLUMN dkim_public_key TEXT NULL,
  ADD COLUMN email_enabled_at DATETIME(6) NULL;
```

These live on `domains` (not a separate table) because they are 1:1 with a domain and because the DKIM public key needs to be readable alongside other domain metadata by both the reconciler (for DNS record injection) and the UI (for the "Copy DKIM public key" button on the Email tab). The private key stays on disk per ADR-0043.

### 4. Stalwart SQL-directory filter shape (query syntax verified in Step 2)

Stalwart's SQL directory config (ADR-0041's `/etc/stalwart/config.toml`, filled in Step 2 of the plan) will use four parameterised queries against this table:

- `query.name` — resolve account metadata: `SELECT local_part, domain_id, quota_bytes FROM mailboxes WHERE email_cached = ? AND is_disabled = 0`.
- `query.auth` — return the password hash for bcrypt compare: `SELECT password_hash FROM mailboxes WHERE email_cached = ? AND is_disabled = 0`.
- `query.domains` — enumerate hosted domains: `SELECT name FROM domains WHERE email_enabled = 1`.
- `query.emails` — confirm existence: `SELECT email_cached FROM mailboxes WHERE email_cached = ? AND is_disabled = 0`.

Stalwart does the bcrypt compare itself (v0.16.0 recognises bcrypt, pbkdf2, and argon2 hashes via scheme prefix); the panel never transmits the hash over the wire.

The exact v0.16.0 filter config-file syntax is not pinned here — Step 2 of the plan handles it with `stalwart --validate` against the rendered config. This ADR pins the semantic shape; the concrete config.toml block belongs to Step 2.

### 5. No panel DB user changes for Stalwart

Stalwart connects to MariaDB as a new user `jabali-stalwart-ro` (created by `install.sh`) with **SELECT-only** grants on `mailboxes` and `domains`. Password changes come from Bulwark via Stalwart's admin API, which updates `mailboxes.password_hash` via a separate SELECT+UPDATE user `jabali-stalwart-pw` (grants `SELECT, UPDATE(password_hash, updated_at) ON mailboxes` only). Two users, one grant-surface each, no schema-wide privilege to the mail stack.

## Consequences

### Positive

- Provisioning a mailbox is a single `INSERT` inside the panel's handler transaction. Agent's `mailbox.create` command is a JMAP cache-invalidate only — the account is reachable the instant the row commits.
- Disabling email on a domain is `UPDATE domains SET email_enabled=0` + one Stalwart reload. No per-mailbox cleanup required to stop auth.
- The reconciler's `reconcileMailboxUsage` loop has a natural backpressure: bounded batches of JMAP `Quota/get`, idempotent writes into `last_usage_*`.
- Backup + restore is the existing MariaDB backup path — no new operator skill.
- `jabali-stalwart-ro` / `-pw` privilege separation means a compromised Stalwart can't mass-delete accounts (no DELETE grant) or tamper with quotas (no UPDATE on `quota_bytes`).

### Negative

- Stalwart re-reads SQL on every auth. For high-traffic inboxes this is a small lookup cost per login. Stalwart caches results in-process; the agent's `mailbox.create` / `delete` / `set_password` commands SIGHUP-ish the Stalwart cache via JMAP invalidate. If we forget that invalidate, old credentials stay valid until Stalwart's internal TTL expires (default 60s). Mitigation: contract-tested at Step 3.
- Trigger-maintained `email_cached` can drift if anyone `INSERT`s directly with an explicit `email_cached` that contradicts `local_part + domain_id`. The trigger sets it BEFORE INSERT, so the explicit value is overwritten; direct SQL attacks can't bypass via that vector. But a later refactor that switches to `SKIP LOCKED` or `INSERT ... ON DUPLICATE KEY UPDATE` should re-verify trigger semantics.
- No `password_enc` means there is no "show me the password again" UI after creation. Post-create flows must capture the reveal-once plaintext (UI keeps it in memory until the modal closes). Lost plaintexts = rotate via `POST /api/v1/mailboxes/:id/rotate-password`. Documented in the plan's §1 "Password model" block.

### Rejected alternatives

- **Stalwart's `internal` directory (RocksDB-backed)**: breaks ADR-0002 (DB is truth) — Stalwart would own account state, the panel would have to sync via JMAP admin API. Two sources of truth.
- **LDAP directory**: would introduce OpenLDAP or slapd as a new daemon. No offsetting benefit.
- **STORED generated column for `email_cached`**: requires a JOIN expression in the column definition. MariaDB doesn't allow subquery in generated columns — portability blocker.
- **One-table schema (email as-is, LOWER() on every query)**: was the first sub-agent draft. Forces full-column scan for case-insensitive lookup, can't be indexed by LOWER() in older MariaDB. Derived `email_cached` column with a UNIQUE index beats it at every auth.

## Amendment 2026-04-21: explicit-null optional queries (live-test)

Live IMAP-LOGIN validation on VM 192.168.100.150 surfaced a trap. Stalwart
v0.16's `x:Directory` schema defaults `queryMemberOf` and
`queryEmailAliases` to **PostgreSQL-style** placeholder syntax:

    "queryMemberOf":     "SELECT member_of FROM group_members WHERE name = $1"
    "queryEmailAliases": "SELECT address FROM emails WHERE name = $1"

On a MySQL/MariaDB-backed SqlDirectory these queries fail at prepare
time (`mysql_async` rejects `$1` — MySQL uses `?`). Because
`SqlDirectory::authenticate` runs them AFTER a successful login
query to pull group + alias data, every login surfaces as
`NO [CONTACTADMIN] MySQL error` even though the bcrypt verify
itself succeeded.

Fix: the apply-plan MUST explicitly set the optional query fields to
`null`, not omit them:

    "queryMemberOf":     null,
    "queryEmailAliases": null,
    "columnClass":       null,
    "columnDescription": null,

Omitting them does NOT yield `None` — the registry create path fills
in the PostgreSQL strings as defaults. Shipping `null` explicitly
forces `Option<String>::None` and Stalwart skips the follow-up
queries entirely.

Contract test for Step 3 must include a "login succeeds without a
`group_members` or `emails` table present" case — otherwise regressions
slip past httptest mocks.

## Related

- ADR-0002 — DB is truth. This ADR extends it to mail directory.
- ADR-0003 — one write path. Stalwart reads, panel writes.
- ADR-0013 — inline best-effort. `mailbox.create` follows this pattern (row first, agent cache-invalidate second, response returns even if the invalidate failed).
- ADR-0041 — mail storage in RocksDB. This ADR fills the directory side.
- ADR-0043 — DKIM keys on disk, public key in `domains.dkim_public_key`.
- Plan: `plans/m6-email-stalwart.md` §1 (schema), §2 Step 2 (Stalwart config).
- Migration: `panel-api/internal/db/migrations/000054_create_mailboxes.up.sql`.
