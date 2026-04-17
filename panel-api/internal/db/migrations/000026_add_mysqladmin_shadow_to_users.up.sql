ALTER TABLE users
  ADD COLUMN mysqladmin_username VARCHAR(64) NULL,
  ADD COLUMN mysqladmin_password_enc VARBINARY(512) NULL,
  ADD COLUMN mysqladmin_provisioned_at DATETIME(6) NULL;
