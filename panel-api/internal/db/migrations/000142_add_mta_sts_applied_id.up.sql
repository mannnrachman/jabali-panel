-- M47 Wave 7c — MTA-STS reconciler-converged apply (ADR-0109).
--
-- mta_sts_applied_id mirrors mta_sts_id but tracks the last id the
-- agent ACKed via mail.mtasts.apply. The reconciler dispatches when
-- applied_id != mta_sts_id, which makes the apply both diff-aware
-- (no nginx reload every tick) and self-healing (a failed apply
-- leaves applied_id stale, so the next tick retries).
ALTER TABLE domains
  ADD COLUMN mta_sts_applied_id BIGINT UNSIGNED NOT NULL DEFAULT 0;
