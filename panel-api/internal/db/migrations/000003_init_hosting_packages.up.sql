CREATE TABLE hosting_packages (
    id                   CHAR(26)        NOT NULL,
    name                 VARCHAR(100)    NOT NULL,
    disk_quota_mb        INT UNSIGNED    NOT NULL DEFAULT 0,
    bandwidth_quota_mb   INT UNSIGNED    NOT NULL DEFAULT 0,
    max_domains          INT UNSIGNED    NOT NULL DEFAULT 0,
    max_email_accounts   INT UNSIGNED    NOT NULL DEFAULT 0,
    max_databases        INT UNSIGNED    NOT NULL DEFAULT 0,
    max_ftp_accounts     INT UNSIGNED    NOT NULL DEFAULT 0,
    ssh_enabled          TINYINT(1)      NOT NULL DEFAULT 0,
    cgi_enabled          TINYINT(1)      NOT NULL DEFAULT 0,
    created_at           DATETIME(6)     NOT NULL,
    updated_at           DATETIME(6)     NOT NULL,
    deleted_at           DATETIME(6)     NULL,

    PRIMARY KEY (id),
    UNIQUE KEY ux_packages_name (name),
    KEY ix_packages_deleted_at (deleted_at)
)
ENGINE = InnoDB
DEFAULT CHARSET = utf8mb4
COLLATE = utf8mb4_unicode_ci;
