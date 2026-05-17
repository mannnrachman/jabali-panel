-- M32.x — panel_certificate per-kind rows (hostname + mail). ADR-0105.
--
-- Splits the singleton panel cert into two independent rows
-- discriminated by `kind` ∈ {hostname, mail}, each owning the full
-- ADR-0066 status state machine + its own cert path / retry. The
-- existing singleton row (id=1) backfills to kind='hostname' via the
-- column DEFAULT before the PK is repointed; the mail row is seeded
-- application-side by PanelCerts.EnsureDefault
-- (feedback_migration_data_seed_ordering — migration is schema only).
--
-- `id` is kept as a plain column (no longer PK / no singleton CHECK)
-- to avoid churning the model + existing tests; `kind` is the new PK.
ALTER TABLE panel_certificate DROP CHECK panel_certificate_singleton;
ALTER TABLE panel_certificate ADD COLUMN kind VARCHAR(16) NOT NULL DEFAULT 'hostname';
ALTER TABLE panel_certificate DROP PRIMARY KEY;
ALTER TABLE panel_certificate ADD PRIMARY KEY (kind);
