CREATE TABLE ssh_keys (
  id CHAR(26) NOT NULL PRIMARY KEY,
  user_id CHAR(26) NOT NULL,
  name VARCHAR(128) NOT NULL,
  public_key TEXT NOT NULL,
  fingerprint CHAR(64) NOT NULL,
  created_at DATETIME(6) NOT NULL,
  INDEX idx_ssh_keys_user_id (user_id),
  UNIQUE INDEX ux_ssh_keys_user_fingerprint (user_id, fingerprint),
  CONSTRAINT fk_ssh_keys_user FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE
);
