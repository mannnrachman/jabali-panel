-- Add SSL/TLS enablement flag to domains.
-- Tracks per-domain opt-in for ACME certificate provisioning.
-- Disabled by default; users toggle per domain from the UI.
ALTER TABLE domains
ADD COLUMN ssl_enabled TINYINT(1) NOT NULL DEFAULT 0;
