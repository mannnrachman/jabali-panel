-- M26 Step 3 (ADR-0055). Per-domain ModSecurity toggle + global engine /
-- paranoia level. Schema-only — defaults are written by ALTER TABLE so
-- existing rows backfill at migrate time. No EnsureDefault wiring per
-- feedback_migration_data_seed_ordering — defaults live in the column.

ALTER TABLE domains
  ADD COLUMN modsec_enabled TINYINT(1) NOT NULL DEFAULT 0 AFTER email_enabled_at;

ALTER TABLE server_settings
  ADD COLUMN modsec_global_enabled TINYINT(1) NOT NULL DEFAULT 0,
  ADD COLUMN modsec_paranoia_level TINYINT UNSIGNED NOT NULL DEFAULT 1;
