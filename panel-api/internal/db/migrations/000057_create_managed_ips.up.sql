-- M24 Step 1: managed_ips pool table.
--
-- managed_ips is the admin-curated pool of IP addresses that can be
-- bound to a domain via domains.listen_ipv4_id / listen_ipv6_id
-- (added in 000058). Server primary IPv4/IPv6 from server_settings
-- are seeded as is_default rows below.
--
-- Why a single migration owns both schema + seed: the 'degraded' column
-- is included here even though it's only set/read in Step 4 — keeping
-- every M24 schema change in one atomic wave (review finding F-H-5)
-- lets us roll back the whole feature with `migrate down 2`.
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

-- Seed from server_settings. NULLIF treats empty string as missing.
SET @v4 := NULLIF((SELECT public_ipv4 FROM server_settings WHERE id = 1), '');
SET @v6 := NULLIF((SELECT public_ipv6 FROM server_settings WHERE id = 1), '');

-- IPv4 is REQUIRED (per M3, every panel host has a public IPv4). Plain
-- INSERT — not IGNORE — so a NULL @v4 raises ER_BAD_NULL_ERROR and the
-- migration aborts with "Column 'address' cannot be null", forcing the
-- operator to populate server_settings.public_ipv4 before retrying.
-- ON DUPLICATE KEY UPDATE makes re-runs a no-op (same address already
-- seeded), without needing INSERT IGNORE which would mask the NULL
-- failure under non-strict SQL modes (review finding F-C-1).
INSERT INTO managed_ips (address, family, label, is_default, is_bound, is_user_selectable)
  VALUES (@v4, 'ipv4', 'server primary (v4)', TRUE, FALSE, FALSE)
  ON DUPLICATE KEY UPDATE address = address;

-- IPv6 is OPTIONAL — many panel hosts run IPv4-only. WHERE filter skips
-- the row entirely when @v6 is NULL/empty; INSERT IGNORE absorbs the
-- unique-conflict on re-runs.
--
-- Note: derived-table alias is `seed1`, NOT `dual` — `dual` became a
-- reserved word in MariaDB 11.4+ and using it as an identifier raises
-- ER_PARSE_ERROR on fresh installs against current MariaDB.
INSERT IGNORE INTO managed_ips (address, family, label, is_default, is_bound, is_user_selectable)
  SELECT @v6, 'ipv6', 'server primary (v6)', TRUE, FALSE, FALSE
    FROM (SELECT 1) AS seed1
   WHERE @v6 IS NOT NULL;
