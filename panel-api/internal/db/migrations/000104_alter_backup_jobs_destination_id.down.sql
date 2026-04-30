ALTER TABLE backup_jobs
  DROP INDEX idx_backup_jobs_destination_id,
  DROP COLUMN destination_id;
