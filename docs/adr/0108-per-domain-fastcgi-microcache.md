# ADR-0108: Per-domain opt-in nginx FastCGI micro-cache

**Status**: accepted (2026-05-19)

## Context

Tenants want faster sites without per-app tuning. Every domain vhost
served PHP uncached. We add an opt-in per-domain switch that turns on
an nginx FastCGI **micro-cache** plus long static-asset cache headers,
mirroring the existing DNSSEC/SSL per-domain toggle end-to-end
(DB-as-truth → reconciler → agent vhost → UI) so the pattern stays
familiar and merges stay clean.

## Decision

- **`domains.cache_enabled`** (migration 000140, bool, default 0) is
  operator intent. Off by default.
- **DB-as-truth**: the `PUT /domains/:id/cache` toggle persists +
  schedules a reconcile. The reconciler passes `cache_enabled` into
  `domain.create`; the agent renders the cache directives into the
  vhost and reloads nginx. No synchronous agent op for the toggle
  (unlike DNSSEC) — the cache is vhost-rendered.
- **Authz**: owner-or-admin (`loadAndAuth`, same as SSL/Email). The
  v1 UI surface is the admin domain editor (`DomainEdit`, where the
  SSL/Email/IPACL toggles already live); the API is owner-capable for
  a future user-facing domain editor.
- **What it caches**: FastCGI full-page micro-cache, **fixed 60 s**
  TTL (no ttl column/UI — short TTL is the safety mechanism), plus
  `expires 30d` + immutable Cache-Control on static assets
  (css/js/img/fonts).
- **Safe bypass set** (any ⇒ no cache): POST, non-empty query string,
  URIs `/wp-admin//wp-login/xmlrpc.php/wp-cron.php/cart/checkout/`
  `my-account/wc-api//edd-api/`, cookies
  `comment_author|wordpress_[a-f0-9]+|wp-postpass|wordpress_logged_in|`
  `woocommerce_items_in_cart|woocommerce_cart_hash|edd_items_in_cart|`
  `PHPSESSID`. `X-Jabali-Cache: $upstream_cache_status` debug header.
- **One shared keyzone** `jabali_fcgi`
  (`/etc/nginx/conf.d/jabali-fastcgi-cache.conf`,
  `/var/cache/nginx/jabali` 0700 www-data) shipped by install.sh AND
  **re-applied on every `jabali update`** (update.go buildStep, with
  `nginx -t` before reload). This is load-bearing: a per-domain vhost
  referencing the zone before it exists fails `nginx -t`. Honors the
  recurring "jabali update doesn't refresh host config" scar
  (PR#45/#49).
- **OFF == behaviourally identical**: every cache/static directive is
  wholly inside `{{ if .CacheEnabled }}`; the golden test asserts the
  marker set is absent when off and present when on, and that the
  normal PHP location is intact.
- **Purge v1**: manual button → agent `nginx.cache.purge` →
  host-key grep-unlink in the shared keyzone (the cache_key embeds
  `$host`). No nginx reload (nginx re-MISSes deleted entries).

## Consequences / out of scope (documented, not faked)

- **Shared keyzone eviction**: one 64 m/4 g zone across all domains; a
  noisy domain can evict another's entries. Acceptable for a 60 s
  micro-cache; revisit with per-domain `keys_zone` if it bites.
- **Purge is O(cache size)** (grep-unlink). Fine for typical sites;
  full per-domain cache partitioning is a follow-up.
- **WordPress auto-purge on content change** (mu-plugin) deferred.
- **Configurable TTL** deferred (fixed 60 s by design).
- Non-WP PHP apps with a session cookie not in the bypass regex could
  serve a cached authenticated page for ≤60 s; mitigated by the short
  TTL + `PHPSESSID` catch; surfaced in the UI copy.
