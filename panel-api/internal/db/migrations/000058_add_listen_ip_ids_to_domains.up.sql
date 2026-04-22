-- M24 Step 1: domain → managed_ip binding columns.
--
-- listen_ipv*_id are nullable FKs into managed_ips. NULL means "use
-- server default" (managed_ips.is_default for that family).
--
-- ON DELETE RESTRICT: deleting an IP referenced by any domain returns
-- 409 from the API with the affected-domains list. No silent cascading
-- to NULL — operator must explicitly reassign first (per ADR-0048 and
-- risk R3).
ALTER TABLE domains
  ADD COLUMN listen_ipv4_id BIGINT UNSIGNED NULL,
  ADD COLUMN listen_ipv6_id BIGINT UNSIGNED NULL,
  ADD CONSTRAINT fk_domains_listen_ipv4 FOREIGN KEY (listen_ipv4_id) REFERENCES managed_ips(id) ON DELETE RESTRICT,
  ADD CONSTRAINT fk_domains_listen_ipv6 FOREIGN KEY (listen_ipv6_id) REFERENCES managed_ips(id) ON DELETE RESTRICT;
