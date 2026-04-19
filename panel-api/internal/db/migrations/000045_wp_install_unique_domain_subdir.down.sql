-- Revert to per-domain uniqueness. Will fail if any domain currently has
-- more than one install — that's intentional; the operator must drop the
-- extras first since there's no automatic way to choose which to keep.
-- Add the single-column unique back FIRST so the FK
-- (fk_wpinstalls_domain) keeps an index on `domain_id` while we drop the
-- composite (errno 1553 — same reason as the up migration).
ALTER TABLE `wordpress_installs` ADD UNIQUE KEY `domain_id` (`domain_id`);
ALTER TABLE `wordpress_installs` DROP INDEX `uniq_wpinstalls_domain_subdir`;
