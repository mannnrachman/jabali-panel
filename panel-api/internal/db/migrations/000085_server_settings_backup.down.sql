ALTER TABLE server_settings
    DROP COLUMN backup_keep_daily,
    DROP COLUMN backup_keep_weekly,
    DROP COLUMN backup_keep_monthly,
    DROP COLUMN backup_remote_url,
    DROP COLUMN backup_remote_credentials_ref;
