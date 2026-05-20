ALTER TABLE mail_outbound_policy
  DROP COLUMN last_error,
  DROP COLUMN last_applied_at,
  DROP COLUMN stalwart_id;
