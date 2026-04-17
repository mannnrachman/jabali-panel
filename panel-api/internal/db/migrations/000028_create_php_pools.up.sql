CREATE TABLE php_pools (
  id CHAR(26) NOT NULL PRIMARY KEY,
  user_id CHAR(26) NOT NULL,
  php_version VARCHAR(8) NOT NULL,
  pm_mode VARCHAR(16) NOT NULL DEFAULT 'ondemand',
  pm_max_children INT UNSIGNED NOT NULL DEFAULT 20,
  process_idle_timeout_seconds INT UNSIGNED NOT NULL DEFAULT 60,
  status VARCHAR(16) NOT NULL DEFAULT 'pending',
  last_error TEXT NULL,
  created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
  updated_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6),
  UNIQUE KEY uniq_user_pool (user_id),
  FOREIGN KEY fk_pool_user (user_id) REFERENCES users(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
