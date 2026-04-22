ALTER TABLE domains
  DROP FOREIGN KEY fk_domains_listen_ipv4,
  DROP FOREIGN KEY fk_domains_listen_ipv6,
  DROP COLUMN listen_ipv4_id,
  DROP COLUMN listen_ipv6_id;
