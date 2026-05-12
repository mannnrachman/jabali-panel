-- ADR-0095 decision 3: bulk-WHM via job-per-account batches.
-- batch_id is a shared ULID across N migration_jobs created in one
-- bulk-create call. Existing single-account rows leave it NULL.
ALTER TABLE migration_jobs
  ADD COLUMN batch_id VARCHAR(26) NULL DEFAULT NULL AFTER id,
  ADD INDEX idx_migration_jobs_batch_id (batch_id);
