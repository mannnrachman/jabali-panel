-- Reversal is guarded: only drop the column if it exists, so re-running
-- the down migration (or running it on a schema that never got 000148)
-- doesn't error. The column is additive and PHP-default, so dropping it
-- returns every domain to implicit PHP-FPM behaviour.
SET @col_exists := (
  SELECT COUNT(*) FROM information_schema.COLUMNS
  WHERE TABLE_SCHEMA = DATABASE()
    AND TABLE_NAME = 'domains'
    AND COLUMN_NAME = 'runtime_type'
);
SET @ddl := IF(@col_exists > 0,
  'ALTER TABLE domains DROP COLUMN runtime_type',
  'SELECT 1');
PREPARE stmt FROM @ddl;
EXECUTE stmt;
DEALLOCATE PREPARE stmt;
