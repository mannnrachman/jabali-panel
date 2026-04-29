-- Track the originating Redis Streams entry on every history row so
-- the inbox UI can correlate rows with the DLQ stream and surface a
-- "dead letter" badge. NULL is allowed both for legacy rows (written
-- before this column existed) and for in-app-only paths where the
-- dispatcher didn't have a stream-entry to attribute.
ALTER TABLE notification_history
  ADD COLUMN envelope_id VARCHAR(40) NULL,
  ADD INDEX idx_notification_history_envelope (envelope_id);
