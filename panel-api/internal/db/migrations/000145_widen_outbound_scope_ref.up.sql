-- M47 Wave 3 v2 — widen mail_outbound_policy.scope_ref.
--
-- v1 declared scope_ref CHAR(26) for ULID refs (users.id / domains.id)
-- but the Stalwart Expression grammar pinned in v2 needs the literal
-- sender address (varchar up to RFC 5321 max 320) or sender domain
-- string (up to 253). One column, widest fit. Existing v1 rows kept
-- (no data — v1 always-fire match meant per-user/domain throttles
-- behaved as global anyway).
ALTER TABLE mail_outbound_policy
  MODIFY COLUMN scope_ref VARCHAR(320) NULL;
