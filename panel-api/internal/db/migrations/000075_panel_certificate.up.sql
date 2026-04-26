-- M32 Step 1 — singleton panel_certificate row.
--
-- Tracks the panel hostname's TLS cert state: which one is currently
-- installed at /etc/jabali/tls/panel.{crt,key}, whether the admin has
-- enabled Let's Encrypt for it, the last attempt's outcome, and when
-- the reconciler should retry on failure. ADR-0066.
--
-- Singleton: id=1 enforced by CHECK so the table can't grow accidental
-- siblings. Empty state on first boot is created by the application
-- (not by this migration) per feedback_migration_data_seed_ordering.
CREATE TABLE panel_certificate (
  id              TINYINT UNSIGNED NOT NULL DEFAULT 1,
  hostname        VARCHAR(253) NOT NULL DEFAULT '',
  status          VARCHAR(32)  NOT NULL DEFAULT 'self_signed',
  cert_pem_path   VARCHAR(255) NOT NULL DEFAULT '/etc/jabali/tls/panel.crt',
  issued_at       DATETIME NULL,
  expires_at      DATETIME NULL,
  last_error      TEXT NULL,
  attempt_count   INT UNSIGNED NOT NULL DEFAULT 0,
  next_retry_at   DATETIME NULL,
  staging         TINYINT(1) NOT NULL DEFAULT 0,
  use_le          TINYINT(1) NOT NULL DEFAULT 0,
  updated_at      TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (id),
  CONSTRAINT panel_certificate_singleton CHECK (id = 1)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
