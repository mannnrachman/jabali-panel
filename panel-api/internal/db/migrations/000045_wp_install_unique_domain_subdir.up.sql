-- Allow multiple WordPress installs per domain, scoped by subdirectory.
-- The original schema (000033) put a column-level UNIQUE on domain_id which
-- enforced a 1:1 domain↔install relationship — fine when WP could only live
-- at the docroot, but blocks the common pattern of running e.g. main site at
-- domain.com and a /blog WordPress at domain.com/blog. The unique constraint
-- now lives on (domain_id, subdirectory) instead so the same domain can host
-- as many installs as it has distinct subdirectories. Empty string is a
-- valid subdirectory value (means "install at docroot") and is treated as a
-- distinct value by the unique index — so you still can't have two installs
-- both at the docroot of the same domain.
-- Add the composite unique FIRST: fk_wpinstalls_domain references
-- `domain_id`, and MariaDB refuses to drop the only index supporting an
-- FK (errno 1553). The composite index has `domain_id` as its leading
-- column, so once it exists the FK has another index to use and the
-- original single-column unique can be dropped.
ALTER TABLE `wordpress_installs`
  ADD UNIQUE KEY `uniq_wpinstalls_domain_subdir` (`domain_id`, `subdirectory`);
ALTER TABLE `wordpress_installs` DROP INDEX `domain_id`;
