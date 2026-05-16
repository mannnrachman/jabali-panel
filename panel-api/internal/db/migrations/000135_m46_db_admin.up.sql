-- M46: database server admin ops (ADR-0097..0100).
-- Schema only — tuning defaults are seeded by the app on first read,
-- never by this migration (feedback_migration_data_seed_ordering:
-- migration 000057 bricked fresh installs by seeding from an
-- app-populated table; migrations are schema, the app owns data).

-- Curated config tuner state. DB is the source of truth; the
-- reconciler renders the on-disk drop-in / postgresql.auto.conf from
-- these rows every tick and reloads on divergence (ADR-0098).
CREATE TABLE db_tuning_settings (
  id          CHAR(26)     NOT NULL PRIMARY KEY,        -- ULID
  engine      VARCHAR(16)  NOT NULL,                    -- 'mariadb' | 'postgres'
  param       VARCHAR(64)  NOT NULL,                    -- allowlisted key (internal/dbtuning)
  value       VARCHAR(255) NOT NULL,                    -- rendered as-is into config; allowlist-validated upstream
  applied_at  DATETIME(6)  NULL,                        -- last successful agent apply (NULL = pending)
  applied_by  CHAR(26)     NULL,                        -- users.id of the admin who applied
  created_at  DATETIME(6)  NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
  updated_at  DATETIME(6)  NOT NULL DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6),

  UNIQUE KEY uniq_db_tuning_engine_param (engine, param),
  INDEX idx_db_tuning_engine (engine)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- Long-running maintenance jobs. Survives `systemctl restart
-- jabali-panel` so a mid-run page reload doesn't 404 its own job,
-- and powers the one-job-per-engine 409 concurrency guard (ADR-0100).
CREATE TABLE db_admin_jobs (
  id            CHAR(26)     NOT NULL PRIMARY KEY,       -- ULID
  engine        VARCHAR(16)  NOT NULL,                   -- 'mariadb' | 'postgres'
  kind          VARCHAR(32)  NOT NULL,                   -- 'maintenance'
  scope         VARCHAR(64)  NOT NULL,                   -- 'all' | '<db name>'
  status        VARCHAR(16)  NOT NULL,                   -- 'running' | 'ok' | 'error'
  summary       TEXT         NULL,                       -- human-readable result / error
  actor_user_id CHAR(26)     NOT NULL,                   -- users.id
  started_at    DATETIME(6)  NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
  finished_at   DATETIME(6)  NULL,

  INDEX idx_db_admin_jobs_engine_status (engine, status),
  INDEX idx_db_admin_jobs_started_at (started_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- Tamper-evident audit of every privileged DB admin action (root pw
-- rotate, config apply, admin SSO mint, KILL/terminate). Pruned
-- >180d by reconciler housekeeping (see runbook).
CREATE TABLE db_admin_audit (
  id            CHAR(26)     NOT NULL PRIMARY KEY,       -- ULID
  ts            DATETIME(6)  NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
  actor_user_id CHAR(26)     NOT NULL,                   -- users.id
  engine        VARCHAR(16)  NOT NULL,                   -- 'mariadb' | 'postgres'
  action        VARCHAR(64)  NOT NULL,                   -- e.g. 'root_password.rotate','config.apply','sso.admin','process.kill'
  target        VARCHAR(255) NOT NULL DEFAULT '',        -- pid / db name / '' where N/A
  outcome       VARCHAR(32)  NOT NULL,                   -- 'ok' | 'error' | 'forbidden'
  detail        VARCHAR(255) NOT NULL DEFAULT '',        -- short context; NEVER secrets

  INDEX idx_db_admin_audit_ts (ts),
  INDEX idx_db_admin_audit_actor (actor_user_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
