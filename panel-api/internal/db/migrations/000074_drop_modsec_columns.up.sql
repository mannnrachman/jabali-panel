-- Drop ModSecurity columns (2026-04-26). ADR-0055 SUPERSEDED — CrowdSec
-- AppSec covers the WAF role, no duplicate inspection layer.
-- Mirrors the ALTERs added in migration 000066 in reverse.

ALTER TABLE domains
  DROP COLUMN modsec_enabled;

ALTER TABLE server_settings
  DROP COLUMN modsec_global_enabled,
  DROP COLUMN modsec_paranoia_level;
