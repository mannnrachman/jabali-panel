ALTER TABLE dns_records
  DROP INDEX ix_dns_records_managed_by,
  DROP COLUMN managed_by;
