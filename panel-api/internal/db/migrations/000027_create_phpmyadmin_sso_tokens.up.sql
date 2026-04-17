CREATE TABLE phpmyadmin_sso_tokens (
  id CHAR(26) NOT NULL PRIMARY KEY,
  user_id CHAR(26) NOT NULL,
  database_id CHAR(26) NOT NULL,
  token_hash CHAR(64) NOT NULL,
  expires_at DATETIME(6) NOT NULL,
  created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
  UNIQUE KEY uniq_token_hash (token_hash),
  INDEX idx_expires_at (expires_at),
  INDEX idx_user_id (user_id),
  FOREIGN KEY fk_sso_user (user_id) REFERENCES users(id) ON DELETE CASCADE,
  FOREIGN KEY fk_sso_db (database_id) REFERENCES `databases`(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
