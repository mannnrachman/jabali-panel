-- Revert the panel-wide default PHP version back to 8.5.
-- Only flips rows still on '8.4' so a deliberately-chosen version survives.
ALTER TABLE server_settings
  ALTER COLUMN default_php_version SET DEFAULT '8.5';

UPDATE server_settings
  SET default_php_version = '8.5'
  WHERE default_php_version = '8.4';
