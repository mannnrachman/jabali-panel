CREATE TABLE database_user_grants (
  id CHAR(26) NOT NULL PRIMARY KEY,
  database_id CHAR(26) NOT NULL,
  database_user_id CHAR(26) NOT NULL,
  grant_level ENUM('rw','ro') NOT NULL,
  created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,

  FOREIGN KEY fk_database_user_grants_database_id (database_id) REFERENCES `databases`(`id`) ON DELETE RESTRICT,
  FOREIGN KEY fk_database_user_grants_database_user_id (database_user_id) REFERENCES database_users(id) ON DELETE RESTRICT,
  UNIQUE KEY uniq_db_user (database_id, database_user_id),
  INDEX idx_database_id (database_id),
  INDEX idx_database_user_id (database_user_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
