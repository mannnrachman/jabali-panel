# WordPress

WordPress is the first-class app in Jabali's Applications Framework (M10 / M19).

## One-click install

`/jabali-panel/applications` → pick the domain → WordPress → **Install**.

The wizard collects:
- Site title, admin username, admin password (or generate one), admin email.
- Install path (root or subdir).
- Locale (en_US, he_IL, ar_SA, etc.).

The agent then:

1. Provisions a MariaDB database + DB user.
2. Downloads the latest WP via `wp core download`.
3. Writes `wp-config.php` with the DB creds + secret salts.
4. Runs `wp core install` (idempotent).
5. Writes a self-deleting magic SSO file inside the install dir.
6. Records the install in `application_installs` with `app_id=wordpress`.

Total time: ~25 seconds for a fresh install on a standard VPS.

## One-click admin

In the Applications list, each install has an **Open Admin** button. Clicking it:

1. The panel writes `<install-dir>/jabali-sso-<43-char-nonce>.php`.
2. Redirects the browser to `https://<domain>/jabali-sso-<nonce>.php`.
3. The file calls `wp_signon()` for the admin user, sets the auth cookie, redirects to `/wp-admin/`, then `flock`s + `unlink`s itself.
4. The systemd reaper sweeps any orphaned SSO files every 30 s (60 s TTL ceiling).

This is the M22 self-deleting SSO file pattern (ADR-0040, Installatron / Softaculous style). The earlier magic-link mu-plugin + HMAC callback approach (failed M22) is **not** in use.

## Clone

Applications → Clone → pick source install + destination domain. The agent rsyncs the install dir, dumps + restores the DB into a new DB row, runs `wp search-replace` on the site URL, writes a fresh SSO file.

## Delete

Applications → Delete. Removes the install dir, drops the DB + DB user, removes the install record. Asks twice; destructive.

## Auto-updates

Per-install toggle. When on, the panel runs `wp core update && wp plugin update --all && wp theme update --all` weekly via a systemd-user timer owned by the WP install's Linux user.

## What is *not* shipped

- **Object cache** — no Redis object cache plugin auto-install.
- **WP-CLI globally** — `wp` is available to the install's user via `~/bin/wp`; not in `$PATH` for other users.
- **Per-domain FastCGI cache** — planned (ADR-0108), not yet shipped.
- **Auto-purge on post update** — depends on FastCGI cache landing first.

## Other apps

Same wizard pattern works for: Moodle, Drupal, Joomla, NextCloud, MediaWiki, PrestaShop, OpenCart, phpBB, Matomo, MyBB, Pixelfed, Mautic, Mahara, SuiteCRM. See [applications.md](./applications.md).
