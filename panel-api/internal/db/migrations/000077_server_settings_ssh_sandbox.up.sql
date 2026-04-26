-- M13 SSH shell sandbox (ADR-0067).
ALTER TABLE server_settings
  ADD COLUMN ssh_sandbox_mode VARCHAR(16) NOT NULL DEFAULT 'bubblewrap',
  ADD COLUMN default_nspawn_image_version VARCHAR(64) NOT NULL DEFAULT 'debian-12-v1';
