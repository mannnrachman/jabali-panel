-- `databases` is a MariaDB built-in name (matches SHOW DATABASES) and
-- `engine` / `collation` are reserved-word-adjacent identifiers. Quoting
-- every identifier here is the safe move — MariaDB will parse an
-- unquoted CREATE TABLE databases with some versions and reject it with
-- others, and the error message when it fails is unhelpful. Keeping the
-- whole schema backticked also survives a future sql_mode=ANSI_QUOTES
-- change without silently breaking.
CREATE TABLE `databases` (
  `id`         CHAR(26) NOT NULL PRIMARY KEY,
  `user_id`    CHAR(26) NOT NULL,
  `name`       VARCHAR(64) NOT NULL,
  `engine`     ENUM('mariadb','postgres') NOT NULL DEFAULT 'mariadb',
  `charset`    VARCHAR(32) NOT NULL DEFAULT 'utf8mb4',
  `collation`  VARCHAR(32) NOT NULL DEFAULT 'utf8mb4_unicode_ci',
  `created_at` TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
  `updated_at` TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,

  CONSTRAINT `fk_databases_user_id` FOREIGN KEY (`user_id`) REFERENCES `users`(`id`) ON DELETE RESTRICT,
  UNIQUE KEY `uniq_user_name` (`user_id`, `name`),
  KEY `idx_user_id` (`user_id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
