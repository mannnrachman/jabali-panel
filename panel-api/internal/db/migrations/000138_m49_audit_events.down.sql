-- Reverse of 000138. Step 0 was purely additive — the up migration
-- only CREATEd audit_events and backfilled INTO it from db_admin_audit
-- (db_admin_audit itself was never modified). Dropping audit_events
-- discards the backfilled copies; the originals remain untouched in
-- db_admin_audit. Fully reversible, no data loss of source rows.
DROP TABLE IF EXISTS audit_events;
