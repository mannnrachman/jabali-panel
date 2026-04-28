-- M30.1 follow-up — opt-in system_backup on account schedules.
-- When include_system_backup=1 on a kind=account_backup schedule, the
-- scheduler tick fires the per-user fan-out AND a system.backup job
-- in the same dispatch. The flag is ignored on kind=system_backup
-- rows (they already back up the system by definition).

ALTER TABLE backup_schedules
  ADD COLUMN include_system_backup TINYINT(1) NOT NULL DEFAULT 0
  AFTER user_id;
