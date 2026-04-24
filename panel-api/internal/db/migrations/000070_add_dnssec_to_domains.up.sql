-- DNSSEC intent + key cache (ADR-0057).
-- Columns added to domains express operator intent. `domain_dnssec_keys` caches
-- whatever `pdnsutil show-zone` reports for the latest reconcile tick; never
-- stores private key material.

ALTER TABLE domains
  ADD COLUMN dnssec_enabled TINYINT(1) NOT NULL DEFAULT 0,
  ADD COLUMN dnssec_enabled_at DATETIME(6) NULL;

CREATE TABLE domain_dnssec_keys (
  domain_id    CHAR(26)               NOT NULL,
  key_tag      INT                    NOT NULL,
  key_type     ENUM('KSK','ZSK','CSK') NOT NULL,
  algorithm    TINYINT UNSIGNED       NOT NULL,
  public_key   TEXT                   NOT NULL,
  active       TINYINT(1)             NOT NULL DEFAULT 1,
  observed_at  DATETIME(6)            NOT NULL,
  PRIMARY KEY (domain_id, key_tag),
  CONSTRAINT fk_dnssec_keys_domain
    FOREIGN KEY (domain_id) REFERENCES domains (id) ON DELETE CASCADE
) ENGINE=InnoDB;
