-- Tranche B introduced 'custom' as a marker on database_user_grants
-- for rows whose real privilege set lives in the privileges column.
-- The original enum is too narrow, so extend it.
ALTER TABLE database_user_grants
    MODIFY COLUMN grant_level ENUM('rw','ro','custom') NOT NULL;
