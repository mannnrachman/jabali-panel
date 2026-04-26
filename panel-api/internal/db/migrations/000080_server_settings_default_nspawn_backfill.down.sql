-- No-op down. The bump is idempotent and ratchets forward; rolling it
-- back would re-pin operators to a non-existent debian-12-v1 image.
SELECT 1;
