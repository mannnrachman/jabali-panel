DROP TABLE IF EXISTS page_templates;

ALTER TABLE server_settings
  DROP COLUMN panel_brand_text,
  DROP COLUMN logo_light_path,
  DROP COLUMN logo_dark_path;
