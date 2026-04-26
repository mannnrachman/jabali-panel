-- Move per-user nspawn image pin to the hosting package. Per-user pins
-- created confusion (every user form had a sandbox field) and admins
-- already manage SSH access at the package layer (ssh_enabled lives on
-- hosting_packages). The per-package pin lets a "Premium" tier point
-- at debian-13-v2 while "Basic" stays on debian-13-v1, and individual
-- users automatically inherit.

ALTER TABLE hosting_packages
  ADD COLUMN nspawn_image_version VARCHAR(64) NULL AFTER cgi_enabled;

ALTER TABLE users
  DROP COLUMN nspawn_image_version;
