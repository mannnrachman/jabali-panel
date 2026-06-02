-- Multi-runtime hosting support (Phase 1).
-- Adds a runtime_type discriminator to domains so the reconciler
-- and nginx vhost generator can route traffic to the correct
-- backend: PHP-FPM (fastcgi_pass), reverse proxy (proxy_pass for
-- Node.js/Go/Python), Docker container, or plain static files.
--
-- Default 'php' preserves backward compatibility — every existing
-- domain keeps its current PHP-FPM behaviour unchanged.

ALTER TABLE domains
  ADD COLUMN runtime_type VARCHAR(16) NOT NULL DEFAULT 'php'
  AFTER php_max_input_time;
