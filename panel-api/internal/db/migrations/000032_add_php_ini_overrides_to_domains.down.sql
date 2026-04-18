ALTER TABLE domains
  DROP COLUMN php_memory_limit,
  DROP COLUMN php_upload_max_filesize,
  DROP COLUMN php_post_max_size,
  DROP COLUMN php_max_input_vars,
  DROP COLUMN php_max_execution_time,
  DROP COLUMN php_max_input_time;
