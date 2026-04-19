-- M20 Step 4: add kratos_identity_id to users so each panel user maps 1:1
-- to an Ory Kratos identity UUID. Nullable because legacy rows (pre-M20)
-- have no Kratos identity until the `jabali kratos-migrate` tool backfills
-- them or a new user is created via the API (which now atomically creates
-- both rows when auth.provider = "kratos").
--
-- MariaDB's UNIQUE KEY treats multiple NULLs as distinct per the SQL
-- standard, so this constraint blocks duplicate non-NULL identity IDs
-- without rejecting the rows that are still waiting for backfill. No
-- partial/filtered index is needed (MariaDB doesn't support them anyway).

ALTER TABLE users
  ADD COLUMN kratos_identity_id VARCHAR(64) DEFAULT NULL,
  ADD UNIQUE KEY ux_users_kratos_identity_id (kratos_identity_id);
