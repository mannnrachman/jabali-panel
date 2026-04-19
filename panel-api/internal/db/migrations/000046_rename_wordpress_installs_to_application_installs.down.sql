-- Down for M19 Step 1. SAFETY: refuses to run if any row has a non-WP
-- app_type, since the old single-app schema can't represent them. After
-- M19 Step 6 (DokuWiki) ships and a single non-WP install exists in
-- production, this down migration is one-way blocked by design — fix
-- forward, do not roll back.
--
-- The SIGNAL() inside the BEGIN/END is the standard MariaDB pattern for
-- aborting a migration with a meaningful message; golang-migrate captures
-- the error text and refuses to advance schema_migrations.

DELIMITER $$
CREATE PROCEDURE _m19_assert_only_wordpress_rows()
BEGIN
  DECLARE n INT;
  SELECT COUNT(*) INTO n FROM `application_installs` WHERE `app_type` <> 'wordpress';
  IF n > 0 THEN
    SIGNAL SQLSTATE '45000'
      SET MESSAGE_TEXT = 'Refusing to roll back 000046: non-wordpress rows exist in application_installs. Drop them first or fix forward.';
  END IF;
END$$
DELIMITER ;

CALL _m19_assert_only_wordpress_rows();
DROP PROCEDURE _m19_assert_only_wordpress_rows;

-- Reverse the index changes in FK-safe order: re-add the single-column
-- unique on `domain_id` first so fk_wpinstalls_domain stays satisfied,
-- then drop the composite.
ALTER TABLE `application_installs`
  ADD UNIQUE KEY `uniq_wpinstalls_domain_subdir`
    (`domain_id`, `subdirectory`);

ALTER TABLE `application_installs`
  DROP INDEX `uniq_app_installs_domain_subdir_apptype`;

ALTER TABLE `application_installs`
  DROP COLUMN `app_type`;

RENAME TABLE `application_installs` TO `wordpress_installs`;
