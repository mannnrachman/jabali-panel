-- M46 follow-up (ADR-0099): admin all-DBs SSO stores a sentinel
-- DatabaseID ("__M46_ADMIN_ALL__") in the SSO token tables so the
-- validate handlers' early sentinel branch can recognise it. The
-- token tables had FOREIGN KEY (database_id) REFERENCES `databases`(id),
-- which rejects the sentinel (no such databases row) → admin-SSO mint
-- 500. Per-user safety is unaffected: the per-user validate path still
-- FindByID's the real database and 404s if it's gone, and SSO tokens
-- are single-use with a 5-min TTL (the ON DELETE CASCADE the FK gave
-- was marginal — a deleted DB's token expires/120s-consumes anyway).
--
-- FK names: phpmyadmin_sso_tokens uses the explicit `fk_sso_db`;
-- adminer_sso_tokens' FK is server-auto-named. Resolve both from
-- information_schema so this is portable across fresh installs.

SET @fk_pma := (SELECT CONSTRAINT_NAME FROM information_schema.KEY_COLUMN_USAGE
  WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'phpmyadmin_sso_tokens'
    AND COLUMN_NAME = 'database_id' AND REFERENCED_TABLE_NAME IS NOT NULL LIMIT 1);
SET @sql_pma := IF(@fk_pma IS NULL, 'SELECT 1',
  CONCAT('ALTER TABLE phpmyadmin_sso_tokens DROP FOREIGN KEY ', @fk_pma));
PREPARE p_pma FROM @sql_pma; EXECUTE p_pma; DEALLOCATE PREPARE p_pma;

SET @fk_adm := (SELECT CONSTRAINT_NAME FROM information_schema.KEY_COLUMN_USAGE
  WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'adminer_sso_tokens'
    AND COLUMN_NAME = 'database_id' AND REFERENCED_TABLE_NAME IS NOT NULL LIMIT 1);
SET @sql_adm := IF(@fk_adm IS NULL, 'SELECT 1',
  CONCAT('ALTER TABLE adminer_sso_tokens DROP FOREIGN KEY ', @fk_adm));
PREPARE p_adm FROM @sql_adm; EXECUTE p_adm; DEALLOCATE PREPARE p_adm;
