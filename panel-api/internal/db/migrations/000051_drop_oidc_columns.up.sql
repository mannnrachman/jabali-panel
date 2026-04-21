-- Rollback M16 Wave D OIDC provisioning fields. Step 1 removed all code
-- that reads/writes OIDCClientID and OIDCClientSecretEnc; this migration
-- drops the orphan columns. Idempotent: both statements guard with IF EXISTS.
DROP INDEX IF EXISTS uniq_app_installs_oidc_client_id ON application_installs;
ALTER TABLE application_installs
  DROP COLUMN IF EXISTS oidc_client_id,
  DROP COLUMN IF EXISTS oidc_client_secret_enc;
