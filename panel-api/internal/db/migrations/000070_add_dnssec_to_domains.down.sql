DROP TABLE IF EXISTS domain_dnssec_keys;
ALTER TABLE domains
  DROP COLUMN dnssec_enabled,
  DROP COLUMN dnssec_enabled_at;
