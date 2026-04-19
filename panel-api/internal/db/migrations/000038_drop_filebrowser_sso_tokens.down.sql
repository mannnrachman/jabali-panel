-- Rolling back this migration recreates the table with the same schema as
-- 000034. Useful only if rolling M11 decommission back for some reason.
CREATE TABLE IF NOT EXISTS filebrowser_sso_tokens (
    id         CHAR(26) PRIMARY KEY,
    user_id    CHAR(26) NOT NULL,
    token_hash CHAR(64) NOT NULL UNIQUE,
    expires_at TIMESTAMP NOT NULL,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    used_at    TIMESTAMP NULL,
    INDEX idx_filebrowser_sso_tokens_user_id (user_id),
    INDEX idx_filebrowser_sso_tokens_expires_at (expires_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
