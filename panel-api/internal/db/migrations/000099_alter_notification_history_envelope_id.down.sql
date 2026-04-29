ALTER TABLE notification_history
  DROP INDEX idx_notification_history_envelope,
  DROP COLUMN envelope_id;
