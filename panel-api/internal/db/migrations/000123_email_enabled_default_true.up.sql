-- ADR-0080 — flip the default for new domains so email is on out of the box.
-- Existing rows are intentionally NOT touched: a tenant or admin who
-- explicitly turned email OFF on a domain (rare but legitimate — parked
-- domains, web-only marketing sites) keeps that opt-out across the
-- migration. New rows from this point forward come up with email
-- enabled and the reconciler converges DKIM + Stalwart Domain + nginx
-- mail.<domain> + LE SAN cert as part of the create flow.
ALTER TABLE domains
  MODIFY COLUMN email_enabled TINYINT(1) NOT NULL DEFAULT 1;
