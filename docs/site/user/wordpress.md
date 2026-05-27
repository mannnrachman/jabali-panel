# WordPress (User)

WordPress is the first-class app in the [Applications](./applications.md) registry.

## Installing

Applications → **Install new** → WordPress.

Required fields:

- Destination domain (one of your hosted domains).
- Install path — root or subdirectory.
- Site title.
- Admin username and admin password (or generate one).
- Admin email.
- Locale (en_US, he_IL, ar_SA, fr_FR, de_DE, etc.).

The agent provisions the DB, downloads the latest WP, writes `wp-config.php` with the DB credentials and freshly-generated salts, runs `wp core install`, and writes the magic SSO file inside the install directory. Total time: roughly 25 seconds.

## Opening the admin panel

In the Applications list, the WordPress install row has an **Open Admin** button. Clicking it:

1. The panel writes `<install-dir>/jabali-sso-<43-char-nonce>.php` (random nonce filename, single-use).
2. Your browser is redirected to that URL.
3. The SSO file calls `wp_signon()` for the admin user, sets the auth cookie, redirects you to `/wp-admin/`, then `flock`s + `unlink`s itself.
4. The TTL is 60 seconds; a systemd reaper sweeps any orphaned SSO files every 30 seconds.

You arrive logged in. No password prompt.

## Updating

The Applications row shows the installed version and whether an upstream update is available. Click **Update** to run `wp core update`, `wp plugin update --all`, and `wp theme update --all` against this install.

Updates may run database migrations; for high-traffic sites, schedule the update during a low-traffic window.

## Cloning

WordPress supports clone-to-another-domain from the Applications page:

1. Pick the source install.
2. Pick the destination domain (must be empty at the install path).
3. The agent rsyncs files, dumps and restores the DB into a new DB row, runs `wp search-replace` on the source URL → destination URL, writes a fresh magic SSO file.

The clone preserves themes, plugins, and content. Salts are regenerated so the cloned site does not share login cookies with the source.

## Deleting

Per-row **Delete**. Asks twice. Removes the install directory, drops the DB and DB user, removes the install record.

## What is *not* shipped

- **Object cache plugin auto-install** (Redis object cache). Install manually via `wp plugin install` if desired.
- **Per-domain FastCGI cache** — planned (ADR-0108), not yet shipped.
- **Auto-purge on post update** — depends on the FastCGI cache landing first.
- **WP-CLI in your `$PATH`** — `wp` is available via `~/bin/wp` for your account; not globally.

## Multisite

Standard WordPress multisite is supported but the panel does not provision the multisite-specific subdomain mapping. Configure multisite manually after the base install if needed; the operator may need to add DNS wildcards.
