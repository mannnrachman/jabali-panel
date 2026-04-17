ALTER TABLE domains
  DROP FOREIGN KEY fk_domain_php_pool,
  DROP COLUMN php_pool_id;
