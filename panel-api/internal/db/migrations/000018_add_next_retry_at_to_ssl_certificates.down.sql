ALTER TABLE ssl_certificates
DROP KEY ix_ssl_cert_next_retry,
DROP COLUMN next_retry_at,
DROP COLUMN retry_count;
