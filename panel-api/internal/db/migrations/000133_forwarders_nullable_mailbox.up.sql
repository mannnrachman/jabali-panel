-- Drop NOT NULL on email_forwarders.mailbox_id so domain-scoped
-- standalone forwarders (no associated mailbox — pure redirect
-- aliases like `info@dom → external@gmail.com`) can be persisted.
--
-- Imported by the DA migration writer from /etc/virtual/<dom>/aliases
-- (DA's alias format). M65 mailbox-keyed reconciler phase filters
-- NULL-mailbox rows; a follow-up domain-scoped phase pushes them to
-- Stalwart as Principal type=list entries (deferred).
--
-- ManagedBy='m35-da-import' tags imported rows so the future
-- reconciler can target them without touching M6.5-managed rows.
ALTER TABLE email_forwarders MODIFY COLUMN mailbox_id CHAR(26) NULL;
CREATE INDEX IF NOT EXISTS ix_email_forwarders_domain_local
  ON email_forwarders(domain_id, local_part);
