-- Reverse of the drop. Columns/tables are recreated empty; the legacy auth
-- code that populated them is gone, so a real down-migration would still
-- leave the panel unable to authenticate anyone. Kept for migrate(1) symmetry.

CREATE TABLE refresh_tokens (
    id             CHAR(26)        NOT NULL,
    user_id        CHAR(26)        NOT NULL,
    device_id      VARCHAR(255)    NOT NULL,
    token_hash     CHAR(64)        NOT NULL,
    expires_at     DATETIME(6)     NOT NULL,
    revoked_at     DATETIME(6)     NULL,
    last_used_at   DATETIME(6)     NULL,
    created_at     DATETIME(6)     NOT NULL,
    PRIMARY KEY (id),
    UNIQUE KEY ux_refresh_tokens_hash (token_hash),
    KEY ix_refresh_tokens_user (user_id),
    KEY ix_refresh_tokens_expires (expires_at),
    CONSTRAINT fk_refresh_tokens_user
        FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE totp_backup_codes (
    id         CHAR(26)    NOT NULL,
    user_id    CHAR(26)    NOT NULL,
    code_hash  VARCHAR(72) NOT NULL,
    used_at    DATETIME(6) NULL,
    created_at DATETIME(6) NOT NULL,
    PRIMARY KEY (id),
    INDEX idx_totp_backup_user (user_id),
    CONSTRAINT fk_totp_backup_user
        FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

ALTER TABLE users
    ADD COLUMN totp_secret_encrypted VARBINARY(256) NULL,
    ADD COLUMN totp_enabled          TINYINT(1)    NOT NULL DEFAULT 0,
    ADD COLUMN totp_enabled_at       DATETIME(6)   NULL;
