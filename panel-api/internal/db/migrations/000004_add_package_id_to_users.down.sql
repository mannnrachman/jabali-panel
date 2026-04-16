ALTER TABLE users
    DROP KEY ix_users_package_id,
    DROP COLUMN package_id;
