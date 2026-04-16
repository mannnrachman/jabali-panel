ALTER TABLE users
    ADD COLUMN package_id CHAR(26) NULL AFTER is_admin,
    ADD KEY ix_users_package_id (package_id);
