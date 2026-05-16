-- M45: root web terminal (ADR-0096).
-- Off-by-default gate on server_settings + one-shot session-token table
-- (mirrors log_access_streams; token single-use, IP+admin bound).

ALTER TABLE server_settings
  ADD COLUMN root_terminal_enabled TINYINT(1) NOT NULL DEFAULT 0;

CREATE TABLE terminal_sessions (
  id          CHAR(26)     NOT NULL PRIMARY KEY,
  user_id     CHAR(26)     NOT NULL,
  token       CHAR(43)     NOT NULL,            -- 256-bit base64url, one-shot
  client_ip   VARCHAR(45)  NOT NULL,            -- bound at mint, re-checked on WS
  expires_at  TIMESTAMP    NOT NULL,            -- connect deadline (~60s)
  used_at     DATETIME(6)  NULL,                -- set on first WS upgrade (single-use)
  started_at  DATETIME(6)  NULL,                -- PTY spawned
  ended_at    DATETIME(6)  NULL,                -- PTY closed
  cast_path   VARCHAR(255) NULL,                -- /var/log/jabali/terminal/<id>.cast
  created_at  DATETIME(6)  NOT NULL DEFAULT CURRENT_TIMESTAMP(6),

  FOREIGN KEY fk_terminal_sessions_user_id (user_id) REFERENCES users(id) ON DELETE CASCADE,
  UNIQUE KEY uniq_terminal_token (token),
  INDEX idx_terminal_user_id (user_id),
  INDEX idx_terminal_expires_at (expires_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
