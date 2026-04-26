-- M13: per-user nspawn image pin. Reconciler stamps NULL rows with the
-- server-wide default at next sweep; existing pins are preserved across
-- default upgrades so tenants don't get silently bumped to a new rootfs.
ALTER TABLE users
  ADD COLUMN nspawn_image_version VARCHAR(64) NULL;
