-- Backfill existing _dmarc TXT records from p=none to p=quarantine.
-- bootstrap.go already emits p=quarantine for new domains; this migration
-- converges domains created before that change (or before M6.4 added the
-- quarantine policy). Only touches records with p=none in the content —
-- any domain whose operator already upgraded to p=quarantine is skipped.
UPDATE dns_records
SET    content    = '"v=DMARC1; p=quarantine; sp=quarantine; adkim=r; aspf=r"',
       updated_at = NOW()
WHERE  name = '_dmarc'
  AND  type = 'TXT'
  AND  content LIKE '%p=none%';
