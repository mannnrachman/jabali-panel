ALTER TABLE malware_settings
  DROP COLUMN mail_scan_per_tick_budget,
  DROP COLUMN mail_scan_timeout_sec,
  DROP COLUMN mail_scan_max_attachment_mb,
  DROP COLUMN mail_scan_skip_addresses,
  DROP COLUMN mail_scan_all_folders,
  DROP COLUMN mail_scan_enabled;

DROP TABLE IF EXISTS mail_scan_failures;
DROP TABLE IF EXISTS mail_scan_state;
