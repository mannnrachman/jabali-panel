ALTER TABLE backup_jobs
    DROP INDEX idx_backup_jobs_run_id,
    DROP COLUMN run_id;
