-- Drop every legacy-auth artifact left over from the M20 Kratos cutover.
-- Custom JWT + refresh-token + TOTP secrets now live in Kratos; the panel DB
-- stops being a credential store. Fresh VM, no data to preserve.

DROP TABLE IF EXISTS refresh_tokens;
DROP TABLE IF EXISTS totp_backup_codes;

ALTER TABLE users
    DROP COLUMN totp_secret_encrypted,
    DROP COLUMN totp_enabled,
    DROP COLUMN totp_enabled_at;
