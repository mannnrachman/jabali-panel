ALTER TABLE php_pools DROP INDEX uniq_user_phpver;
ALTER TABLE php_pools ADD UNIQUE KEY uniq_user_pool (user_id);
