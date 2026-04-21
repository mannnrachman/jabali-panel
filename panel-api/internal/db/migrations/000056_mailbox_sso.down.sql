DROP TABLE IF EXISTS mailbox_sso_tokens;

ALTER TABLE mailboxes
  DROP COLUMN password_enc;
