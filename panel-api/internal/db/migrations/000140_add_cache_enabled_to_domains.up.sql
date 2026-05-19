-- Per-domain opt-in nginx FastCGI micro-cache (ADR-0108).
-- Operator intent only; the reconciler converges the vhost and the
-- agent renders the cache directives. Fixed 60s TTL — no ttl column.

ALTER TABLE domains
  ADD COLUMN cache_enabled TINYINT(1) NOT NULL DEFAULT 0;
