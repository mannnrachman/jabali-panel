-- Per-event-kind enable/disable toggle for the M14 notification
-- dispatcher. Schema only; rows are seeded by
-- NotificationEventSettingRepository.EnsureDefaults from the app
-- first-boot path (per feedback_migration_data_seed_ordering).
CREATE TABLE notification_event_settings (
  event_kind  VARCHAR(60)   NOT NULL PRIMARY KEY,
  enabled     TINYINT(1)    NOT NULL DEFAULT 0,
  updated_at  DATETIME(6)   NOT NULL DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
