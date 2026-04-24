-- M14 Step 1: VAPID keypair columns on server_settings.
--
-- Three nullable columns that hold the per-installation Web Push VAPID
-- keypair + subject. See ADR-0057. Columns are NULL on upgrade; panel-api's
-- first-boot seed (serve.go) generates the keypair on first start and
-- Upserts into the same row — mirrors how the PublicIPv* seed works.
--
-- VARCHAR sizes:
--   * vapid_public_key  = base64-url of an uncompressed P-256 point, 87 chars.
--     Size 128 to leave headroom for future P-384/custom curves without another
--     ALTER; the library (SherClockHolmes/webpush-go) pins P-256 today.
--   * vapid_private_key = base64-url of a 32-byte scalar, 43 chars. VARCHAR(64)
--     for the same future-proofing reason.
--   * vapid_subject     = "mailto:admin@<hostname>" — 320 matches the RFC-5321
--     local+domain cap, the same limit we apply to admin_email above.

ALTER TABLE server_settings
  ADD COLUMN vapid_public_key  VARCHAR(128) NULL AFTER ssh_user_password_auth,
  ADD COLUMN vapid_private_key VARCHAR(64)  NULL AFTER vapid_public_key,
  ADD COLUMN vapid_subject     VARCHAR(320) NULL AFTER vapid_private_key;
