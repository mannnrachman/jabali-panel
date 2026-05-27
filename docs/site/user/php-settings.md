# PHP Settings

`/jabali-panel/php-settings`. Per-user PHP configuration. Applied to every domain you own that runs PHP through your per-user FPM pool.

## Editable values

| Key | Purpose |
|---|---|
| `memory_limit` | Maximum memory a PHP script may allocate. Larger values let memory-hungry CMS plugins run; too large causes the host to OOM under load. |
| `upload_max_filesize` | Maximum single uploaded file size. |
| `post_max_size` | Maximum total POST body size. Must be ≥ `upload_max_filesize`. |
| `max_execution_time` | Maximum CPU time per request. |
| `max_input_time` | Maximum time PHP spends parsing input data. |
| `max_input_vars` | Maximum POST variables per request. |
| `display_errors` | Show PHP errors to the browser. Off by default; only enable temporarily in development. |
| `date.timezone` | Default time zone for date / time functions. |

Each value is capped by your package; the form clamps to the cap on save with a UI warning.

## Application

On save, the agent rewrites your per-user FPM pool drop-in (`/etc/php/<version>/fpm/pool.d/jabali-<your-username>.conf`) and issues a graceful FPM reload (no in-flight request loss). Effects are visible on the next request.

## Per-domain overrides

The current panel applies PHP settings **per user**, not per domain. Every domain in your account inherits the same INI values. Per-domain overrides require operator action (`.user.ini` files in the docroot are honoured by PHP-FPM; the panel does not manage them).

## OpCache

OPcache is enabled per-version with operator-chosen defaults (typically 128 MiB cache, 10000 files). Tenant tuning of OpCache is not exposed; ask the operator if you need a larger cache for a code base with many files.

## JIT

JIT is off by default. Your application is unlikely to benefit from JIT for typical web-request workloads (the per-request setup cost outweighs the runtime gain on short requests). Enable per-version under the operator's [PHP Manager](../admin/php-manager.md); the operator decides at the version level.

## Choosing the right values

Most CMSes (WordPress, Drupal, Moodle) ship recommendations:

- WordPress: `memory_limit 256M`, `upload_max_filesize 64M`, `post_max_size 64M`, `max_execution_time 300`.
- Moodle: `memory_limit 512M`, `upload_max_filesize 1024M` (when accepting large coursework uploads).

Start with the recommendation, then raise specific values only when you hit an error in the app's error log.
