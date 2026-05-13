-- M-suspend: per-user suspension flag + audit trail. Suspending a user:
--   1. flips users.suspended = 1 (gated by API)
--   2. sets Kratos identity state = inactive (blocks panel + webmail login)
--   3. flips every owned domains.is_enabled = 0 (reconciler removes the
--      nginx sites-enabled symlink so all sites serve 404 until lifted)
-- Unsuspend reverses 1 + 2; domain enable-state is restored by setting
-- is_enabled = 1 on every domain still owned by the user. SSH/SFTP is
-- gated by reconciler in a follow-up — for v1 the panel-side block + DNS
-- + nginx removal is enough to take the user fully offline.
ALTER TABLE users
  ADD COLUMN IF NOT EXISTS suspended TINYINT(1) NOT NULL DEFAULT 0,
  ADD COLUMN IF NOT EXISTS suspended_at DATETIME(6) NULL,
  ADD COLUMN IF NOT EXISTS suspend_reason VARCHAR(255) NOT NULL DEFAULT '';
CREATE INDEX IF NOT EXISTS idx_users_suspended ON users(suspended);
