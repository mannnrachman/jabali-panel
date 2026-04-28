-- M30.1 backup destinations (ADR-0078).
-- Admin-managed remote destinations for restic copy. The local repo is
-- implicit (always present at /var/lib/jabali-backups/repo); rows in
-- this table represent remotes only when kind != 'local'. A 'local' row
-- exists for symmetry so schedules can reference local + remote uniformly,
-- but the copy worker no-ops on it.
--
-- Credentials never live in the DB. credentials_ref points at a 0600
-- root:root env file under /etc/jabali-panel/restic-remotes/<id>.env
-- holding restic backend env vars (AWS_*, B2_*, AZURE_*, etc.).

CREATE TABLE backup_destinations (
  id              CHAR(26)     NOT NULL PRIMARY KEY,
  name            VARCHAR(64)  NOT NULL,
  kind            ENUM('local','sftp','s3','b2','azure','gcs','rest') NOT NULL,
  url             VARCHAR(512) NOT NULL,
  credentials_ref VARCHAR(255) NULL,
  enabled         TINYINT(1)   NOT NULL DEFAULT 1,
  created_at      DATETIME(6)  NOT NULL,
  updated_at      DATETIME(6)  NOT NULL,
  UNIQUE KEY uniq_backup_dest_name (name),
  KEY idx_backup_dest_enabled (enabled)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
