-- M47 Wave 7 — Per-domain MTA-STS opt-in toggle (ADR-0109).
--
-- mta_sts_enabled: 0/1 flag, off by default. When flipped on, the
--   reconciler converges the policy file + nginx vhost + DNS records.
-- mta_sts_id: unsigned bigint used as the policy version cookie in the
--   `_mta-sts.<domain>` TXT record (`v=STSv1; id=<this>`). Bumped on
--   every enable/mode-change so receivers invalidate cached policies.
--   0 sentinel means "no policy ever published"; the repo sets it to
--   the current unix timestamp on first enable.
ALTER TABLE domains
  ADD COLUMN mta_sts_enabled TINYINT(1) NOT NULL DEFAULT 0,
  ADD COLUMN mta_sts_id      BIGINT UNSIGNED NOT NULL DEFAULT 0;
