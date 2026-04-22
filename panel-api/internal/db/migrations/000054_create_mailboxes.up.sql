-- M6 Step 1: mailboxes table + domain-level email state.
--
-- Stalwart's SQL directory (ADR-0042) reads this table for auth + quota.
-- Mail content lives in Stalwart's RocksDB (ADR-0041); this is directory only.
--
-- email_cached is a denormalised `local_part + '@' + domains.name`, kept in sync
-- by BEFORE INSERT / BEFORE UPDATE triggers. Stalwart's filter queries by
-- email_cached directly, no JOIN at auth time.
--
-- STORED generated columns are not used because MariaDB does not support
-- subqueries in generated column expressions. Triggers are the portable path.

CREATE TABLE mailboxes (
  id               CHAR(26)                NOT NULL PRIMARY KEY,
  domain_id        CHAR(26)                NOT NULL,
  local_part       VARCHAR(64)             NOT NULL,
  email_cached     VARCHAR(320)            NOT NULL,
  password_hash    VARCHAR(255)            NOT NULL,
  quota_bytes      BIGINT UNSIGNED         NOT NULL DEFAULT 1073741824,
  is_disabled      TINYINT(1)              NOT NULL DEFAULT 0,
  last_usage_bytes BIGINT UNSIGNED         NOT NULL DEFAULT 0,
  last_usage_at    DATETIME(6)             NULL,
  created_at       DATETIME(6)             NOT NULL,
  updated_at       DATETIME(6)             NOT NULL,
  UNIQUE KEY ux_mailboxes_email_cached (email_cached),
  KEY ix_mailboxes_domain (domain_id),
  CONSTRAINT fk_mailboxes_domain
    FOREIGN KEY (domain_id) REFERENCES domains(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- BEFORE INSERT + BEFORE UPDATE triggers keep email_cached in sync.
-- We use SET NEW.email_cached rather than a post-insert UPDATE to avoid
-- trigger recursion (MariaDB refuses AFTER INSERT/UPDATE triggers that
-- UPDATE the same table without raising max_sp_recursion_depth).

CREATE TRIGGER trg_mailboxes_before_insert
  BEFORE INSERT ON mailboxes
  FOR EACH ROW
    SET NEW.email_cached = CONCAT(
      NEW.local_part, '@',
      (SELECT name FROM domains WHERE id = NEW.domain_id)
    );

CREATE TRIGGER trg_mailboxes_before_update
  BEFORE UPDATE ON mailboxes
  FOR EACH ROW
    SET NEW.email_cached = IF(
      NEW.local_part <> OLD.local_part OR NEW.domain_id <> OLD.domain_id,
      CONCAT(NEW.local_part, '@', (SELECT name FROM domains WHERE id = NEW.domain_id)),
      NEW.email_cached
    );

-- If a domain is renamed (not currently exposed by the panel API, but
-- possible via direct SQL or a future admin flow), resync every mailbox
-- row that references it. UPDATE-on-UPDATE is safe here because this
-- trigger fires on `domains`, not `mailboxes`.

CREATE TRIGGER trg_domains_after_update_resync_mailboxes
  AFTER UPDATE ON domains
  FOR EACH ROW
    UPDATE mailboxes
      SET email_cached = CONCAT(local_part, '@', NEW.name)
      WHERE domain_id = NEW.id
        AND NEW.name <> OLD.name;

-- Domain-level email state: one row per domain already exists in `domains`;
-- we add the four email columns here (rather than a separate table) because
-- they are 1:1 with a domain and need to be read alongside other domain
-- metadata on every page load of the UI's Email tab.
--
-- Private DKIM key stays on disk at /etc/jabali-panel/dkim/<domain>.key
-- (ADR-0043). `dkim_public_key` here is the TXT-record value, so the
-- reconciler can re-publish after backup restore without regenerating.

ALTER TABLE domains
  ADD COLUMN email_enabled    TINYINT(1)   NOT NULL DEFAULT 0,
  ADD COLUMN dkim_selector    VARCHAR(64)  NULL,
  ADD COLUMN dkim_public_key  TEXT         NULL,
  ADD COLUMN email_enabled_at DATETIME(6)  NULL;
