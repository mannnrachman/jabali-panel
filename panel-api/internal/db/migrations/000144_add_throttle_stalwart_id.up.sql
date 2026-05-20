-- M47 Wave 3 — outbound throttle Stalwart cursor.
--
-- mail_outbound_policy already exists (mig 000139). Wave 3 ships the
-- reconciler that pushes each row into Stalwart's MtaOutboundThrottle
-- object — we need to remember the Stalwart-assigned id per row so
-- updates target the right object and deletes don't leak.
--
-- stalwart_id empty = not yet pushed (or push failed). The reconciler
-- treats empty as "create"; non-empty as "update". Disable + clear via
-- DELETE on the row triggers the reconciler's clean-up path.
ALTER TABLE mail_outbound_policy
  ADD COLUMN stalwart_id VARCHAR(64) NOT NULL DEFAULT '',
  ADD COLUMN last_applied_at DATETIME(6) NULL,
  ADD COLUMN last_error TEXT NULL;
