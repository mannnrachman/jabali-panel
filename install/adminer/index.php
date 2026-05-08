<?php
/**
 * Jabali Adminer entry-point.
 *
 * Loads the upstream Adminer single-file build with our jabali-sso
 * plugin, which:
 *  1. reads ?token=<base64url> from the URL,
 *  2. POSTs it to /sso/adminer/validate over the panel-api UDS,
 *  3. supplies the engine-specific credentials to Adminer,
 *  4. forces a single-database scope (no schema browser leakage).
 *
 * Engine routing:
 *   - mariadb  → driver "server" (Adminer's MySQLi/PDO_MySQL)
 *   - postgres → driver "pgsql"  (Adminer's PostgreSQL via libpq)
 *
 * Layout:
 *   /var/www/jabali-adminer/index.php           — this file
 *   /var/www/jabali-adminer/adminer.php         — upstream single-file
 *   /var/www/jabali-adminer/jabali-sso-plugin.php — plugin
 */

require_once __DIR__ . '/jabali-sso-plugin.php';

function adminer_object() {
    $sock = '/run/jabali-panel/sso.sock';
    $plugins = [
        new JabaliAdminerSSO($sock),
    ];
    if (!class_exists('AdminerPlugin')) {
        require_once __DIR__ . '/plugin.php';
    }
    return new AdminerPlugin($plugins);
}

require_once __DIR__ . '/adminer.php';
