-- M30.2: per-destination restic repository password.
--
-- Stored AES-256-GCM-sealed under /etc/jabali-panel/sso.key (same
-- envelope as mysqladmin_password_enc). Nullable so existing rows
-- backfill lazily — first rotate-password click writes the column;
-- until then `backup.repo.password.read` falls back to the legacy
-- shared password file at /etc/jabali-panel/backup.repo.password.

ALTER TABLE backup_destinations
  ADD COLUMN password_enc      VARBINARY(512) NULL AFTER credentials_ref,
  ADD COLUMN password_rotated_at DATETIME(6)  NULL AFTER password_enc;
