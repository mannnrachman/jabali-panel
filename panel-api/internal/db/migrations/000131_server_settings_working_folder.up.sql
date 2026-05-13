-- Add working_folder to server_settings. Defines the base directory
-- used by migration imports + backup repos. Subdirs:
--   <working_folder>/migrations/<job-id>/  — extracted cpmove / DA / Hestia tarballs
--   <working_folder>/backups/repo          — restic repository
--   <working_folder>/backups/logs/         — per-job backup logs
-- Default /var/lib/jabali keeps install.sh + reconciler invariant.
-- Operators retarget to a larger disk (e.g. /mnt/storage/jabali) when
-- /var partition is small.
ALTER TABLE server_settings
  ADD COLUMN IF NOT EXISTS working_folder VARCHAR(255) NOT NULL DEFAULT '/var/lib/jabali';
