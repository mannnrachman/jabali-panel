-- M14 follow-up: add 'sms' to notification_channels.kind ENUM.
-- The SMS channel POSTs `{to, body}` JSON to a user-configured gateway URL
-- (Twilio Functions, MessageBird, BulkSMS, generic gateway). HMAC-SHA256
-- signature header is identical to the webhook channel so receivers can
-- reuse the same verification path.
ALTER TABLE notification_channels
  MODIFY COLUMN kind ENUM('email','slack','discord','ntfy','webhook','webpush','sms') NOT NULL;
