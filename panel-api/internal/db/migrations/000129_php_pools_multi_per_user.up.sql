-- M35.8 P6: allow multiple PHP-FPM pools per panel user — one per
-- distinct PHP version. Pre-M35 jabali enforced one pool per user
-- (uniq_user_pool) which broke any cpanel migration where domains
-- ran a mix of PHP versions.
--
-- Order matters: uniq_user_pool is the index MariaDB uses to back
-- the fk_pool_user FK. Dropping it first errors with 1553. Add the
-- new composite (user_id, php_version) FIRST so MariaDB has a
-- user_id-prefixed index satisfying the FK, then drop the old
-- single-column unique.
--
-- IF NOT EXISTS / IF EXISTS make the migration idempotent on hosts
-- where an earlier (buggy) version of this file half-applied.
ALTER TABLE php_pools ADD UNIQUE KEY IF NOT EXISTS uniq_user_phpver (user_id, php_version);
ALTER TABLE php_pools DROP INDEX IF EXISTS uniq_user_pool;
