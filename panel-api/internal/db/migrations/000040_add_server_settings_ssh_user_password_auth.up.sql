ALTER TABLE server_settings
ADD COLUMN ssh_user_password_auth TINYINT(1) NOT NULL DEFAULT 0;
