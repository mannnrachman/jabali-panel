-- M28 Branding + Page Templates.
--
-- Adds panel-branding fields to server_settings (brand text + logo
-- paths) and a new page_templates table holding operator-editable
-- stock content (default index.html on new domains, error pages).
-- Schema only — template default bodies are seeded by
-- PageTemplateRepository.EnsureDefaults from app first-boot per
-- feedback_migration_data_seed_ordering.

ALTER TABLE server_settings
  ADD COLUMN panel_brand_text VARCHAR(60) NOT NULL DEFAULT '',
  ADD COLUMN logo_light_path VARCHAR(255) NOT NULL DEFAULT '',
  ADD COLUMN logo_dark_path VARCHAR(255) NOT NULL DEFAULT '';

CREATE TABLE page_templates (
  `key`       VARCHAR(64)   NOT NULL PRIMARY KEY,
  content     LONGTEXT      NOT NULL,
  updated_at  DATETIME(6)   NOT NULL DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
