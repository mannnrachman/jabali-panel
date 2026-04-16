-- Structured rule-builder input for the Nginx Directives modal.
--
-- JSON array of typed rule objects with a discriminated `type` field:
--   { "type": "custom_header", "name": "...", "value": "...", "always": true }
--   { "type": "rewrite", "pattern": "...", "replacement": "...", "flag": "last" }
--   { "type": "proxy_pass", "path": "/api/", "target": "http://localhost:3000" }
--   { "type": "ip_access", "path": "/admin/", "mode": "allow_list", "ips": [...] }
--   { "type": "php_setting", "name": "memory_limit", "value": "512M" }
--   { "type": "max_upload_size", "size": "100M" }
--
-- Lives alongside nginx_custom_directives (raw snippet field). Rules
-- compile to nginx directives server-side via internal/nginxrules.
ALTER TABLE domains
    ADD COLUMN nginx_rules JSON NULL AFTER index_priority;
