-- Revert M32.x panel_certificate per-kind split.
DELETE FROM panel_certificate WHERE kind = 'mail';
ALTER TABLE panel_certificate DROP PRIMARY KEY;
ALTER TABLE panel_certificate ADD PRIMARY KEY (id);
ALTER TABLE panel_certificate DROP COLUMN kind;
ALTER TABLE panel_certificate ADD CONSTRAINT panel_certificate_singleton CHECK (id = 1);
