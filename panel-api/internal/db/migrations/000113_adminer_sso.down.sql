ALTER TABLE users
  DROP COLUMN pgadmin_provisioned_at,
  DROP COLUMN pgadmin_password_enc,
  DROP COLUMN pgadmin_username;

DROP TABLE adminer_sso_tokens;
