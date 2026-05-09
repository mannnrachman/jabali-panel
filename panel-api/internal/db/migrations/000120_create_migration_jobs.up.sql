-- M35: migration importers — job header.
--
-- One row per migration attempt for one (source-host, source-user,
-- source-kind) tuple. The same source user retried after a failure
-- reuses the row (resume) — UNIQUE on the natural key prevents two
-- parallel attempts from racing on the same files.
--
-- target_user_id is NULL until the restore stage actually creates
-- the destination user. manifest_json is written when restore
-- succeeds (caller dumps the post-flight AccountManifest).
--
-- States mirror the four-pipeline pattern: analyze → fix-perms →
-- validate → restore, plus terminal done/failed/cancelled and
-- entry pending. See internal/migrate/stage.go (Step 2) for the
-- transition rules.

CREATE TABLE migration_jobs (
    id                CHAR(26)     NOT NULL PRIMARY KEY,
    source_kind       VARCHAR(32)  NOT NULL,
    source_host       VARCHAR(255) NOT NULL,
    source_user       VARCHAR(64)  NOT NULL,
    target_user_id    CHAR(26)     NULL,
    state             VARCHAR(32)  NOT NULL DEFAULT 'pending',
    started_at        DATETIME(6)  NOT NULL,
    ended_at          DATETIME(6)  NULL,
    manifest_json     LONGTEXT     NULL,
    last_error        TEXT         NULL,
    created_at        DATETIME(6)  NOT NULL,
    updated_at        DATETIME(6)  NOT NULL,
    UNIQUE KEY uq_migration_source (source_host, source_user, source_kind),
    KEY idx_migration_state (state),
    KEY idx_migration_target_user (target_user_id),
    CONSTRAINT fk_migration_jobs_target_user
        FOREIGN KEY (target_user_id) REFERENCES users (id)
        ON DELETE SET NULL
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
