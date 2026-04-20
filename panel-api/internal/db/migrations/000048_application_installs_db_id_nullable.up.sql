-- M19 added flat-file apps (DokuWiki, Grav, Backdrop, ...) that don't
-- need a MariaDB database, but db_id stayed NOT NULL with an FK to
-- databases(id). The service pipeline passes "" for chain.DBID on
-- RequiresDB=false apps, which fails the FK at INSERT time.
--
-- Make db_id nullable + relax the FK (still RESTRICT on actual rows
-- pointing at a database). RequiresDB=true apps continue to insert a
-- valid db_id; RequiresDB=false apps insert NULL.
ALTER TABLE `application_installs`
  MODIFY COLUMN `db_id` CHAR(26) NULL;
