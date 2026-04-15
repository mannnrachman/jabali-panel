CREATE TABLE users (
    id             CHAR(26)        NOT NULL,
    email          VARCHAR(255)    NOT NULL,
    name_first     VARCHAR(100)    NOT NULL DEFAULT '',
    name_last      VARCHAR(100)    NOT NULL DEFAULT '',
    password_hash  VARCHAR(255)    NOT NULL,
    is_admin       TINYINT(1)      NOT NULL DEFAULT 0,
    linux_uid      INT UNSIGNED    NULL,
    created_at     DATETIME(6)     NOT NULL,
    updated_at     DATETIME(6)     NOT NULL,
    deleted_at     DATETIME(6)     NULL,

    PRIMARY KEY (id),
    UNIQUE KEY ux_users_email (email),
    KEY ix_users_deleted_at (deleted_at)
)
ENGINE = InnoDB
DEFAULT CHARSET = utf8mb4
COLLATE = utf8mb4_unicode_ci;
