ALTER TABLE hosting_packages
  ADD COLUMN cpu_quota_percent INT UNSIGNED NOT NULL DEFAULT 0 AFTER disk_quota_mb,
  ADD COLUMN memory_limit_mb   INT UNSIGNED NOT NULL DEFAULT 0 AFTER cpu_quota_percent,
  ADD COLUMN io_read_mbps      INT UNSIGNED NOT NULL DEFAULT 0 AFTER memory_limit_mb,
  ADD COLUMN io_write_mbps     INT UNSIGNED NOT NULL DEFAULT 0 AFTER io_read_mbps,
  ADD COLUMN max_tasks         INT UNSIGNED NOT NULL DEFAULT 0 AFTER io_write_mbps;
