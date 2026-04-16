ALTER TABLE users ADD COLUMN username VARCHAR(32) NULL AFTER email;
ALTER TABLE users ADD UNIQUE INDEX ux_users_username (username);

-- Backfill: admins stay NULL (panel-only). Non-admins get email prefix.
-- In this DB there's a single non-admin user and no collisions, so a plain
-- SUBSTRING_INDEX is safe. If a future migration run ever has collisions
-- the ALTER will fail loudly and operators can reconcile manually.
UPDATE users
   SET username = SUBSTRING_INDEX(email, '@', 1)
 WHERE is_admin = 0 AND username IS NULL;
