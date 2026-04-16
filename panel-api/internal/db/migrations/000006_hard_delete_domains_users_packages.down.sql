-- Rollback: restore deleted_at columns and indexes for soft delete

ALTER TABLE hosting_packages ADD COLUMN deleted_at DATETIME(6) NULL;
ALTER TABLE users ADD COLUMN deleted_at DATETIME(6) NULL;
ALTER TABLE domains ADD COLUMN deleted_at DATETIME(6) NULL;

ALTER TABLE hosting_packages ADD INDEX ix_packages_deleted_at (deleted_at);
ALTER TABLE users ADD INDEX ix_users_deleted_at (deleted_at);
ALTER TABLE domains ADD INDEX ix_domains_deleted_at (deleted_at);
