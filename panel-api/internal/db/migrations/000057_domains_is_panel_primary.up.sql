-- M6.4 / ADR-0048: is_panel_primary marks the single auto-registered
-- panel-hostname domain row. "At most one" is enforced in the Go
-- repository layer (MarkPanelPrimary transaction), NOT via a UNIQUE
-- KEY -- MariaDB's UNIQUE on TINYINT with DEFAULT 0 treats every 0 as
-- a distinct value that still participates in uniqueness checks, and
-- partial indexes (UNIQUE ... WHERE is_panel_primary=1) aren't
-- supported on MariaDB 11.x. Repo-layer enforcement is simpler and
-- keeps the schema uniform.
--
-- The row is delete-protected at the repo AND API layer. See ADR-0048.

ALTER TABLE domains
  ADD COLUMN is_panel_primary TINYINT(1) NOT NULL DEFAULT 0,
  ADD INDEX ix_domains_panel_primary (is_panel_primary);
