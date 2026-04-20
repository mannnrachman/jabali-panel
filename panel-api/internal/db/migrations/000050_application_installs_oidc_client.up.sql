-- M16 Wave D Step 6: per-install OAuth 2 / OIDC client columns.
--
-- oidc_client_id        CHAR(40) — ULID we pass to Hydra as the
--                       client_id at CreateClient time. Unique across
--                       installs so the compensating-transaction on
--                       install-delete can DeleteClient(id) without
--                       risk of killing another install's client.
--
-- oidc_client_secret_enc VARBINARY(512) — AES-256-GCM envelope of the
--                       one-shot client_secret Hydra returned at
--                       create. Key is the existing sso.key (see
--                       ssokey package); envelope is nonce(12) ||
--                       ciphertext || tag(16). Nullable for:
--                         (a) pre-M16 rows already in the table,
--                         (b) the tiny window between install-row
--                             insert and CreateClient success,
--                         (c) any future app whose descriptor sets
--                             OIDCCallbackPath="" (no SSO).
--
-- Both columns are nullable. A NULL oidc_client_id means "this install
-- has no OIDC client yet" — either pre-M16 or the CreateClient step
-- rolled back. Neither state is an error; the install still serves
-- over classic wp-login etc.
ALTER TABLE application_installs
  ADD COLUMN oidc_client_id CHAR(40) NULL,
  ADD COLUMN oidc_client_secret_enc VARBINARY(512) NULL,
  ADD CONSTRAINT uniq_app_installs_oidc_client_id UNIQUE (oidc_client_id);
