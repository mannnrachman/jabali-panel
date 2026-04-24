-- Global disk-quota toggle. When false (default), the reconciler skips
-- POSIX user-quota apply and the Packages UI disables the disk-quota
-- input fields. cgroups limits (cpu/memory/io/tasks) remain unaffected.
--
-- Operator must independently ensure /home is on its own filesystem
-- with usrquota,grpquota mount options before flipping this true,
-- otherwise the agent's quota apply will fail loud. install.sh already
-- guards against turning quota on when /home == /.

ALTER TABLE server_settings
    ADD COLUMN disk_quota_enabled TINYINT(1) NOT NULL DEFAULT 0;
