-- Cascade the rest of the user → database FK forest. After 000106 flipped
-- databases.user_id + database_users.user_id to CASCADE, three downstream
-- FKs still RESTRICTed and blocked the cascade chain on user delete:
--
--   1. database_user_grants.database_user_id → database_users  RESTRICT
--   2. database_user_grants.database_id      → databases       RESTRICT
--   3. application_installs.db_id            → databases       RESTRICT
--
-- All three need CASCADE so DELETE FROM users works when the user owns any
-- application-installed database (mx 2026-04-30: cf44d783 surfaced the
-- chain via the new translateErr log path).
--
-- `databases` is a MariaDB reserved word — backticked.

ALTER TABLE database_user_grants DROP FOREIGN KEY fk_database_user_grants_database_id;

ALTER TABLE database_user_grants ADD CONSTRAINT fk_database_user_grants_database_id
  FOREIGN KEY (database_id) REFERENCES `databases`(id) ON DELETE CASCADE;

ALTER TABLE database_user_grants DROP FOREIGN KEY fk_database_user_grants_database_user_id;

ALTER TABLE database_user_grants ADD CONSTRAINT fk_database_user_grants_database_user_id
  FOREIGN KEY (database_user_id) REFERENCES database_users(id) ON DELETE CASCADE;

ALTER TABLE application_installs DROP FOREIGN KEY fk_wpinstalls_db;

ALTER TABLE application_installs ADD CONSTRAINT fk_wpinstalls_db
  FOREIGN KEY (db_id) REFERENCES `databases`(id) ON DELETE CASCADE;
