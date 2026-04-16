-- Hard delete soft-delete tombstones and drop deleted_at columns from
-- domains, users, and hosting_packages. These resources rarely need audit
-- history; the benefit does not justify the unique constraint collision pain.

-- Clean up soft-delete tombstones first
DELETE FROM domains WHERE deleted_at IS NOT NULL;
DELETE FROM users WHERE deleted_at IS NOT NULL;
DELETE FROM hosting_packages WHERE deleted_at IS NOT NULL;

-- Drop deleted_at indexes
ALTER TABLE domains DROP INDEX ix_domains_deleted_at;
ALTER TABLE users DROP INDEX ix_users_deleted_at;
ALTER TABLE hosting_packages DROP INDEX ix_packages_deleted_at;

-- Drop deleted_at columns
ALTER TABLE domains DROP COLUMN deleted_at;
ALTER TABLE users DROP COLUMN deleted_at;
ALTER TABLE hosting_packages DROP COLUMN deleted_at;
