ALTER TABLE database_user_grants
    MODIFY COLUMN grant_level ENUM('rw','ro') NOT NULL;
