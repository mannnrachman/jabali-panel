-- M37 Step 1: PostgreSQL parity knobs in server_settings.
--
-- server_settings is the single-row config table (id=1) — every
-- operator-editable server-wide setting is a column here, not a
-- key/value pair. Add the two PG knobs the same way M28 added the
-- branding columns.
--
-- postgres_enabled — Phase 1 ships with the service installed but
-- disabled; admin flips this to TRUE in Server Settings to start
-- using PG.
-- postgres_max_connections_per_user — soft cap surfaced when we
-- create a per-user PG role in Wave A. Default 25 mirrors the
-- MariaDB max_user_connections we tune in install_mariadb.

ALTER TABLE server_settings
  ADD COLUMN postgres_enabled                  TINYINT(1)        NOT NULL DEFAULT 0,
  ADD COLUMN postgres_max_connections_per_user SMALLINT UNSIGNED NOT NULL DEFAULT 25;
