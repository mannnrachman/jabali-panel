-- M36: per-domain IP allow/deny lists.
--
-- Operators tag specific IPs/CIDRs as allow or deny on a per-domain
-- basis (cPanel/DA "Password Protect Directories" — IP-edition).
-- Reconciler writes a per-domain snippet to /etc/nginx/snippets/
-- jabali-acl-<domain-id>.conf which the vhost template includes
-- INSIDE the server block. nginx `allow` / `deny` directives apply.
--
-- Order matters in nginx. Lower priority = evaluated first. Operators
-- typically order specific allows above broader denies. Default
-- priority 0 → ties broken by created_at.

CREATE TABLE domain_ip_acls (
    id          CHAR(26)        NOT NULL PRIMARY KEY,
    domain_id   CHAR(26)        NOT NULL,
    cidr        VARCHAR(64)     NOT NULL,
    action      VARCHAR(8)      NOT NULL,           -- 'allow' | 'deny'
    priority    INT             NOT NULL DEFAULT 0,
    comment     VARCHAR(200)    NOT NULL DEFAULT '',
    created_at  DATETIME(6)     NOT NULL,
    INDEX idx_domain_ip_acls_domain (domain_id, priority),
    CONSTRAINT fk_domain_ip_acls_domain
        FOREIGN KEY (domain_id) REFERENCES domains (id)
        ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
