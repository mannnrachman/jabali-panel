-- Rollback — drops the two columns + unique constraint. Existing
-- oidc_client_id values are abandoned; Hydra clients they point at
-- become orphans until an operator cleans them up with:
--
--     hydra clients list --endpoint=http://127.0.0.1:4445
--     hydra clients delete --endpoint=http://127.0.0.1:4445 <id>
--
-- Orphan clients are harmless (their redirect_uris point at subdirs
-- that may still exist or not). Hydra's janitor doesn't reap clients,
-- only expired tokens. Keeping this down migration destructive is
-- deliberate — a partial rollback that retained the columns would
-- leave the table shape incompatible with the old model.
ALTER TABLE application_installs
  DROP CONSTRAINT uniq_app_installs_oidc_client_id,
  DROP COLUMN oidc_client_id,
  DROP COLUMN oidc_client_secret_enc;
