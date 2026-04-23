-- M6.5 Mailbox Shares (ACL-like sharing via Stalwart Mailbox.shareWith)
-- Stalwart integration: JMAP Mailbox/set + shareWith patch
-- Jabali is truth; reconciler converges to Stalwart

CREATE TABLE mailbox_shares (
  id CHAR(26) PRIMARY KEY,
  owner_mailbox_id CHAR(26) NOT NULL COMMENT 'mailbox being shared out',
  shared_with_mailbox_id CHAR(26) NOT NULL COMMENT 'mailbox receiving access',
  rights JSON NOT NULL COMMENT 'MailboxRights map (read, addItems, removeItems, etc.)',
  managed_by VARCHAR(16) DEFAULT 'm6.5',
  created_at TIMESTAMP(6) DEFAULT CURRENT_TIMESTAMP(6),

  CONSTRAINT fk_share_owner FOREIGN KEY (owner_mailbox_id) REFERENCES mailboxes(id) ON DELETE CASCADE,
  CONSTRAINT fk_share_other FOREIGN KEY (shared_with_mailbox_id) REFERENCES mailboxes(id) ON DELETE CASCADE,

  UNIQUE KEY uq_share (owner_mailbox_id, shared_with_mailbox_id),

  KEY idx_shared_with (shared_with_mailbox_id)
);
