ALTER TABLE hosting_packages
  DROP COLUMN max_tasks,
  DROP COLUMN io_write_mbps,
  DROP COLUMN io_read_mbps,
  DROP COLUMN memory_limit_mb,
  DROP COLUMN cpu_quota_percent;
