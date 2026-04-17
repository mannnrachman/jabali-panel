ALTER TABLE database_user_grants ADD COLUMN privileges VARCHAR(255) NOT NULL DEFAULT 'ALL';

UPDATE database_user_grants SET privileges = 'ALL' WHERE grant_level = 'rw';
UPDATE database_user_grants SET privileges = 'SELECT' WHERE grant_level = 'ro';
