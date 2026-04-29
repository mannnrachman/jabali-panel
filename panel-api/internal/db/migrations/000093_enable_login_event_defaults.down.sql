-- Revert to default-off for the two login kinds. We can't tell which
-- rows the up migration flipped vs. which an admin re-enabled manually,
-- so this is a best-effort restore — operators who disabled them
-- before the up migration will have to re-disable.
UPDATE notification_event_settings
   SET enabled = 0, updated_at = NOW()
 WHERE event_kind IN ('ssh.login', 'admin.login');
