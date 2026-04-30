-- M30.2 Option B (ADR-0080): each backup writes directly to ONE destination.
-- Legacy rows (pre-M30.2) carry NULL = the implicit local repo.
ALTER TABLE backup_jobs
  ADD COLUMN destination_id CHAR(26) NULL AFTER user_id,
  ADD INDEX idx_backup_jobs_destination_id (destination_id);
