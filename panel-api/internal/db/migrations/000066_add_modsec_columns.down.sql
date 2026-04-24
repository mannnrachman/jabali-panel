ALTER TABLE server_settings
  DROP COLUMN modsec_paranoia_level,
  DROP COLUMN modsec_global_enabled;

ALTER TABLE domains
  DROP COLUMN modsec_enabled;
