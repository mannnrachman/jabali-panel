-- M27 Step 5: captcha remediation credentials for crowdsec-nginx-bouncer.
-- Plaintext storage follows the existing server_settings pattern (kratos
-- admin secret, VAPID private key, SMTP relay password are all plaintext
-- in this table). Secret value is WRITE-ONLY at the API layer (never
-- returned via GET).
ALTER TABLE server_settings
  ADD COLUMN crowdsec_captcha_enabled    BOOLEAN      NOT NULL DEFAULT FALSE AFTER appsec_geoblock_countries,
  ADD COLUMN crowdsec_captcha_provider   VARCHAR(32)  NOT NULL DEFAULT ''    AFTER crowdsec_captcha_enabled,
  ADD COLUMN crowdsec_captcha_site_key   VARCHAR(512) NOT NULL DEFAULT ''    AFTER crowdsec_captcha_provider,
  ADD COLUMN crowdsec_captcha_secret_key VARCHAR(512) NOT NULL DEFAULT ''    AFTER crowdsec_captcha_site_key;
