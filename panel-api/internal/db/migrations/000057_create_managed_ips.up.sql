-- M24 Step 1: managed_ips pool table (schema only).
--
-- managed_ips is the admin-curated pool of IP addresses that can be
-- bound to a domain via domains.listen_ipv4_id / listen_ipv6_id
-- (added in 000058). The default row for each family (is_default=TRUE,
-- sourced from server_settings.public_ipv4/public_ipv6) is seeded by
-- panel-api at first boot — NOT in this migration.
--
-- Why the seed is NOT here (history of a real footgun):
-- An earlier version of this migration seeded the default row by
-- SELECTing from server_settings. On fresh installs that broke:
-- install.sh runs migrations (via `jabali-panel serve` → db.Migrate)
-- BEFORE the server_settings row is populated (the first-boot seed in
-- serve.go runs AFTER migrations complete). Migration 57 would crash
-- with ER_BAD_NULL_ERROR, get marked dirty, and every subsequent
-- restart crash-looped with "Dirty database version 57. Fix and force
-- version." — bricking every fresh install.
--
-- Clean separation: migrations shape the DB; the application seeds
-- data. `managedIPRepo.EnsureDefault(ctx, addr, family)` is called
-- from serve.go right after server_settings.Upsert, so the default
-- row appears exactly once on first boot and no-ops on re-runs.
--
-- The 'degraded' column is included here even though it's only set
-- and read by Step 4's rebind-on-start loop — keeping every M24
-- schema change in one atomic migration (review finding F-H-5) lets
-- us roll back the whole feature with `migrate down 2`.
CREATE TABLE managed_ips (
  id                 BIGINT UNSIGNED NOT NULL AUTO_INCREMENT PRIMARY KEY,
  address            VARCHAR(45)  NOT NULL,
  family             ENUM('ipv4','ipv6') NOT NULL,
  label              VARCHAR(120) NOT NULL DEFAULT '',
  -- is_default marks the per-family fallback IP used when a domain has
  -- no listen_ipv*_id binding. Mirrors server_settings.public_ipv*.
  is_default         BOOLEAN      NOT NULL DEFAULT FALSE,
  -- is_bound is true once the agent has confirmed `ip addr add` succeeded
  -- on the host kernel. Pre-bound externally (operator added to netplan
  -- before adding via the UI) stays false; the panel won't try to re-bind.
  is_bound           BOOLEAN      NOT NULL DEFAULT FALSE,
  -- is_user_selectable exposes the IP in the user-shell domain picker.
  -- Default false: admins must explicitly opt an IP into user-side use.
  is_user_selectable BOOLEAN      NOT NULL DEFAULT FALSE,
  -- degraded means the agent's rebind-on-start loop or the post-bind
  -- connectivity probe (Step 3 R9 mitigation) flagged this IP as
  -- non-functional. UI surfaces this; operator must reassign or fix.
  degraded           BOOLEAN      NOT NULL DEFAULT FALSE,
  created_at         DATETIME     NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at         DATETIME     NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  UNIQUE KEY uq_managed_ips_address (address),
  KEY idx_managed_ips_family (family)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
