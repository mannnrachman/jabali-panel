ALTER TABLE domains
  ADD COLUMN php_memory_limit         VARCHAR(16) NULL,
  ADD COLUMN php_upload_max_filesize  VARCHAR(16) NULL,
  ADD COLUMN php_post_max_size        VARCHAR(16) NULL,
  ADD COLUMN php_max_input_vars       INT UNSIGNED NULL,
  ADD COLUMN php_max_execution_time   INT UNSIGNED NULL,
  ADD COLUMN php_max_input_time       INT UNSIGNED NULL;
