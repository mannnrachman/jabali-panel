-- M33.2 — async mail YARA scanner. Periodic walker scans Stalwart
-- mailboxes via JMAP, yr-scans attachments, on hit moves the message
-- to a per-account "Malware" folder + emits a malware_quarantine_added
-- event (source=yara). ADR-0079.

-- Per-mailbox cursor + accounting. PRIMARY KEY is account_id+mailbox_id
-- because JMAP mailbox ids are scoped to accounts.
CREATE TABLE mail_scan_state (
  account_id          VARCHAR(64)  NOT NULL,
  mailbox_id          VARCHAR(64)  NOT NULL,
  last_email_id       VARCHAR(64)  NULL,
  last_received_at    TIMESTAMP    NULL,
  scanned_count       INT UNSIGNED NOT NULL DEFAULT 0,
  hit_count           INT UNSIGNED NOT NULL DEFAULT 0,
  failure_count       INT UNSIGNED NOT NULL DEFAULT 0,
  scanned_at          TIMESTAMP    NOT NULL DEFAULT CURRENT_TIMESTAMP
                                   ON UPDATE CURRENT_TIMESTAMP,
  quarantine_mailbox          VARCHAR(64) NULL,
  quarantine_mailbox_verified TIMESTAMP   NULL,
  PRIMARY KEY (account_id, mailbox_id),
  KEY idx_mail_scan_state_scanned_at (scanned_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- DLQ for tick failures. Bounded retention via app-side purge (oldest
-- 10k rows kept; older purged on tick). Surfaced in admin Mail-scan card.
CREATE TABLE mail_scan_failures (
  id            VARCHAR(26) NOT NULL PRIMARY KEY,
  account_id    VARCHAR(64) NOT NULL,
  mailbox_id    VARCHAR(64) NOT NULL,
  email_id      VARCHAR(64) NULL,
  attachment    VARCHAR(255) NULL,
  reason        VARCHAR(64)  NOT NULL,
  detail        TEXT NULL,
  attempted_at  TIMESTAMP    NOT NULL DEFAULT CURRENT_TIMESTAMP,
  KEY idx_mail_scan_failures_at (attempted_at),
  KEY idx_mail_scan_failures_account (account_id, mailbox_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

ALTER TABLE malware_settings
  ADD COLUMN mail_scan_enabled            TINYINT(1)   NOT NULL DEFAULT 0,
  ADD COLUMN mail_scan_all_folders        TINYINT(1)   NOT NULL DEFAULT 0,
  ADD COLUMN mail_scan_skip_addresses     TEXT         NOT NULL DEFAULT (''),
  ADD COLUMN mail_scan_max_attachment_mb  INT UNSIGNED NOT NULL DEFAULT 25,
  ADD COLUMN mail_scan_timeout_sec        INT UNSIGNED NOT NULL DEFAULT 10,
  ADD COLUMN mail_scan_per_tick_budget    INT UNSIGNED NOT NULL DEFAULT 200;
