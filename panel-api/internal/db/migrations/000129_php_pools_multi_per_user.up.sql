-- M35.8 P6: allow multiple PHP-FPM pools per panel user — one per
-- distinct PHP version. Pre-M35 jabali enforced one pool per user
-- (uniq_user_pool) which broke any cpanel migration where domains
-- ran a mix of PHP versions. Drop the original unique key and add a
-- composite (user_id, php_version) so each (user, version) pair is
-- unique but a user can own pools for 7.4 + 8.2 + 8.3 simultaneously.
ALTER TABLE php_pools DROP INDEX uniq_user_pool;
ALTER TABLE php_pools ADD UNIQUE KEY uniq_user_phpver (user_id, php_version);
