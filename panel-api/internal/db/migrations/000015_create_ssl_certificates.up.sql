-- Panel-managed SSL certificate lifecycle storage.
--
-- One row per hosted domain with SSL enabled. Tracks ACME issuance,
-- renewal, and expiration. Status field drives the renewal ticker logic.
-- Staging flag allows testing with Let's Encrypt staging environment.
CREATE TABLE ssl_certificates (
    id              CHAR(26)        NOT NULL PRIMARY KEY,
    domain_id       CHAR(26)        NOT NULL,
    -- Status lifecycle: pending → issuing → issued → renewing → issued
    -- Or: pending/issuing → failed (stays failed until manual retry or UI toggle)
    -- Or: issued → revoked (manual revocation)
    status          VARCHAR(32)     NOT NULL DEFAULT 'pending',
    -- When the cert was successfully issued (NULL until first issuance).
    issued_at       DATETIME(6)     NULL,
    -- When the cert expires (NULL until first issuance).
    -- Renewal ticker checks: status='issued' AND expires_at < NOW()+renewal_window.
    expires_at      DATETIME(6)     NULL,
    -- How many times this cert has been renewed (incremented on successful renewal).
    renewal_count   INT             NOT NULL DEFAULT 0,
    -- When the last renewal attempt completed (NULL until first renewal).
    last_renewed_at DATETIME(6)     NULL,
    -- Last error message if status='failed' (helps with debugging).
    last_error      TEXT            NULL,
    -- Whether this cert was issued from Let's Encrypt staging (used for testing).
    staging         TINYINT(1)      NOT NULL DEFAULT 0,
    -- Filesystem path to the full chain certificate (e.g. /etc/letsencrypt/live/example.com/fullchain.pem).
    cert_path       VARCHAR(512)    NULL,
    -- Filesystem path to the private key.
    key_path        VARCHAR(512)    NULL,
    created_at      DATETIME(6)     NOT NULL,
    updated_at      DATETIME(6)     NOT NULL,

    UNIQUE KEY uniq_ssl_cert_domain (domain_id),
    KEY ix_ssl_cert_expires (expires_at),
    CONSTRAINT fk_ssl_certs_domain FOREIGN KEY (domain_id)
        REFERENCES domains (id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
