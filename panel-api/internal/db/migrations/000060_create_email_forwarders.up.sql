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

  -- Aliases: (domain_id, local_part) uniqueness. External forwards leave local_part
  -- NULL and MariaDB treats multiple NULLs as distinct in UNIQUE keys — so external
  -- rows do NOT collide here. Stalwart independently rejects cross-mailbox alias
  -- collisions at the JMAP layer if anything slips past the app check.
  --
  -- (MariaDB 1901 rejects the generated-column CASE pattern originally tried for
  -- NULL-aware uniqueness; composite unique key delivers the same constraint for
  -- our use case.)
  UNIQUE KEY uq_alias_local (domain_id, local_part),

  -- External forwards: prevent duplicate (mailbox_id, type, target) pairs
  UNIQUE KEY uq_external_forward (mailbox_id, type, target),

  KEY idx_domain (domain_id),
  KEY idx_mailbox (mailbox_id)
);
