-- M34: per-user PHP-FPM egress firewall policy table.
--
-- One row per Linux user. State drives the nftables chain rendered by
-- the reconciler (panel-api/internal/reconciler/user_egress_reconciler.go):
--
--   off       — no chain emitted; packet falls through to default accept
--   learning  — chain matches default allowlist + extras, would-drops are
--               logged + counter-bumped + accepted (NOT dropped)
--   enforced  — chain matches default allowlist + extras, would-drops are
--               counter-bumped + dropped (kernel-level, before any
--               PHP-extension exec or LD_PRELOAD can phone home)
--
-- allowed_extra is the per-user override list:
--   [{"cidr":"203.0.113.0/24","port":443,"protocol":"tcp","comment":"..."}]
-- The global default allowlist (loopback4/6 + TCP 53/80/443/587/465/25 +
-- UDP 53) lives in server_settings (added in M34 Step 6 mig 000102).
--
-- drop_count_24h is denormalised from nft counters every reconciler tick
-- so admin UIs do not need to shell into nft on every page load.
--
-- learning_started_at anchors the 7-day LEARNING -> ENFORCED auto-flip
-- timer (Step 8). NULL on rows that are not in 'learning' state. The
-- repository EnsureDefault sets it when state transitions to learning;
-- migrations stay schema-only (memory: feedback_migration_data_seed_ordering).

CREATE TABLE IF NOT EXISTS user_egress_policies (
  user_id              VARCHAR(26)  NOT NULL,
  state                ENUM('off','learning','enforced') NOT NULL DEFAULT 'enforced',
  allowed_extra        JSON         NOT NULL,
  drop_count_24h       BIGINT UNSIGNED NOT NULL DEFAULT 0,
  drop_count_at        TIMESTAMP    NULL,
  learning_started_at  TIMESTAMP    NULL,
  updated_at           TIMESTAMP    NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  updated_by           VARCHAR(26)  NULL,
  PRIMARY KEY (user_id),
  KEY idx_user_egress_state (state),
  KEY idx_user_egress_drops (drop_count_24h),
  CONSTRAINT fk_user_egress_user FOREIGN KEY (user_id)
    REFERENCES users(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
