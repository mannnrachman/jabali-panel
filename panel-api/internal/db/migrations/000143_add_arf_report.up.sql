-- M47 Wave 4 — ARF (Abuse Feedback Report) ingest (RFC 5965).
--
-- Receivers (Gmail/Microsoft/Yahoo) email back when their users click
-- "Report spam"; Stalwart parses them into ArfExternalReport schema
-- objects and the panel-api eventsource (mail_abuse_ingest) polls
-- those, then writes one row here per feedback envelope.
--
-- Append-only, 90-day retention (matches dmarc_aggregate cadence).
-- No FKs (DB-as-truth, ADR-0002). utf8mb4 per scar.
CREATE TABLE arf_report (
  id                 CHAR(26)     NOT NULL PRIMARY KEY,
  stalwart_id        VARCHAR(128) NOT NULL,                       -- the ArfExternalReport id (idempotency gate)
  received_at        DATETIME     NOT NULL,
  feedback_type      VARCHAR(32)  NOT NULL DEFAULT 'abuse',       -- abuse|fraud|virus|other|not-spam
  reporter           VARCHAR(253) NOT NULL DEFAULT '',
  original_rcpt      VARCHAR(320) NOT NULL DEFAULT '',
  original_mail_from VARCHAR(320) NOT NULL DEFAULT '',
  source_ip          VARCHAR(45)  NOT NULL DEFAULT '',
  incidents          INT UNSIGNED NOT NULL DEFAULT 1,
  user_agent         VARCHAR(255) NOT NULL DEFAULT '',
  reporting_mta      VARCHAR(253) NOT NULL DEFAULT '',
  arrival_date       DATETIME     NULL,
  created_at         DATETIME(6)  NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
  UNIQUE KEY uq_arf_stalwart_id (stalwart_id),
  KEY idx_arf_received (received_at),
  KEY idx_arf_rcpt (original_mail_from)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
