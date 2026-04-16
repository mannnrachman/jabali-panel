-- Flip the default for domains.ssl_enabled from OFF to ON.
--
-- Context: the earlier opt-in model (migration 000014) assumed admins
-- would enable SSL per-domain after creation. In practice every hosted
-- domain wants HTTPS, so we flip the default and backfill existing
-- rows so the reconciler will start ACME issuance on the next tick.
-- Domains that genuinely don't want SSL (internal IPs, non-public
-- names) can still be toggled off explicitly via the edit page.

UPDATE domains SET ssl_enabled = 1 WHERE ssl_enabled = 0;

ALTER TABLE domains ALTER COLUMN ssl_enabled SET DEFAULT 1;
