-- 2FA / TOTP (plan: plans/2fa-totp.md). Additive + nullable so the column
-- exists on every user row but 2FA is opt-in per-account.
ALTER TABLE users
    ADD COLUMN totp_secret_encrypted VARBINARY(256) NULL,
    ADD COLUMN totp_enabled          TINYINT(1)    NOT NULL DEFAULT 0,
    ADD COLUMN totp_enabled_at       DATETIME(6)   NULL;

-- Backup codes: 10 per user, shown once at enrolment, single-use.
-- code_hash is bcrypt so a DB read never exposes the raw code.
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
