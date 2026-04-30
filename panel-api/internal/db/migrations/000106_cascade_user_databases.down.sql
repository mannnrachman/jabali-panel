-- Revert databases.user_id + database_users.user_id back to RESTRICT.

ALTER TABLE databases DROP FOREIGN KEY fk_databases_user_id;

ALTER TABLE databases ADD CONSTRAINT fk_databases_user_id
  FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE RESTRICT;

ALTER TABLE database_users DROP FOREIGN KEY fk_database_users_user_id;

ALTER TABLE database_users ADD CONSTRAINT fk_database_users_user_id
  FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE RESTRICT;
