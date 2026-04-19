-- Revert to per-domain uniqueness. Will fail if any domain currently has more
-- than one install — that's intentional, the operator must drop the extras
-- first since there's no automatic way to choose which install to keep.
ALTER TABLE `wordpress_installs` DROP INDEX `uniq_wpinstalls_domain_subdir`;
ALTER TABLE `wordpress_installs` ADD UNIQUE KEY `domain_id` (`domain_id`);
