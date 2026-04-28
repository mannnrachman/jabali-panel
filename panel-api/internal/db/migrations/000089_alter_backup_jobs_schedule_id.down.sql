ALTER TABLE backup_jobs
  DROP KEY idx_backup_jobs_schedule,
  DROP COLUMN schedule_id;
