-- Single-row table holding operator-editable server identity + DNS config.
-- Row id=1 is the only row; seeded from config.toml at first app boot.
CREATE TABLE server_settings (
    id            TINYINT UNSIGNED NOT NULL DEFAULT 1,
    hostname      VARCHAR(253)  NOT NULL DEFAULT '',
    public_ipv4   VARCHAR(45)   NOT NULL DEFAULT '',
    public_ipv6   VARCHAR(45)   NOT NULL DEFAULT '',
    ns1_name      VARCHAR(253)  NOT NULL DEFAULT '',
    ns1_ipv4      VARCHAR(45)   NOT NULL DEFAULT '',
    ns2_name      VARCHAR(253)  NOT NULL DEFAULT '',
    ns2_ipv4      VARCHAR(45)   NOT NULL DEFAULT '',
    admin_email   VARCHAR(320)  NOT NULL DEFAULT '',
    updated_at    DATETIME(6)   NOT NULL,
    CONSTRAINT pk_server_settings PRIMARY KEY (id),
    CONSTRAINT ck_server_settings_single_row CHECK (id = 1)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
