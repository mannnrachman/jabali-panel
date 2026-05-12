-- ADR-0095 decision 6: lazy per-account size cache.
-- Keyed by (host, source_user). TTL enforced in app via fetched_at +
-- 24h window — keeping computation in app makes adjusting TTL trivial.
CREATE TABLE migration_account_size_cache (
  id          BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  host        VARCHAR(255) NOT NULL,
  source_user VARCHAR(255) NOT NULL,
  size_bytes  BIGINT UNSIGNED NOT NULL,
  fetched_at  DATETIME NOT NULL,
  PRIMARY KEY (id),
  UNIQUE KEY uniq_host_user (host, source_user)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
