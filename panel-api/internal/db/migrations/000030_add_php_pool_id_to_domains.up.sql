ALTER TABLE domains
  ADD COLUMN php_pool_id CHAR(26) NULL,
  ADD CONSTRAINT fk_domain_php_pool FOREIGN KEY (php_pool_id) REFERENCES php_pools(id) ON DELETE SET NULL;
