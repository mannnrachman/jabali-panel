CREATE TABLE domains (
    id                       CHAR(26)        NOT NULL,
    user_id                  CHAR(26)        NOT NULL,
    name                     VARCHAR(255)    NOT NULL,
    doc_root                 VARCHAR(512)    NOT NULL DEFAULT '',
    is_enabled               TINYINT(1)      NOT NULL DEFAULT 1,
    nginx_custom_directives  TEXT            NULL,
    created_at               DATETIME(6)     NOT NULL,
    updated_at               DATETIME(6)     NOT NULL,
    deleted_at               DATETIME(6)     NULL,

    PRIMARY KEY (id),
    UNIQUE KEY ux_domains_name (name),
    KEY ix_domains_user_id (user_id),
    KEY ix_domains_deleted_at (deleted_at)
)
ENGINE = InnoDB
DEFAULT CHARSET = utf8mb4
COLLATE = utf8mb4_unicode_ci;
