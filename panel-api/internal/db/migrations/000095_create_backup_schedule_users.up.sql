-- M30.1 follow-up — multi-user account schedules.
-- One backup_schedules row can target N users. Empty = all non-admin
-- users (the previous "user_id NULL = fan-out" semantic). Non-empty =
-- those specific users only. The legacy single user_id column on
-- backup_schedules stays for backwards compat with existing rows but
-- is no longer authoritative; readers must query this join.

CREATE TABLE backup_schedule_users (
  schedule_id CHAR(26)    NOT NULL,
  user_id     CHAR(26)    NOT NULL,
  created_at  DATETIME(6) NOT NULL,
  PRIMARY KEY (schedule_id, user_id),
  KEY idx_bsu_user (user_id),
  CONSTRAINT fk_bsu_schedule FOREIGN KEY (schedule_id)
    REFERENCES backup_schedules(id) ON DELETE CASCADE,
  CONSTRAINT fk_bsu_user FOREIGN KEY (user_id)
    REFERENCES users(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- Backfill: every existing schedule with a user_id becomes a 1-row
-- entry in the join. Schedules with user_id NULL stay as-is (will
-- continue to mean "all non-admin users" once the scheduler reads the
-- join).
INSERT INTO backup_schedule_users (schedule_id, user_id, created_at)
SELECT id, user_id, NOW(6)
FROM backup_schedules
WHERE user_id IS NOT NULL;
