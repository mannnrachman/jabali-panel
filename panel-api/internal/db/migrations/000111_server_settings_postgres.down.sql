-- M37 Step 1 rollback. Drops the postgres knobs.

ALTER TABLE server_settings
  DROP COLUMN postgres_enabled,
  DROP COLUMN postgres_max_connections_per_user;
