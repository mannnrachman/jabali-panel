-- M39 rollback. Forward-only convention applies; this is best-effort
-- recreate of the structures dropped in the up migration so a manual
-- migrate-down works during development. Re-creating the table will
-- not restore prior policy_state rows — those are lost.

CREATE TABLE IF NOT EXISTS tetragon_policy_state (
  policy_name  VARCHAR(128) NOT NULL PRIMARY KEY,
  enabled      TINYINT(1) NOT NULL DEFAULT 1,
  updated_at   TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  updated_by   VARCHAR(26) NULL
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

ALTER TABLE malware_settings ADD COLUMN IF NOT EXISTS tetragon_enabled TINYINT(1) NOT NULL DEFAULT 1;
