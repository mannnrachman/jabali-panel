-- M35.8 P1: per-mailbox Sieve filters. cpanel preserves filter
-- rules as plain-text files under <homedir>/etc/<dom>/<local>/filter.yaml;
-- jabali stores the raw sieve_text so the operator can edit + push
-- via Stalwart's JMAP SieveScript/set when needed. Real cpanel-to-
-- Sieve conversion is out of scope here — the raw cpanel rule body
-- lands in `cpanel_raw` for manual conversion until M6.5 wires a
-- Sieve authoring UI.
CREATE TABLE email_filters (
  id            CHAR(26)        NOT NULL PRIMARY KEY,
  mailbox_id    CHAR(26)        NOT NULL,
  name          VARCHAR(64)     NOT NULL,
  sieve_text    MEDIUMTEXT      NULL,
  cpanel_raw    MEDIUMTEXT      NULL,
  priority      INT             NOT NULL DEFAULT 0,
  enabled       TINYINT(1)      NOT NULL DEFAULT 1,
  managed_by    VARCHAR(16)     NOT NULL DEFAULT 'm6.5',
  created_at    DATETIME(6)     NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
  updated_at    DATETIME(6)     NOT NULL DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6),
  UNIQUE KEY uniq_mailbox_filter_name (mailbox_id, name),
  KEY ix_email_filters_mailbox (mailbox_id),
  CONSTRAINT fk_email_filter_mailbox FOREIGN KEY (mailbox_id) REFERENCES mailboxes(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
