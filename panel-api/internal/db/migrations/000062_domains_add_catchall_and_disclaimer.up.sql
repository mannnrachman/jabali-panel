-- M6.5 Catch-All and Disclaimer (per-domain fields)
-- Stalwart integration: x:Domain.catchAllAddress + x:SieveSystemScript
-- Jabali is truth; reconciler converges to Stalwart

ALTER TABLE domains
  ADD COLUMN catchall_target VARCHAR(255) NULL COMMENT 'catch-all address for unmatched domain mail',
  ADD COLUMN disclaimer_enabled BOOLEAN NOT NULL DEFAULT FALSE COMMENT 'whether to append domain disclaimer to outbound mail',
  ADD COLUMN disclaimer_text TEXT NULL COMMENT 'disclaimer text to append (sieve script body)';
