CREATE TABLE backup_copy_jobs (
  id              CHAR(26)    NOT NULL PRIMARY KEY,
  backup_job_id   CHAR(26)    NOT NULL,
  destination_id  CHAR(26)    NOT NULL,
  status          ENUM('queued','running','succeeded','failed','cancelled') NOT NULL DEFAULT 'queued',
  systemd_unit    VARCHAR(128) NOT NULL DEFAULT '',
  retry_count     INT         NOT NULL DEFAULT 0,
  next_attempt_at DATETIME(6) NULL,
  started_at      DATETIME(6) NULL,
  finished_at     DATETIME(6) NULL,
  bytes_copied    BIGINT UNSIGNED NULL,
  error_text      TEXT        NULL,
  created_at      DATETIME(6) NOT NULL,
  updated_at      DATETIME(6) NOT NULL,
  KEY idx_bcj_queue (status, next_attempt_at),
  KEY idx_bcj_backup (backup_job_id),
  CONSTRAINT fk_bcj_backup FOREIGN KEY (backup_job_id)
    REFERENCES backup_jobs(id) ON DELETE CASCADE,
  CONSTRAINT fk_bcj_dest   FOREIGN KEY (destination_id)
    REFERENCES backup_destinations(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
