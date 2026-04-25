-- Admin-controlled cap for file-manager uploads. Enforced at panel-api
-- (http.MaxBytesReader middleware + per-request size check). Default
-- 1024 MB matches the FileManagerPage client-side ceiling and the
-- nginx vhost client_max_body_size; admins can lower it from the
-- Server Settings page when bandwidth or storage warrants.
ALTER TABLE server_settings
  ADD COLUMN upload_max_size_mb INT UNSIGNED NOT NULL DEFAULT 1024;
