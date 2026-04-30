-- Flip databases.user_id + database_users.user_id from RESTRICT to CASCADE.
--
-- Why: the M7 ADR-0019 RESTRICT was meant to prevent silent data loss. In
-- practice it blocks every panel user delete the moment the user has any
-- application-managed database, even when the operator explicitly chose
-- "Delete and purge files" in the UI. mx 2026-04-30 surfaced this as a
-- 500 on every test user delete. The destructive consent is in the UI; the
-- panel-DB rows should follow the user out.
--
-- Note: this only cleans the panel's metadata. The actual MariaDB schemas
-- + grants on the data plane stay until the operator runs `jabali db drop`
-- (or the agent reconciler garbage-collects). That's a follow-up; this
-- migration unblocks the immediate UI failure.

ALTER TABLE databases
  DROP FOREIGN KEY fk_databases_user_id,
  ADD CONSTRAINT fk_databases_user_id FOREIGN KEY (user_id)
    REFERENCES users(id) ON DELETE CASCADE;

ALTER TABLE database_users
  DROP FOREIGN KEY fk_database_users_user_id,
  ADD CONSTRAINT fk_database_users_user_id FOREIGN KEY (user_id)
    REFERENCES users(id) ON DELETE CASCADE;
