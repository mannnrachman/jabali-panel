-- M47: email deliverability suite (ADR-0103). Schema only — all
-- forward data is app-owned (feedback_migration_data_seed_ordering;
-- no seeding from app-populated tables). No hard FKs (DB-as-truth,
-- ADR-0002 — refs are by-value ULID/domain like the rest of jabali);
-- utf8mb4_unicode_ci per feedback_mariadb_collation_fk.

-- Per-user / per-domain outbound rate caps. reconciler converges
-- Stalwart rate-limit config; agent mail.throttle.apply (Step 3).
CREATE TABLE mail_outbound_policy (
  id           CHAR(26)     NOT NULL PRIMARY KEY,            -- ULID
  scope        VARCHAR(16)  NOT NULL,                        -- 'user' | 'domain' | 'global'
  scope_ref    CHAR(26)     NULL,                            -- users.id / domains.id ULID; NULL for global
  max_per_hour INT UNSIGNED NOT NULL DEFAULT 0,              -- 0 = unlimited
  max_per_day  INT UNSIGNED NOT NULL DEFAULT 0,
  enabled      TINYINT(1)   NOT NULL DEFAULT 1,
  created_at   DATETIME(6)  NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
  updated_at   DATETIME(6)  NOT NULL DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6),
  UNIQUE KEY uq_outbound_scope (scope, scope_ref)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- RBL/blacklist state for the server's public IP(s). Step 5; hard
-- cached (TTL >=1h), polled every 4-6h, curated free RBL set only.
CREATE TABLE mail_rbl_state (
  id         CHAR(26)    NOT NULL PRIMARY KEY,
  ip         VARCHAR(45) NOT NULL,                            -- v4/v6
  rbl        VARCHAR(64) NOT NULL,                            -- e.g. 'zen.spamhaus.org'
  listed     TINYINT(1)  NOT NULL DEFAULT 0,
  detail     TEXT        NULL,                                -- TXT answer / reason
  checked_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
  created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
  updated_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6),
  UNIQUE KEY uq_rbl (ip, rbl)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- DMARC aggregate (rua) ingest. RETENTION: 90 days, pruned app-side
-- by a reconciler/timer (Step 6) — append-only, never UPDATEd; this
-- table grows reporter x domain x window x source and MUST be reaped.
CREATE TABLE dmarc_aggregate (
  id           CHAR(26)     NOT NULL PRIMARY KEY,
  domain       VARCHAR(253) NOT NULL,
  reporter     VARCHAR(253) NOT NULL,
  window_start DATETIME     NOT NULL,
  window_end   DATETIME     NOT NULL,
  source_ip    VARCHAR(45)  NOT NULL,
  disposition  VARCHAR(16)  NOT NULL,                         -- none|quarantine|reject
  dkim         VARCHAR(8)   NOT NULL,                          -- pass|fail
  spf          VARCHAR(8)   NOT NULL,
  cnt          INT UNSIGNED NOT NULL DEFAULT 0,
  created_at   DATETIME(6)  NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
  KEY idx_dmarc_domain_window (domain, window_end)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- Per-domain MTA-STS policy. Step 7b state machine; reconciler emits
-- DNS (M15 PDNS) + policy file (nginx). Default mode none until 7a
-- scaffolding (DNS+vhost+cert) verifies. max_age small first.
CREATE TABLE mta_sts_policy (
  domain     VARCHAR(253) NOT NULL PRIMARY KEY,
  mode       VARCHAR(8)   NOT NULL DEFAULT 'none',            -- none|testing|enforce
  max_age    INT UNSIGNED NOT NULL DEFAULT 86400,             -- RFC 8461; start small before 604800+
  policy_id  VARCHAR(64)  NOT NULL DEFAULT '',                -- _mta-sts TXT id= token
  updated_at DATETIME(6)  NOT NULL DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- TLS-RPT (rua) ingest. RETENTION: 90 days, pruned app-side (Step 8)
-- — same unbounded-growth reaping discipline as dmarc_aggregate.
CREATE TABLE tlsrpt_aggregate (
  id            CHAR(26)     NOT NULL PRIMARY KEY,
  domain        VARCHAR(253) NOT NULL,
  reporter      VARCHAR(253) NOT NULL,
  window_start  DATETIME     NOT NULL,
  window_end    DATETIME     NOT NULL,
  result_type   VARCHAR(48)  NOT NULL DEFAULT '',             -- e.g. starttls-not-supported
  success_count INT UNSIGNED NOT NULL DEFAULT 0,
  failure_count INT UNSIGNED NOT NULL DEFAULT 0,
  created_at    DATETIME(6)  NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
  KEY idx_tlsrpt_domain_window (domain, window_end)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
