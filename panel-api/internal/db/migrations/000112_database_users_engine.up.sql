-- M37 Phase 2: tag database_users with engine.
--
-- A database user (DB-level role/login, not the panel hosting user) is
-- engine-specific: MariaDB users live in mysql.user, Postgres roles
-- live in pg_authid. The grants table is auto-engine-tagged via its
-- database_id FK (databases.engine), but the user row itself needs
-- to know which engine to dispatch CREATE / DROP / ALTER PASSWORD to.
--
-- Default 'mariadb' on existing rows — every M7 user predates
-- PostgreSQL parity. Phase 3 will surface engine in the create-user
-- API; Phase 2 just makes the column exist so backup tooling can
-- snapshot per-engine role lists correctly.

ALTER TABLE database_users
  ADD COLUMN engine ENUM('mariadb', 'postgres') NOT NULL DEFAULT 'mariadb' AFTER username,
  ADD INDEX idx_engine (engine);
