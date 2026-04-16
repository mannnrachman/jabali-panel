ALTER TABLE ssl_certificates
ADD COLUMN next_retry_at DATETIME(6) NULL,
ADD COLUMN retry_count INT NOT NULL DEFAULT 0,
ADD KEY ix_ssl_cert_next_retry (next_retry_at);
