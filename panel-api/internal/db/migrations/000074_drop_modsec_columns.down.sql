-- Re-add ModSecurity columns. Restores schema state of migration 000066
-- (used only on rollback; column data is gone).

ALTER TABLE domains
  ADD COLUMN modsec_enabled TINYINT(1) NOT NULL DEFAULT 0 AFTER email_enabled_at;

ALTER TABLE server_settings
  ADD COLUMN modsec_global_enabled TINYINT(1) NOT NULL DEFAULT 0,
  ADD COLUMN modsec_paranoia_level TINYINT UNSIGNED NOT NULL DEFAULT 1;
