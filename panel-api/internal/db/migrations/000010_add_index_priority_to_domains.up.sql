-- Per-domain default index file priority.
--
-- Controls the nginx `index` directive. Enum values map in the agent:
--   html_first → index index.html index.php;
--   php_first  → index index.php index.html;
--   html_only  → index index.html;
--   php_only   → index index.php;
--   full       → index index.php index.html index.htm;
--
-- Defaulting existing rows to html_first preserves the previous hardcoded
-- behaviour so in-flight domains don't change which file serves.
ALTER TABLE domains
    ADD COLUMN index_priority VARCHAR(32) NOT NULL DEFAULT 'html_first' AFTER page_redirects;
