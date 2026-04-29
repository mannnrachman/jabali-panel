-- M30.1 follow-up: concurrent-backup limit. The in-process scheduler
-- now enqueues backup_jobs as status=queued and a dispatcher drains
-- them up to this limit. Default 2 — high enough to keep a fast disk
-- busy, low enough that a 50-user fan-out doesn't melt restic.

ALTER TABLE server_settings
    ADD COLUMN backup_max_concurrent_jobs INT UNSIGNED NOT NULL DEFAULT 2;
