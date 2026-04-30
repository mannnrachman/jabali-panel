-- M39 — drop Tetragon state. Tetragon was added in M33 (mig 000081)
-- and removed in M39 (2026-04-30). See ADR-0072 amendment + ADR-0085.
--
-- IF EXISTS guards are defensive: M33 created the table + column
-- unconditionally, but a host that never ran the M33 migrations (eg.
-- restored from a pre-M33 backup) should not crash here.

DROP TABLE IF EXISTS tetragon_policy_state;

ALTER TABLE malware_settings DROP COLUMN IF EXISTS tetragon_enabled;
