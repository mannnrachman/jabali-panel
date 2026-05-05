CREATE TABLE log_access_streams (
  id CHAR(26) NOT NULL PRIMARY KEY,
  user_id CHAR(26) NOT NULL,
  domain_id CHAR(26) NULL,
  log_type ENUM('access', 'error', 'goaccess') NOT NULL,
  stream_key CHAR(32) NOT NULL,
  expires_at TIMESTAMP NOT NULL,
  created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,

  FOREIGN KEY fk_log_access_streams_user_id (user_id) REFERENCES users(id) ON DELETE CASCADE,
  FOREIGN KEY fk_log_access_streams_domain_id (domain_id) REFERENCES domains(id) ON DELETE CASCADE,
  UNIQUE KEY uniq_stream_key (stream_key),
  INDEX idx_user_id (user_id),
  INDEX idx_domain_id (domain_id),
  INDEX idx_expires_at (expires_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;