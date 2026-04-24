-- M14 Step 1: notifications schema (channels + webhook retry state + history + webpush subscriptions).
--
-- Four tables total. Schema only — no data seeds. Per
-- feedback_migration_data_seed_ordering, migrations shape the DB; the app
-- seeds values (VAPID keypair into server_settings happens in
-- serve.go first-boot after this migration lands).
--
-- Integrity notes:
--   * notification_channels.config_json stores channel-specific blob
--     (url, bearer, hmac_secret, priority, tags, to/from email). The API
--     layer validates per-kind before persistence; the DB enforces JSON
--     well-formedness and nothing more.
--   * webhook_endpoints is the per-channel retry / last-error sidecar.
--     The name survived from the early blueprint ("webhook_endpoints")
--     even though it now holds state for every sending channel, not
--     just the generic webhook. One row per channel_id (1:1). PK on
--     channel_id + FK with ON DELETE CASCADE so deleting a channel
--     clears its retry state in one transaction.
--   * notification_history.channel_id is NULLable: in-app-bell-only
--     notifications don't have a channel (they're for the UI). FK still
--     set so that a channel deletion doesn't orphan its audit rows —
--     ON DELETE SET NULL.
--   * notification_history.user_id is NULLable: system-wide events
--     (disk-full) aren't addressed to a specific user; per-user
--     delivery (web-push fanout) writes a row per recipient.

CREATE TABLE notification_channels (
  id             CHAR(26) NOT NULL PRIMARY KEY,
  name           VARCHAR(120) NOT NULL,
  kind           ENUM('email','slack','discord','ntfy','webhook','webpush') NOT NULL,
  config_json    JSON NOT NULL,
  enabled        TINYINT(1) NOT NULL DEFAULT 1,
  created_at     DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
  updated_at     DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6),

  INDEX idx_notification_channels_kind_enabled (kind, enabled)
);

CREATE TABLE webhook_endpoints (
  channel_id            CHAR(26) NOT NULL PRIMARY KEY,
  last_success_at       DATETIME(6) NULL,
  last_error            TEXT NULL,
  consecutive_failures  INT NOT NULL DEFAULT 0,
  backoff_until         DATETIME(6) NULL,
  updated_at            DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6),

  CONSTRAINT fk_webhook_endpoints_channel
    FOREIGN KEY (channel_id)
    REFERENCES notification_channels(id)
    ON DELETE CASCADE
);

CREATE TABLE notification_history (
  id             CHAR(26) NOT NULL PRIMARY KEY,
  channel_id     CHAR(26) NULL,
  event_kind     VARCHAR(60) NOT NULL,
  severity       ENUM('info','warning','error','critical') NOT NULL,
  title          VARCHAR(200) NOT NULL,
  body           TEXT NOT NULL,
  deeplink       VARCHAR(500) NULL,
  outcome        ENUM('pending','sent','failed','skipped') NOT NULL DEFAULT 'pending',
  retry_count    INT NOT NULL DEFAULT 0,
  error_message  TEXT NULL,
  read_at        DATETIME(6) NULL,
  user_id        CHAR(26) NULL,
  created_at     DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
  updated_at     DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6),

  CONSTRAINT fk_notification_history_channel
    FOREIGN KEY (channel_id)
    REFERENCES notification_channels(id)
    ON DELETE SET NULL,
  CONSTRAINT fk_notification_history_user
    FOREIGN KEY (user_id)
    REFERENCES users(id)
    ON DELETE SET NULL,

  INDEX idx_notification_history_event_created (event_kind, created_at),
  INDEX idx_notification_history_user_read     (user_id, read_at)
);

CREATE TABLE webpush_subscriptions (
  id            CHAR(26) NOT NULL PRIMARY KEY,
  user_id       CHAR(26) NOT NULL,
  endpoint      VARCHAR(500) NOT NULL,
  p256dh        VARCHAR(200) NOT NULL,
  auth          VARCHAR(50) NOT NULL,
  user_agent    VARCHAR(300) NULL,
  created_at    DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
  last_used_at  DATETIME(6) NULL,

  CONSTRAINT fk_webpush_subscriptions_user
    FOREIGN KEY (user_id)
    REFERENCES users(id)
    ON DELETE CASCADE,

  UNIQUE KEY uq_webpush_subscriptions_endpoint (endpoint),
  INDEX idx_webpush_subscriptions_user (user_id)
);
