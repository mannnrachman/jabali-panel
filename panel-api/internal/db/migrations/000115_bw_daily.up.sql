-- M13.1: per-domain daily bandwidth ledger.
--
-- Populated by the agent's bandwidth.scan_day handler at 00:30 daily,
-- which runs goaccess against /var/log/nginx/<domain>-access.log.1
-- (yesterday's log; logrotate's delaycompress keeps it uncompressed
-- for the first day) and harvests bytes_total + requests_total per
-- log file → upserted into this table.
--
-- The day column is the date the traffic occurred (YYYY-MM-DD UTC),
-- NOT the date the row was inserted. Idempotent: re-running the
-- handler against the same .log.1 produces the same numbers, so the
-- ON DUPLICATE KEY clause overwrites without history loss.
--
-- Aggregation queries (per-month totals on the Users + Domains pages)
-- group by (domain_id, YEAR(day), MONTH(day)) and sum bytes_total.

CREATE TABLE bw_daily (
    domain_id      VARCHAR(26) NOT NULL,
    day            DATE        NOT NULL,
    bytes_total    BIGINT UNSIGNED NOT NULL DEFAULT 0,
    requests_total BIGINT UNSIGNED NOT NULL DEFAULT 0,
    updated_at     DATETIME(6) NOT NULL,
    PRIMARY KEY (domain_id, day),
    CONSTRAINT fk_bw_daily_domain
        FOREIGN KEY (domain_id) REFERENCES domains (id)
        ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE INDEX idx_bw_daily_day ON bw_daily (day);
