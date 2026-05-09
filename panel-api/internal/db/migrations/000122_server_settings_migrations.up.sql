-- M35: server-wide migration knobs.
--
-- Default off — operators flip on when their team is ready. A green
-- migration_jobs.state='done' on a smoke account is the typical pre-
-- production gate.
--
-- Concurrent-per-source caps parallel transfers from one source host
-- so a single operator import doesn't saturate the source's SSH
-- channel limit. IMAP folder-concurrency is a separate cap because
-- imapsync's per-folder workers are independent.

ALTER TABLE server_settings
    ADD COLUMN migrations_enabled                 TINYINT(1) NOT NULL DEFAULT 0,
    ADD COLUMN migrations_concurrent_per_source   INT        NOT NULL DEFAULT 2,
    ADD COLUMN migrations_imap_concurrent_folders INT        NOT NULL DEFAULT 4;
