-- M30.1 follow-up: backup_runs grouping. Each scheduler tick mints
-- a single run_id ULID and stamps every fan-out backup_jobs row with
-- it. Manual creates leave run_id NULL so they keep rendering as
-- standalone rows in the admin UI.

ALTER TABLE backup_jobs
    ADD COLUMN run_id CHAR(26) NULL AFTER schedule_id,
    ADD INDEX idx_backup_jobs_run_id (run_id);
