-- ADR-0095 decision 8: opt-in private-host override for migration
-- outbound dials (default off — SSRF guards block RFC1918/loopback
-- /link-local/ULA by default).
ALTER TABLE server_settings
  ADD COLUMN migration_allow_private_hosts BOOLEAN NOT NULL DEFAULT FALSE;
