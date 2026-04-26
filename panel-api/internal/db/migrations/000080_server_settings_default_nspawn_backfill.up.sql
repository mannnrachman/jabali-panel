-- Defensive re-bump. Migration 000078 fired the UPDATE clause exactly
-- once per row matching default_nspawn_image_version='debian-12-v1';
-- on an install whose UI had clobbered the row with the empty string
-- before the migration ran (panel-ui pre-5685767 sent "debian-12-v1"
-- as a hardcoded fallback when the field was blank), the WHERE didn't
-- match and the row stayed bad. Catch both cases here.
UPDATE server_settings
   SET default_nspawn_image_version = 'debian-13-v1'
 WHERE default_nspawn_image_version IN ('', 'debian-12-v1');
