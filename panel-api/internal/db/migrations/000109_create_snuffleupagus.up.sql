-- M41 (ADR-0088) Snuffleupagus PHP-hardening tables.
--
-- snuffleupagus_state          singleton row carrying the operator-selected
--                              mode + last apply metadata
-- snuffleupagus_rule_overrides admin per-rule kill switch (false-positive
--                              triage)
-- snuffleupagus_incidents      append-only event log fed by the agent's
--                              journalctl tail of sp.log_facility=local6

CREATE TABLE snuffleupagus_state (
  id                  TINYINT      NOT NULL DEFAULT 1 PRIMARY KEY,
  mode                ENUM('off','simulation','enforce') NOT NULL DEFAULT 'off',
  last_applied_at     DATETIME(6)  NULL,
  last_applied_sha256 CHAR(64)     NULL,
  CONSTRAINT chk_snuf_state_singleton CHECK (id = 1)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

INSERT INTO snuffleupagus_state (id, mode) VALUES (1, 'off');

CREATE TABLE snuffleupagus_rule_overrides (
  rule_name      VARCHAR(128) NOT NULL PRIMARY KEY,
  enabled        TINYINT(1)   NOT NULL DEFAULT 1,
  reason         VARCHAR(512) NULL,
  set_by_user_id CHAR(26)     NULL,
  set_at         DATETIME(6)  NOT NULL DEFAULT CURRENT_TIMESTAMP(6)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE snuffleupagus_incidents (
  id          BIGINT        NOT NULL AUTO_INCREMENT PRIMARY KEY,
  ts          DATETIME(6)   NOT NULL,
  rule_name   VARCHAR(128)  NOT NULL,
  action      ENUM('log','block','simulated_block') NOT NULL,
  source_ip   VARBINARY(16) NULL,
  request_uri VARCHAR(2048) NULL,
  php_version VARCHAR(8)    NULL,
  domain_id   CHAR(26)      NULL,
  raw         TEXT          NULL,
  KEY ix_snuf_inc_ts (ts),
  KEY ix_snuf_inc_rule (rule_name),
  KEY ix_snuf_inc_domain (domain_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
