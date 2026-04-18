CREATE TABLE filebrowser_sso_tokens (
  id CHAR(26) NOT NULL PRIMARY KEY,
  user_id CHAR(26) NOT NULL,
  token_hash CHAR(64) NOT NULL,
  created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
  expires_at DATETIME(6) NOT NULL,
  used_at DATETIME(6) NULL,
  UNIQUE KEY uniq_token_hash (token_hash),
  INDEX idx_expires_at (expires_at),
  INDEX idx_user_id (user_id),
  FOREIGN KEY fk_filebrowser_sso_user (user_id) REFERENCES users(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
