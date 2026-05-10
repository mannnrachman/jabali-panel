-- Revert: set quarantine records back to p=none.
-- NOTE: this undoes records that were manually set to quarantine before
-- the migration ran — there is no way to distinguish those from backfilled
-- rows. Treat down migration as best-effort.
UPDATE dns_records
SET    content    = '"v=DMARC1; p=none"',
       updated_at = NOW()
WHERE  name = '_dmarc'
  AND  type = 'TXT'
  AND  content LIKE '%p=quarantine%';
