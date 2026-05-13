DROP INDEX IF EXISTS ix_email_forwarders_domain_local ON email_forwarders;
-- Refuse to re-add NOT NULL when any NULL rows exist; operator must
-- DELETE WHERE mailbox_id IS NULL first or this fails loud.
ALTER TABLE email_forwarders MODIFY COLUMN mailbox_id CHAR(26) NOT NULL;
