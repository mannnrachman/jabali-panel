ALTER TABLE ssl_certificates
DROP KEY ix_ssl_cert_last_attempt,
DROP COLUMN last_attempt_at;
