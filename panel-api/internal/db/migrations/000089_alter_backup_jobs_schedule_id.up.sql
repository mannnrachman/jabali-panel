-- M30.1 link backup_jobs back to the schedule that fired them
-- (ADR-0078). NULL = manual/admin-triggered backup. Indexed for the
-- "show me jobs from schedule X" UI drilldown.

ALTER TABLE backup_jobs
  ADD COLUMN schedule_id CHAR(26) NULL AFTER user_id,
  ADD KEY idx_backup_jobs_schedule (schedule_id);
