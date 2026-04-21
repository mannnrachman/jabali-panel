CREATE TABLE magic_link_tokens (
  id                     CHAR(26)    NOT NULL,
  application_install_id CHAR(26)    NOT NULL,
  panel_user_id          CHAR(26)    NOT NULL,
  token_hash             CHAR(64)    NOT NULL,
  expires_at             DATETIME(6) NOT NULL,
  used_at                DATETIME(6) NULL,
  created_at             DATETIME(6) NOT NULL,
  PRIMARY KEY (id),
  UNIQUE KEY uniq_magic_link_tokens_token_hash (token_hash),
  KEY idx_magic_link_tokens_expires (expires_at),
  KEY idx_magic_link_tokens_install (application_install_id),
  CONSTRAINT fk_magic_link_tokens_install
    FOREIGN KEY (application_install_id)
    REFERENCES application_installs(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
