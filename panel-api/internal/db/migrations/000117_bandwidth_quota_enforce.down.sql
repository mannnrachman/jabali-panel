DROP INDEX idx_domains_quota_suspended ON domains;
ALTER TABLE domains DROP COLUMN is_quota_suspended;
ALTER TABLE server_settings DROP COLUMN bandwidth_quota_enforce_enabled;
