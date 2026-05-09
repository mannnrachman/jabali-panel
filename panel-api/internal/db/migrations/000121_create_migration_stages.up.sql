-- M35: per-stage state for resumability.
--
-- Each migration_jobs row spawns N migration_stages rows — one per
-- pipeline stage that runs (analyze, fix_perms, validate, restore,
-- plus per-domain / per-mailbox sub-stages emitted by the importers).
-- A failed mid-run leaves stages with state='failed' / 'pending'
-- which the resume path picks up: done stages no-op, pending re-run,
-- failed re-run after operator inspects last_error.
--
-- bytes_processed lets the UI render a progress bar without
-- recalculating from logs every poll.

CREATE TABLE migration_stages (
    id                CHAR(26)     NOT NULL PRIMARY KEY,
    job_id            CHAR(26)     NOT NULL,
    stage_name        VARCHAR(64)  NOT NULL,
    state             VARCHAR(16)  NOT NULL DEFAULT 'pending',
    started_at        DATETIME(6)  NULL,
    ended_at          DATETIME(6)  NULL,
    bytes_processed   BIGINT       NOT NULL DEFAULT 0,
    last_error        TEXT         NULL,
    created_at        DATETIME(6)  NOT NULL,
    updated_at        DATETIME(6)  NOT NULL,
    KEY idx_migration_stages_job (job_id, stage_name),
    CONSTRAINT fk_migration_stages_job
        FOREIGN KEY (job_id) REFERENCES migration_jobs (id)
        ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
