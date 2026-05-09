-- M13.1.1: bandwidth quota auto-suspend (opt-in).
--
-- server_settings.bandwidth_quota_enforce_enabled — global on/off
-- toggle. Off by default. When on, the reconciler walks every user
-- with a package whose BandwidthQuotaMB > 0, sums month-to-date
-- bytes, and on ≥ 100% sets every owned domain's is_enabled=false +
-- is_quota_suspended=true. On drop back ≤ 80% reverses for domains
-- where is_quota_suspended=true (panel-driven disable). Manual
-- admin disables (is_quota_suspended=false) are NEVER re-enabled by
-- the reconciler.
--
-- domains.is_quota_suspended — disambiguates panel-driven disables
-- from manual ones. Without it, a quota-driven re-enable would
-- accidentally un-suspend domains the operator wanted off for
-- unrelated reasons (parked, billing dispute, etc.).

ALTER TABLE server_settings
    ADD COLUMN bandwidth_quota_enforce_enabled TINYINT(1) NOT NULL DEFAULT 0;

ALTER TABLE domains
    ADD COLUMN is_quota_suspended TINYINT(1) NOT NULL DEFAULT 0;

CREATE INDEX idx_domains_quota_suspended ON domains (is_quota_suspended);
