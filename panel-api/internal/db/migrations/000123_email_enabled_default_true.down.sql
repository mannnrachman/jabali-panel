-- Revert default to 0 (pre-ADR-0080 behaviour). Existing rows untouched.
ALTER TABLE domains
  MODIFY COLUMN email_enabled TINYINT(1) NOT NULL DEFAULT 0;
