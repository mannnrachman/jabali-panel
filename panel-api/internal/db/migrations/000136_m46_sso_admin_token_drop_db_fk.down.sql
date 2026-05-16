-- Re-add the database_id → `databases`(id) FKs (best-effort; skipped
-- if one already exists). Rollback implies M46 admin-SSO is reverted,
-- so no sentinel rows should remain; any stale token is <=5-min TTL.

SET @has_pma := (SELECT COUNT(*) FROM information_schema.KEY_COLUMN_USAGE
  WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'phpmyadmin_sso_tokens'
    AND COLUMN_NAME = 'database_id' AND REFERENCED_TABLE_NAME IS NOT NULL);
SET @sql_pma := IF(@has_pma > 0, 'SELECT 1',
  'ALTER TABLE phpmyadmin_sso_tokens ADD CONSTRAINT fk_sso_db FOREIGN KEY (database_id) REFERENCES `databases`(id) ON DELETE CASCADE');
PREPARE p_pma FROM @sql_pma; EXECUTE p_pma; DEALLOCATE PREPARE p_pma;

SET @has_adm := (SELECT COUNT(*) FROM information_schema.KEY_COLUMN_USAGE
  WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'adminer_sso_tokens'
    AND COLUMN_NAME = 'database_id' AND REFERENCED_TABLE_NAME IS NOT NULL);
SET @sql_adm := IF(@has_adm > 0, 'SELECT 1',
  'ALTER TABLE adminer_sso_tokens ADD CONSTRAINT fk_adminer_sso_db FOREIGN KEY (database_id) REFERENCES `databases`(id) ON DELETE CASCADE');
PREPARE p_adm FROM @sql_adm; EXECUTE p_adm; DEALLOCATE PREPARE p_adm;
