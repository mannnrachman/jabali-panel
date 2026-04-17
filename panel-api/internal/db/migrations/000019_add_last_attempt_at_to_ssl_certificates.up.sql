ALTER TABLE ssl_certificates
ADD COLUMN last_attempt_at DATETIME(6) NULL,
ADD KEY ix_ssl_cert_last_attempt (last_attempt_at);
