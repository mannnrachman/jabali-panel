-- M6.5 Email Forwarders (aliases + sieve forwards)
-- Stalwart integration: x:UserAccount.aliases + x:SieveUserScript
-- Jabali is truth; reconciler converges to Stalwart

CREATE TABLE email_forwarders (
  id CHAR(26) PRIMARY KEY,
  mailbox_id CHAR(26) NOT NULL COMMENT 'target mailbox (recipient of the forwarded mail)',
  domain_id CHAR(26) NOT NULL COMMENT 'domain of the source address',
  type ENUM('alias','external') NOT NULL COMMENT 'alias = local alias; external = forward to outside email',
  local_part VARCHAR(64) NULL COMMENT 'NOT NULL for type=alias; source local-part (e.g. "sales")',
  target VARCHAR(255) NOT NULL COMMENT 'for external: outside email; for alias: source@domain for display',
  enabled BOOLEAN NOT NULL DEFAULT TRUE,
  managed_by VARCHAR(16) DEFAULT 'm6.5',
  created_at TIMESTAMP(6) DEFAULT CURRENT_TIMESTAMP(6),
  updated_at TIMESTAMP(6) DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6),

  CONSTRAINT fk_fwd_mailbox FOREIGN KEY (mailbox_id) REFERENCES mailboxes(id) ON DELETE CASCADE,
  CONSTRAINT fk_fwd_domain FOREIGN KEY (domain_id) REFERENCES domains(id) ON DELETE CASCADE,

  -- Alias local-part must be unique per domain across ALL mailboxes (Stalwart rejects duplicates)
  -- Generated column so MariaDB can enforce uniqueness only for aliases
  alias_key VARCHAR(320) AS (CASE WHEN type='alias' THEN CONCAT(domain_id, '::', local_part) ELSE NULL END) STORED UNIQUE,

  -- For external forwards: prevent duplicate (mailbox_id, type, target) pairs
  UNIQUE KEY uq_external_forward (mailbox_id, type, target),

  KEY idx_domain (domain_id),
  KEY idx_mailbox (mailbox_id)
);
