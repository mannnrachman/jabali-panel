-- M37 Phase 4: Adminer SSO bridge for both engines.
--
-- Mirrors phpmyadmin_sso_tokens (mig 000027) + adds engine column so a
-- single bridge serves MariaDB and PostgreSQL. Adminer is a single-PHP
-- multi-engine DB admin tool; SSO plugin reads token from URL,
-- validates over UDS, returns {driver, server, user, pass, db} —
-- engine drives which set of credentials we mint.

CREATE TABLE adminer_sso_tokens (
  id          CHAR(26)                      NOT NULL PRIMARY KEY,
  user_id     CHAR(26)                      NOT NULL,
  database_id CHAR(26)                      NOT NULL,
  engine      ENUM('mariadb', 'postgres')   NOT NULL,
  token_hash  CHAR(64)                      NOT NULL,
  expires_at  DATETIME(6)                   NOT NULL,
  created_at  DATETIME(6)                   NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
  UNIQUE KEY uniq_adminer_token_hash (token_hash),
  INDEX idx_adminer_expires_at (expires_at),
  INDEX idx_adminer_user_id (user_id),
  INDEX idx_adminer_engine (engine),
  CONSTRAINT fk_adminer_sso_user
    FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE,
  CONSTRAINT fk_adminer_sso_db
    FOREIGN KEY (database_id) REFERENCES `databases`(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- Postgres shadow role columns on users — parallels mysqladmin_*.
-- Adminer SSO mints a PG-shadow ROLE with CREATEDB on the user's
-- owned databases; password is encrypted with the same SSO key.
ALTER TABLE users
  ADD COLUMN pgadmin_username        VARCHAR(64)    NULL AFTER mysqladmin_provisioned_at,
  ADD COLUMN pgadmin_password_enc    VARBINARY(512) NULL AFTER pgadmin_username,
  ADD COLUMN pgadmin_provisioned_at  DATETIME(6)    NULL AFTER pgadmin_password_enc;
