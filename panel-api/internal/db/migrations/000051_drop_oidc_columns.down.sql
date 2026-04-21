-- Compensating migration: restore OIDCClientID and OIDCClientSecretEnc columns.
-- WARNING: sealed secrets in OIDCClientSecretEnc cannot be recovered from
-- production backups. The sso.key half-life is expected; any rows restored
-- by this migration will have valid schema-compatible blobs but cannot be
-- decrypted post-rekey. Plan key rotation before running rollback in production.
ALTER TABLE application_installs
  ADD COLUMN oidc_client_id CHAR(40) DEFAULT NULL,
  ADD COLUMN oidc_client_secret_enc VARBINARY(512) DEFAULT NULL;
CREATE UNIQUE INDEX uniq_app_installs_oidc_client_id ON application_installs (oidc_client_id);
