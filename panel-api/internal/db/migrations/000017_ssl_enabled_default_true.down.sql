ALTER TABLE domains ALTER COLUMN ssl_enabled SET DEFAULT 0;
-- Intentionally do NOT reset existing rows to 0 on down-migration —
-- a cert is already issued for them and tearing it down is surprising.
