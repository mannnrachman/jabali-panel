-- M30.1 schedule <-> destination M:N join (ADR-0078).
-- A schedule with zero rows = local-only. A schedule with N rows fans
-- out to N copy_jobs after the local backup succeeds.

CREATE TABLE backup_schedule_destinations (
  schedule_id    CHAR(26)    NOT NULL,
  destination_id CHAR(26)    NOT NULL,
  created_at     DATETIME(6) NOT NULL,
  PRIMARY KEY (schedule_id, destination_id),
  CONSTRAINT fk_bsd_schedule    FOREIGN KEY (schedule_id)
    REFERENCES backup_schedules(id) ON DELETE CASCADE,
  CONSTRAINT fk_bsd_destination FOREIGN KEY (destination_id)
    REFERENCES backup_destinations(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
