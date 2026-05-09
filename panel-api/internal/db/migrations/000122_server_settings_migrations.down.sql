ALTER TABLE server_settings
    DROP COLUMN migrations_imap_concurrent_folders,
    DROP COLUMN migrations_concurrent_per_source,
    DROP COLUMN migrations_enabled;
