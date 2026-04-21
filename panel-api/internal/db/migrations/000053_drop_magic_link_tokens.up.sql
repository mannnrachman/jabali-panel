-- M22 rework (ADR-0040). The self-deleting sso-file design has no
-- panel-side token store: the 256-bit nonce in the filename is the
-- capability and single-use enforcement happens on the WordPress
-- filesystem (flock + unlink). The magic_link_tokens table from
-- 000052 is no longer read or written by panel-api, so drop it.
--
-- DROP IF EXISTS makes this idempotent — a fresh install that never
-- ran 000052 (because it's pre-M22) is fine; an install that did
-- run 000052 has the table dropped.
DROP TABLE IF EXISTS magic_link_tokens;
