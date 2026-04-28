-- M30.1 follow-up: per-destination structured options (ADR-0078).
-- Started life as plain credentials_env (KEY=VALUE in /etc/jabali-panel/
-- restic-remotes/<id>.env), but SFTP wants host+user+port+path+key_path
-- as separate fields so the admin UI can render a typed form instead of
-- making the operator hand-build sftp.command strings.
--
-- Shape per-kind (UI-defined; opaque to the DB):
--
--   {"sftp": {"host":"...","user":"...","port":22,"path":"/repo",
--            "auth":"key"|"password","key_path":"/root/.ssh/id_rsa"}}
--
-- Other kinds may add their own sub-objects later (e.g. {"s3":{"region":...}}).
-- The wrapper only acts on keys it recognizes; unknown sub-objects are
-- ignored, which lets the UI evolve without DB migrations.

ALTER TABLE backup_destinations
  ADD COLUMN extra_options JSON NULL AFTER credentials_ref;
