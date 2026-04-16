-- Add redirect configuration to domains.
--
-- redirect_all_to / redirect_all_type: whole-domain redirect. When
-- redirect_all_to is set, all requests to this domain are redirected to
-- that URL (ignoring the docroot entirely). Type is the HTTP status code
-- ("301", "302", "307", "308") as a short string so we don't need a
-- separate enum table.
--
-- page_redirects: JSON array of per-path redirects. Schema per element:
--   { "source": "/old", "destination": "https://...", "type": "301" }
-- MariaDB stores JSON as LONGTEXT under the hood; that's fine for our
-- sizes and we validate structure in application code.
ALTER TABLE domains
    ADD COLUMN redirect_all_to   VARCHAR(2048) NULL AFTER nginx_custom_directives,
    ADD COLUMN redirect_all_type VARCHAR(8)    NULL AFTER redirect_all_to,
    ADD COLUMN page_redirects    JSON          NULL AFTER redirect_all_type;
