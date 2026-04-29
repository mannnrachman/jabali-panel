-- Flip ssh.login and admin.login from default-off to default-on.
-- Existing installs were seeded with enabled=0 by EnsureDefaults; the
-- product decision (2026-04-29) is that login events ship enabled out
-- of the box. New installs pick up the new default via the meta table;
-- this migration brings already-seeded rows in line.
UPDATE notification_event_settings
   SET enabled = 1, updated_at = NOW()
 WHERE event_kind IN ('ssh.login', 'admin.login')
   AND enabled = 0;
