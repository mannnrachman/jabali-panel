CREATE TABLE php_pool_ini_overrides (
  id CHAR(26) NOT NULL PRIMARY KEY,
  pool_id CHAR(26) NOT NULL,
  directive VARCHAR(64) NOT NULL,
  value VARCHAR(255) NOT NULL,
  kind ENUM('value','flag') NOT NULL DEFAULT 'value',
  created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
  updated_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6),
  UNIQUE KEY uniq_pool_directive (pool_id, directive),
  FOREIGN KEY fk_override_pool (pool_id) REFERENCES php_pools(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
