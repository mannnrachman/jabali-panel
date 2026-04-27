-- M30 backup-restore: per-server retention knobs + remote-repo pointers.
-- backup_remote_url empty = local repo at /var/lib/jabali-backups/repo only.
-- backup_remote_credentials_ref points at /etc/jabali-panel/restic-remote.env
-- when an operator opts into S3/SFTP/B2 in M30.1; v1 leaves it empty.

ALTER TABLE server_settings
    ADD COLUMN backup_keep_daily             INT UNSIGNED NOT NULL DEFAULT 7,
    ADD COLUMN backup_keep_weekly            INT UNSIGNED NOT NULL DEFAULT 4,
    ADD COLUMN backup_keep_monthly           INT UNSIGNED NOT NULL DEFAULT 6,
    ADD COLUMN backup_remote_url             VARCHAR(512) NOT NULL DEFAULT '',
    ADD COLUMN backup_remote_credentials_ref VARCHAR(128) NOT NULL DEFAULT '';
