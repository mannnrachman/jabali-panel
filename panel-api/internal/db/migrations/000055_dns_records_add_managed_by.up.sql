-- M6 Step 6 (DNS autoconfig): mark records inserted by the email-enable
-- path so disable can scope the cleanup. NULL on existing rows (backward
-- compat — everything that's here today was M4-bootstrapped or hand-
-- edited, neither of which M6 should ever delete).
--
-- Values used:
--   NULL    — pre-M6 or user-edited (untouched by M6 lifecycle)
--   "m6"    — inserted by domain.email_enable; removed by email_disable
--
-- A plain TINYTEXT was considered but the index can't live on one; the
-- delete-on-disable query is `WHERE zone_id = ? AND managed_by = 'm6'`
-- which needs a prefix lookup on a fixed-width column.

ALTER TABLE dns_records
  ADD COLUMN managed_by VARCHAR(16) NULL AFTER managed,
  ADD INDEX ix_dns_records_managed_by (zone_id, managed_by);
