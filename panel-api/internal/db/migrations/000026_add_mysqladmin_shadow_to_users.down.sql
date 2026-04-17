ALTER TABLE users
  DROP COLUMN mysqladmin_provisioned_at,
  DROP COLUMN mysqladmin_password_enc,
  DROP COLUMN mysqladmin_username;
