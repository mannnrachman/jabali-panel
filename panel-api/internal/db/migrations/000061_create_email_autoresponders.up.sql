-- M6.5 Email Autoresponders
-- Stalwart integration: JMAP VacationResponse (RFC 8621 §8)
-- One per mailbox; jabali is truth

CREATE TABLE email_autoresponders (
  mailbox_id CHAR(26) PRIMARY KEY COMMENT 'one autoresponder per mailbox',
  enabled BOOLEAN NOT NULL DEFAULT FALSE,
  from_date TIMESTAMP(6) NULL COMMENT 'vacation start date',
  to_date TIMESTAMP(6) NULL COMMENT 'vacation end date',
  subject VARCHAR(998) NULL COMMENT 'autoresponse subject (RFC 5322)',
  text_body TEXT NULL COMMENT 'plaintext version of the autoresponse',
  html_body MEDIUMTEXT NULL COMMENT 'HTML version of the autoresponse',
  managed_by VARCHAR(16) DEFAULT 'm6.5',
  updated_at TIMESTAMP(6) DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6),

  CONSTRAINT fk_ar_mailbox FOREIGN KEY (mailbox_id) REFERENCES mailboxes(id) ON DELETE CASCADE
);
