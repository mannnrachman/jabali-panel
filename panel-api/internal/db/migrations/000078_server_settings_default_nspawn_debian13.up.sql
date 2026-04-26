ALTER TABLE server_settings
  ALTER COLUMN default_nspawn_image_version SET DEFAULT 'debian-13-v1';

UPDATE server_settings
  SET default_nspawn_image_version = 'debian-13-v1'
  WHERE default_nspawn_image_version = 'debian-12-v1';
