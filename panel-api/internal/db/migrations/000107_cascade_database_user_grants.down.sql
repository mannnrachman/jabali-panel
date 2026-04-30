-- Revert downstream DB-related FKs back to RESTRICT.

ALTER TABLE database_user_grants DROP FOREIGN KEY fk_database_user_grants_database_id;

ALTER TABLE database_user_grants ADD CONSTRAINT fk_database_user_grants_database_id
  FOREIGN KEY (database_id) REFERENCES `databases`(id) ON DELETE RESTRICT;

ALTER TABLE database_user_grants DROP FOREIGN KEY fk_database_user_grants_database_user_id;

ALTER TABLE database_user_grants ADD CONSTRAINT fk_database_user_grants_database_user_id
  FOREIGN KEY (database_user_id) REFERENCES database_users(id) ON DELETE RESTRICT;

ALTER TABLE application_installs DROP FOREIGN KEY fk_wpinstalls_db;

ALTER TABLE application_installs ADD CONSTRAINT fk_wpinstalls_db
  FOREIGN KEY (db_id) REFERENCES `databases`(id) ON DELETE RESTRICT;
