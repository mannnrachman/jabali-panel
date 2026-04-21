-- M6 Step 1 rollback.
--
-- Order matters: drop triggers first (they reference mailboxes + domains
-- columns we're about to drop), then the mailboxes table, then the column
-- additions on domains.

DROP TRIGGER IF EXISTS trg_domains_after_update_resync_mailboxes;
DROP TRIGGER IF EXISTS trg_mailboxes_before_update;
DROP TRIGGER IF EXISTS trg_mailboxes_before_insert;

DROP TABLE IF EXISTS mailboxes;

ALTER TABLE domains
  DROP COLUMN IF EXISTS email_enabled_at,
  DROP COLUMN IF EXISTS dkim_public_key,
  DROP COLUMN IF EXISTS dkim_selector,
  DROP COLUMN IF EXISTS email_enabled;
