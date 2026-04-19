DROP TABLE IF EXISTS totp_backup_codes;

ALTER TABLE users
    DROP COLUMN totp_enabled_at,
    DROP COLUMN totp_enabled,
    DROP COLUMN totp_secret_encrypted;
