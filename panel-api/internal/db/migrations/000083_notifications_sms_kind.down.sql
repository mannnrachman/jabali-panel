-- Rollback: drop 'sms' from notification_channels.kind ENUM.
-- Any sms rows must be deleted by the operator first; this migration
-- does not destroy data.
ALTER TABLE notification_channels
  MODIFY COLUMN kind ENUM('email','slack','discord','ntfy','webhook','webpush') NOT NULL;
