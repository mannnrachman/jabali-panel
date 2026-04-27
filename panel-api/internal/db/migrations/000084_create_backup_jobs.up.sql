-- M30 backup-restore foundation (ADR-0075).
-- backup_jobs is the audit trail for every backup/restore run. The actual
-- bundle bytes live in restic; this table holds workflow metadata so the
-- UI can list jobs, surface progress, and link snapshots back to a user.
-- Schema only -- no seed rows.

CREATE TABLE backup_jobs (
  id               CHAR(26) NOT NULL PRIMARY KEY,
  user_id          CHAR(26) NOT NULL,
  kind             ENUM('account_backup','account_restore','system_backup','system_restore') NOT NULL,
  status           ENUM('queued','running','succeeded','partial','failed','cancelled') NOT NULL DEFAULT 'queued',
  systemd_unit     VARCHAR(128) NOT NULL,
  snapshot_id      CHAR(64) NOT NULL DEFAULT '',
  parent_snapshot  CHAR(64) NOT NULL DEFAULT '',
  bytes_added      BIGINT UNSIGNED NOT NULL DEFAULT 0,
  bytes_total      BIGINT UNSIGNED NOT NULL DEFAULT 0,
  manifest_json    JSON DEFAULT NULL,
  warnings_json    JSON DEFAULT NULL,
  error_text       TEXT DEFAULT NULL,
  source_hostname  VARCHAR(253) NOT NULL DEFAULT '',
  source_panel_sha CHAR(40) NOT NULL DEFAULT '',
  created_at       DATETIME(6) NOT NULL,
  started_at       DATETIME(6) DEFAULT NULL,
  finished_at      DATETIME(6) DEFAULT NULL,
  KEY idx_backup_jobs_user_created (user_id, created_at DESC),
  KEY idx_backup_jobs_status (status),
  KEY idx_backup_jobs_snapshot (snapshot_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
