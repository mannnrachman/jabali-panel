-- M37 Phase 2 rollback. Drop the engine column.

ALTER TABLE database_users
  DROP INDEX idx_engine,
  DROP COLUMN engine;
