-- M19 Step 1: generalise wordpress_installs into application_installs.
-- The table now hosts every kind of installable app (WordPress today,
-- DokuWiki, MediaWiki, Joomla, Drupal, … later), keyed by `app_type`.
-- WordPress is one row value among many; existing rows back-fill cleanly.
--
-- The composite uniqueness widens from (domain_id, subdirectory) to
-- (domain_id, subdirectory, app_type) so a single (domain, subdir) slot
-- can host multiple distinct apps (e.g. WordPress at /blog AND something
-- else at /shop on the same domain — and crucially, two different app
-- types can share an exact slot if a future product change ever wants
-- that. For now the panel UI keeps users from doing it).
--
-- FK-safe ordering (errno 1553 — the canonical case is in 000045): add
-- the new composite UNIQUE FIRST so fk_wpinstalls_domain still has an
-- index on `domain_id`, THEN drop the old unique. The FK constraint
-- itself is renamed below to match the table's new identity.

RENAME TABLE `wordpress_installs` TO `application_installs`;

ALTER TABLE `application_installs`
  ADD COLUMN `app_type` VARCHAR(32) NOT NULL DEFAULT 'wordpress' AFTER `subdirectory`;

ALTER TABLE `application_installs`
  ADD UNIQUE KEY `uniq_app_installs_domain_subdir_apptype`
    (`domain_id`, `subdirectory`, `app_type`);

ALTER TABLE `application_installs`
  DROP INDEX `uniq_wpinstalls_domain_subdir`;
