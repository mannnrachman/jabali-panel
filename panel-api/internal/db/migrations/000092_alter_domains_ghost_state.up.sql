-- M38 Ghost Domain Detector: add the per-domain DNS-alignment state
-- columns the periodic detector populates.
--
-- ghost_state values:
--   unchecked - default; detector hasn't run on this row yet
--   ok        - public A record matches the server's expected IP
--   mismatch  - resolved to an IP we don't manage
--   nxdomain  - public DNS has no A record at all
--   error     - DNS lookup failed (timeout, SERVFAIL, etc); transient
--
-- ghost_checked_at: last time the detector ran on this row (NULL on
--                   freshly-inserted rows).
-- ghost_detail:     short human string explaining the latest state
--                   ("resolved 1.2.3.4 expected 5.6.7.8"); shown in
--                   the admin Domains badge tooltip + the M14 event
--                   body. NULL when state=ok or unchecked.

ALTER TABLE domains
    ADD COLUMN ghost_state ENUM('unchecked','ok','mismatch','nxdomain','error')
        NOT NULL DEFAULT 'unchecked',
    ADD COLUMN ghost_checked_at DATETIME(0) NULL,
    ADD COLUMN ghost_detail     VARCHAR(255) NULL,
    ADD INDEX idx_domains_ghost_state (ghost_state, ghost_checked_at);
