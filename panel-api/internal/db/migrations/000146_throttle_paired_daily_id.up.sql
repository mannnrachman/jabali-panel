-- M47 Wave 3 v3 — paired hourly+daily throttle Stalwart objects.
--
-- v2 (mig 000144) tracked ONE stalwart_id per policy row because
-- Stalwart's MtaOutboundThrottle.rate carries a single time window.
-- When both max_per_hour AND max_per_day were set, hourly won and
-- daily was logged-only.
--
-- v3 splits into TWO Stalwart objects per row when both are set:
--   stalwart_id        → hourly throttle (kept name for back-compat)
--   stalwart_id_daily  → daily throttle (NEW)
-- The reconciler creates/updates/deletes each independently.
ALTER TABLE mail_outbound_policy
  ADD COLUMN stalwart_id_daily VARCHAR(64) NOT NULL DEFAULT ''
    AFTER stalwart_id;
