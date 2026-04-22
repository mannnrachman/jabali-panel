ALTER TABLE domains
  DROP INDEX ix_domains_panel_primary,
  DROP COLUMN is_panel_primary;
