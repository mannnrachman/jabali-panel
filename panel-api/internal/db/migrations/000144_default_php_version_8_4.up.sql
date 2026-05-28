-- Move the panel-wide default PHP version from 8.5 to 8.4 (GH#111).
--
-- phpMyAdmin 5.2.x cannot run on PHP 8.5 (its bundled Symfony DI drops
-- core services -> "ServiceNotFoundException: non-existent service config"),
-- so install.sh now installs PHP 8.4 by default and the pma FPM pool runs
-- on 8.4. The panel's server_settings.default_php_version (the version new
-- user pools are seeded with by the reconciler) was still '8.5', set by
-- migration 000031. On a host that now only has 8.4 installed, a new domain
-- would be pinned to an uninstalled 8.5 and its FPM pool would fail to start.
--
-- 1. Change the column default for fresh inserts.
-- 2. Flip the existing singleton row only if it is still on the old
--    default '8.5' — an admin who deliberately picked another version is
--    left untouched. Existing per-user PHPPool rows are NOT modified here,
--    so already-running sites keep their pinned version.
ALTER TABLE server_settings
  ALTER COLUMN default_php_version SET DEFAULT '8.4';

UPDATE server_settings
  SET default_php_version = '8.4'
  WHERE default_php_version = '8.5';
