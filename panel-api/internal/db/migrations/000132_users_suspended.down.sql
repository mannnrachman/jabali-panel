DROP INDEX IF EXISTS idx_users_suspended ON users;
ALTER TABLE users
  DROP COLUMN IF EXISTS suspend_reason,
  DROP COLUMN IF EXISTS suspended_at,
  DROP COLUMN IF EXISTS suspended;
