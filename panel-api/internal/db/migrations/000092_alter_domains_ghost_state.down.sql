ALTER TABLE domains
    DROP INDEX idx_domains_ghost_state,
    DROP COLUMN ghost_state,
    DROP COLUMN ghost_checked_at,
    DROP COLUMN ghost_detail;
