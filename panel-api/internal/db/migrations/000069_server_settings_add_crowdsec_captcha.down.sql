ALTER TABLE server_settings
  DROP COLUMN crowdsec_captcha_secret_key,
  DROP COLUMN crowdsec_captcha_site_key,
  DROP COLUMN crowdsec_captcha_provider,
  DROP COLUMN crowdsec_captcha_enabled;
