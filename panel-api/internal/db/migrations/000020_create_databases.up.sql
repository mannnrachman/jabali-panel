CREATE TABLE databases (
  id CHAR(26) NOT NULL PRIMARY KEY,
  user_id CHAR(26) NOT NULL,
  name VARCHAR(64) NOT NULL,
  engine ENUM('mariadb','postgres') NOT NULL DEFAULT 'mariadb',
  charset VARCHAR(32) NOT NULL DEFAULT 'utf8mb4',
  collation VARCHAR(32) NOT NULL DEFAULT 'utf8mb4_unicode_ci',
  created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,

  FOREIGN KEY fk_databases_user_id (user_id) REFERENCES users(id) ON DELETE RESTRICT,
  UNIQUE KEY uniq_user_name (user_id, name),
  INDEX idx_user_id (user_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
