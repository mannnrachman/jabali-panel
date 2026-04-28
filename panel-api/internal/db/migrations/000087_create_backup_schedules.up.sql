-- M30.1 backup schedules (ADR-0078).
-- Admin-defined cron schedules. user_id NULL is reserved for
-- system_backup (server-wide). account_backup rows MUST have a user_id
-- (enforced in repository validation; FK enforces existence).
--
-- keep_daily/weekly/monthly NULL = inherit server_settings backup_keep_*
-- values; non-NULL = per-schedule override (rarely needed in v1, exposed
-- as advanced UI for future-proofing per-schedule retention).

CREATE TABLE backup_schedules (
  id            CHAR(26)     NOT NULL PRIMARY KEY,
  kind          ENUM('account_backup','system_backup') NOT NULL,
  user_id       CHAR(26)     NULL,
  cron_expr     VARCHAR(64)  NOT NULL,
  enabled       TINYINT(1)   NOT NULL DEFAULT 1,
  keep_daily    INT          NULL,
  keep_weekly   INT          NULL,
  keep_monthly  INT          NULL,
  last_run_at   DATETIME(6)  NULL,
  next_run_at   DATETIME(6)  NULL,
  created_at    DATETIME(6)  NOT NULL,
  updated_at    DATETIME(6)  NOT NULL,
  KEY idx_backup_sched_due (enabled, next_run_at),
  KEY idx_backup_sched_user (user_id, kind),
  CONSTRAINT fk_backup_sched_user FOREIGN KEY (user_id)
    REFERENCES users(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
