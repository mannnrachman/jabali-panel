-- M30.2 Option B (ADR-0080): per-destination backup model means no source
-- repo to mirror from; backup_copy_jobs is dead.
DROP TABLE IF EXISTS backup_copy_jobs;
