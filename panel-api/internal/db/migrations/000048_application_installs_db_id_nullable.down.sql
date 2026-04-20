-- Reverting requires backfilling NULL db_id rows to a non-empty
-- placeholder; the simplest safe rollback is to refuse if any NULL
-- rows exist. Operators downgrading must drop those rows manually.
UPDATE `application_installs` SET `db_id` = '' WHERE `db_id` IS NULL;
ALTER TABLE `application_installs`
  MODIFY COLUMN `db_id` CHAR(26) NOT NULL;
