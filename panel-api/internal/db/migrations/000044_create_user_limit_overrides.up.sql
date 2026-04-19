CREATE TABLE user_limit_overrides (
  user_id           CHAR(26)      NOT NULL PRIMARY KEY,
  disk_quota_mb     INT UNSIGNED  NULL,
  cpu_quota_percent INT UNSIGNED  NULL,
  memory_limit_mb   INT UNSIGNED  NULL,
  io_read_mbps      INT UNSIGNED  NULL,
  io_write_mbps     INT UNSIGNED  NULL,
  max_tasks         INT UNSIGNED  NULL,
  updated_at        TIMESTAMP     NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  CONSTRAINT fk_user_limit_overrides_user_id
    FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
