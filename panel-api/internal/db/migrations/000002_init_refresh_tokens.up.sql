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

    CONSTRAINT fk_refresh_tokens_user FOREIGN KEY (user_id)
        REFERENCES users (id)
        ON DELETE CASCADE
        ON UPDATE RESTRICT
)
ENGINE = InnoDB
DEFAULT CHARSET = utf8mb4
COLLATE = utf8mb4_unicode_ci;
