ALTER TABLE migration_jobs
  DROP INDEX idx_migration_jobs_batch_id,
  DROP COLUMN batch_id;
