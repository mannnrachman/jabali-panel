ALTER TABLE migration_jobs ADD UNIQUE KEY uq_migration_source (source_host, source_user, source_kind);
