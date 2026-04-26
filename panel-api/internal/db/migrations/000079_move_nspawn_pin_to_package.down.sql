ALTER TABLE users
  ADD COLUMN nspawn_image_version VARCHAR(64) NULL;

ALTER TABLE hosting_packages
  DROP COLUMN nspawn_image_version;
