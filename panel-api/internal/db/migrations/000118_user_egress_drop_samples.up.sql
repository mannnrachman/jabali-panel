-- M34 deep stats — per-tick egress drop samples for the 24h sparkline.
--
-- The reconciler's user.egress.read_counters tick already computes
-- per-user delta (current packets - last_seen). Persist that delta
-- as a row so the admin Egress card can render a 24h sparkline of
-- drops without standing up a metrics backend.
--
-- Retention: prune rows older than 25h on every tick (one-hour
-- buffer past the rendering window). At 60s tick × 24h = 1440 rows
-- per user — cheap.

CREATE TABLE user_egress_drop_samples (
    user_id     CHAR(26)        NOT NULL,
    at          DATETIME(6)     NOT NULL,
    drops       BIGINT UNSIGNED NOT NULL DEFAULT 0,
    PRIMARY KEY (user_id, at),
    INDEX idx_user_egress_drop_samples_at (at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
