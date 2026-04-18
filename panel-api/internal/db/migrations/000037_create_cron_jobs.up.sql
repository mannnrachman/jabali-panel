CREATE TABLE cron_jobs (
  id              CHAR(26)      NOT NULL PRIMARY KEY,
  user_id         CHAR(26)      NOT NULL,
  name            VARCHAR(100)  NOT NULL,
  command         VARCHAR(1024) NOT NULL,
  schedule        VARCHAR(100)  NOT NULL,
  enabled         TINYINT(1)    NOT NULL DEFAULT 1,
  last_run_at     TIMESTAMP     NULL,
  last_exit_code  INT           NULL,
  last_error      VARCHAR(1024) NULL,
  created_at      TIMESTAMP     NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at      TIMESTAMP     NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  KEY idx_cron_jobs_user_id (user_id),
  CONSTRAINT fk_cron_jobs_user_id FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
