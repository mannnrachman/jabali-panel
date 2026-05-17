-- M49: unified audit log (ADR-0106).
-- Schema only + a one-time fold-in BACKFILL of pre-existing rows from
-- db_admin_audit (created by 000135, runs before this). The backfill is
-- NOT seeding app-data: on a fresh install db_admin_audit is empty so
-- the INSERT...SELECT is a clean no-op (does not repeat the
-- feedback_migration_data_seed_ordering / 000057 scar — that broke
-- fresh installs by seeding FROM an app-populated table this migration
-- DEPENDS ON; here the source is optional/empty on fresh and the app
-- owns all forward data).
--
-- Open-risk #2 (M46 fold-in cutover) decided HERE: Step 0 is purely
-- ADDITIVE. db_admin_audit is LEFT INTACT — M46 code still writes to
-- it, and a view is not INSERTable without triggers, so converting it
-- now would break the live writer. The compatibility alias-view swap
-- happens in Step 3 (after the M46 write path is rerouted to the
-- recorder); M50 drops the view. Step 0 breaks no reader and no writer.

-- Append-only audit timeline. DB is the source of truth (ADR-0002);
-- one recorder writes via the M14 bus (ADR-0106), two server-scoped
-- views read (admin: all; /me/activity: subject_user_id = caller).
-- NO UPDATE/DELETE of rows anywhere — retention is a whole-partition
-- drop past N days (ADR-0106), never a selective delete. There is
-- deliberately no updated_at: an append-only row is never updated.
CREATE TABLE audit_events (
  id              CHAR(26)     NOT NULL PRIMARY KEY,                  -- ULID
  ts              DATETIME(6)  NOT NULL DEFAULT CURRENT_TIMESTAMP(6), -- event time (the only timestamp; append-only)
  actor_user_id   CHAR(26)     NULL,                                  -- users.id of who acted; NULL for system/automation/cli
  actor_kind      VARCHAR(16)  NOT NULL,                              -- 'user' | 'admin' | 'automation' | 'system' | 'cli'
  subject_user_id CHAR(26)     NULL,                                  -- whose account/resource; NULL = not user-scoped (invisible to /me/activity = safe-fail)
  action          VARCHAR(128) NOT NULL,                              -- normalized method+route template OR domain action; NEVER request bodies
  target_type     VARCHAR(32)  NOT NULL DEFAULT '',                   -- 'domain' | 'database' | 'user' | 'token' | ...
  target_id       VARCHAR(255) NOT NULL DEFAULT '',                   -- id/name; NEVER secrets
  result          VARCHAR(16)  NOT NULL,                              -- 'ok' | 'denied' | 'error'
  source_ip       VARCHAR(45)  NULL,                                  -- IPv4/IPv6 textual; NULL if N/A
  request_id      VARCHAR(64)  NULL,                                  -- ginctx RequestID for cross-log correlation
  prev_hash       CHAR(64)     NULL,                                  -- hex sha256 of prior chain row; NULL = pre-chain (folded/historical)
  row_hash        CHAR(64)     NULL,                                  -- hex sha256 of this row incl prev_hash; set by the single-writer chain consumer (Step 4)
  meta            JSON         NULL,                                  -- structured context only; NEVER secrets/bodies

  INDEX idx_audit_events_ts (ts),                                     -- admin time-range scans
  INDEX idx_audit_events_subject_ts (subject_user_id, ts),            -- /me/activity (plan-required composite)
  INDEX idx_audit_events_actor_ts (actor_user_id, ts),               -- "everything actor X did"
  INDEX idx_audit_events_action (action),
  INDEX idx_audit_events_request (request_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- One-time fold-in of historical M46 DB-admin audit rows. INSERT IGNORE
-- so a dirty-migration re-apply (RUNBOOK §1) cannot duplicate (id PK).
-- M46 db-admin ops are RequireAdmin and server-scoped (not a hosting
-- user's personal activity) → actor_kind='admin', subject_user_id=NULL
-- (correctly excluded from /me/activity). outcome 'forbidden' maps to
-- the audit_events 'denied' enum. prev_hash/row_hash stay NULL: these
-- rows predate the chain (pre-chain historical; the Step-4 consumer
-- only chains forward). engine + detail preserved losslessly in meta.
INSERT IGNORE INTO audit_events
  (id, ts, actor_user_id, actor_kind, subject_user_id, action,
   target_type, target_id, result, source_ip, request_id,
   prev_hash, row_hash, meta)
SELECT
  a.id,
  a.ts,
  a.actor_user_id,
  'admin',
  NULL,
  a.action,
  'database',
  a.target,
  CASE a.outcome WHEN 'forbidden' THEN 'denied'
                 WHEN 'error'     THEN 'error'
                 ELSE 'ok' END,
  NULL,
  NULL,
  NULL,
  NULL,
  JSON_OBJECT('engine', a.engine, 'detail', a.detail, 'folded_from', 'db_admin_audit')
FROM db_admin_audit a;
