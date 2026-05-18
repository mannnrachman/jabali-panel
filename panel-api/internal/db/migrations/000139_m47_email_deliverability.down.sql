-- Revert M47 Wave 0 (ADR-0103).
DROP TABLE IF EXISTS tlsrpt_aggregate;
DROP TABLE IF EXISTS mta_sts_policy;
DROP TABLE IF EXISTS dmarc_aggregate;
DROP TABLE IF EXISTS mail_rbl_state;
DROP TABLE IF EXISTS mail_outbound_policy;
