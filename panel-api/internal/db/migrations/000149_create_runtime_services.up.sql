-- Runtime services table for non-PHP application processes (Phase 1).
-- Each row represents a managed application process (Node.js, Python,
-- Go binary, or Docker container) bound to exactly one domain. The
-- reconciler reads this table every tick and converges the host state
-- (systemd units, nginx vhosts, Docker containers) to match.
--
-- Port allocation: the panel-api assigns ports from a configurable
-- range (default 10000–60000). The UNIQUE on listen_port prevents
-- collisions between panel-managed runtimes. The PortAllocator service
-- also keeps an in-flight reservation set so concurrent allocations
-- don't pick the same port before either is persisted.
--
-- env_vars is stored as JSON so the agent can write it verbatim to
-- the systemd EnvironmentFile without parsing. NULL means no extra
-- env (the unit still gets HOME, USER, PATH from the slice).

CREATE TABLE runtime_services (
  id            CHAR(26)     NOT NULL,
  domain_id     CHAR(26)     NOT NULL,
  user_id       CHAR(26)     NOT NULL,
  runtime       VARCHAR(16)  NOT NULL COMMENT 'nodejs, python, go, docker',
  version       VARCHAR(16)  NOT NULL DEFAULT '' COMMENT 'e.g. 20, 3.12, 1.22',
  entry_point   VARCHAR(512) NOT NULL COMMENT 'e.g. server.js, main.py, ./myapp',
  listen_port   INT UNSIGNED NOT NULL COMMENT 'port allocated by panel for reverse proxy',
  env_vars      JSON         DEFAULT NULL,
  status        VARCHAR(16)  NOT NULL DEFAULT 'pending' COMMENT 'pending, deploying, running, stopped, failed',
  last_error    TEXT         DEFAULT NULL,
  pid_file      VARCHAR(255) NOT NULL DEFAULT '',
  systemd_unit  VARCHAR(255) NOT NULL DEFAULT '' COMMENT 'e.g. jabali-rt-username-example-com.service',
  created_at    DATETIME(6)  NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
  updated_at    DATETIME(6)  NOT NULL DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6),

  PRIMARY KEY (id),
  UNIQUE KEY ux_runtime_services_domain (domain_id),
  UNIQUE KEY ux_runtime_services_port (listen_port),
  INDEX ix_runtime_services_user (user_id),
  INDEX ix_runtime_services_status (status),

  CONSTRAINT fk_runtime_services_domain FOREIGN KEY (domain_id)
    REFERENCES domains(id) ON DELETE CASCADE,
  CONSTRAINT fk_runtime_services_user FOREIGN KEY (user_id)
    REFERENCES users(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
