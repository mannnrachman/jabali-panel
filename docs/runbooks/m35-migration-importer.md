# M35 Migration Importer Runbook

**Scope**: operator-facing reference for the M35.1 / M35.2 migration
GUI shipped via ADR-0094 + ADR-0095.

---

## TL;DR — three flows

| When | Entry point | Endpoint shape |
|---|---|---|
| Single cPanel / DA / Hestia account, you already know host+user+password | List page → **New migration** | POST `/admin/migrations` + per-step driver |
| Bulk WHM, you have account list in hand | List page → **Bulk WHM (paste)** | POST `/admin/migrations/bulk` |
| Anything else (discover-driven, WHM with picker) | List page → **Wizard** | Draft → PATCH → discover → bulk/submit |

---

## URLs

| Path | Purpose |
|---|---|
| `/jabali-admin/migrations` | List view (stats + batch column + per-row actions) |
| `/jabali-admin/migrations/<id>` | Per-job detail (SSE-driven stages + retry buttons) |
| `/jabali-admin/migrations?wizard=<id>` | Resume a draft wizard (set automatically) |

---

## Wizard step → endpoint map

| Step | Action | Endpoint |
|---|---|---|
| 1 — Source | Pick `cpanel` / `whm_pkgacct` / `directadmin` / `hestiacp` | `POST /admin/migrations {state:"draft"}` |
| 2 — Connection | Fill host + admin user + password / SSH key | `PATCH /admin/migrations/:id` + `POST /admin/migrations/:id/secrets` |
| 3 — Accounts (WHM only) | Multi-select from live `whmapi1 listaccts` | `GET /admin/migrations/:id/discover-accounts` |
| 4 — Review & submit | WHM: bulk create. Other: submit draft. | `POST /admin/migrations/bulk` OR `POST /admin/migrations/:id/submit` |

---

## Backend endpoints

### Job lifecycle
```
POST   /admin/migrations                 create (state=pending OR draft)
PATCH  /admin/migrations/:id             update draft (state=draft only)
POST   /admin/migrations/:id/submit      draft → pending
POST   /admin/migrations/:id/secrets     write per-job .env
POST   /admin/migrations/:id/pull-source kick off SSH pull
POST   /admin/migrations/:id/import      kick off import runner
POST   /admin/migrations/:id/tarball     upload pre-built cpmove tar
GET    /admin/migrations/:id             one job + recent stages
GET    /admin/migrations/:id/stages      full stage timeline
GET    /admin/migrations/:id/stream      SSE — live snapshots (2s)
DELETE /admin/migrations/:id             soft-revoke
POST   /admin/migrations/:id/destroy     hard-delete row + secret + dir
POST   /admin/migrations/:id/retry       resume retry
POST   /admin/migrations/:id/retry?from_scratch=true   nuke stages + retry
```

### Bulk (WHM)
```
POST   /admin/migrations/bulk            N drafts sharing batch_id
DELETE /admin/migrations/batches/:id     cancel every non-terminal in batch
```

### Discovery (live source contact via stored secret)
```
GET /admin/migrations/:id/discover-accounts          list source accounts
GET /admin/migrations/:id/account-size/:user         live du -sh probe (caches 24h)
GET /admin/migrations/discover-accounts/:host/:user/size   cache-only lookup (503 if cold)
```

---

## SSRF guard (ADR-0095 decision 8)

Every outbound dial from `internal/migrate/` (discover, secrets push,
pull-source, account-size probe) is gated by
`migrate.ValidateHost` + `Dialer.Control`. Default-deny ranges:

- 127.0.0.0/8 loopback
- 169.254.0.0/16 link-local (AWS/GCP metadata)
- 10.0.0.0/8 + 172.16.0.0/12 + 192.168.0.0/16 RFC1918
- ::1, fc00::/7, fe80::/10 IPv6 equivalents

Override:
```
mysql -e "UPDATE server_settings SET migration_allow_private_hosts=1"
```
Restart panel-api after the flip. The DNS-rebinding-protection
peer-IP check still applies — connections to public hostnames whose
record was swapped for an internal IP between resolve and dial still
fail.

---

## Retry semantics (ADR-0095 decision 7)

| Action | What runs | When to use |
|---|---|---|
| **Retry (resume)** | Re-runs stages whose state ≠ done. DB restore now wraps in `DROP DATABASE IF EXISTS ; CREATE DATABASE ;` so partial DB imports are idempotent. | Transient infra blip — SSH dropout, source rebooted, disk full |
| **Retry from scratch** | Wipes every stage row + re-runs from analyze. Existing `extracted/` is overwritten on next pull. | Source data changed; you re-uploaded a different cpmove tarball; mailbox-only sync re-do |
| **Destroy** | Hard-delete job row + secret + `/var/lib/jabali-migrations/<id>/`. | Job is unsalvageable + you want the (host, user, kind) unique slot back |

---

## Cancel batch

In the list view, click the purple **batch_id** tag (last 6 ULID
chars). Popconfirm asks for confirmation; OK fires
`DELETE /admin/migrations/batches/:id`. Backend transitions every
non-terminal job in the batch to `cancelled`. Terminal jobs (done /
failed / cancelled) stay untouched.

---

## Draft reaper

`jabali migrate reap-secrets` (run daily by
`jabali-migration-secrets-reap.timer` at 04:30 UTC) does TWO things:

1. Deletes `/etc/jabali-panel/migration-secrets/<id>.env` for any
   job in a terminal state.
2. Hard-deletes any `state=draft` job whose `updated_at` is older
   than 24h. Drafts hold no secrets (those are written at Step 2,
   which flips the row to a non-draft state) so deletion is safe.

Operator can `--dry-run` to preview without deleting.

---

## Where things live

| Component | Path |
|---|---|
| Frontend | `panel-ui/src/shells/admin/migrations/` |
| Wizard | `panel-ui/src/shells/admin/migrations/CreateMigrationWizard.tsx` |
| SSE hook | `panel-ui/src/hooks/useMigrationStream.ts` |
| Backend routes | `panel-api/internal/api/admin_migrations.go` |
| Discoverer registry | `panel-api/internal/migrate/registry.go` |
| Per-source impl | `panel-api/internal/migrate/{cpanel,directadmin,hestiacp}/` |
| SSRF helper | `panel-api/internal/migrate/ssrf.go` |
| Agent DB restore | `panel-agent/internal/commands/db_restore.go` |
| Agent runner glue | `panel-agent/internal/commands/migration_admin_run.go` |
| Secrets dir | `/etc/jabali-panel/migration-secrets/` (root:jabali 0640) |
| Per-job staging | `/var/lib/jabali-migrations/<job-id>/` |
| Reaper unit | `jabali-migration-secrets-reap.timer` (daily 04:30 UTC) |

---

## Troubleshooting

### "secret_missing" (412) on /:id/discover-accounts
Operator skipped step 2 (POST `/:id/secrets`). Open the wizard
with `?wizard=<id>` and finish step 2.

### "wrong_state" (409) on PATCH /:id
The row isn't in `state=draft`. Either it was already submitted
(can't edit pending jobs) or reaped (>24h old draft). Check
`SELECT state, updated_at FROM migration_jobs WHERE id=?`.

### SSE stream returns 502 from nginx
nginx WS-proxy block must include `proxy_buffering off;`. Already
configured for `/api/v1/logs/stream/*`; the migrations stream
endpoint inherits the same location block. If you've forked the
vhost template, mirror the buffering setting.

### CREATE TABLE conflict on retry-resume
Update the panel — the M35.2 patch (commit `d4415d69`) sets
`reset_before_restore=true` on every db.restore call from the
migration importer. If you're still seeing this on jabali ≥ 0.2.10,
re-run the migration with **Retry from scratch** to force a fresh
DB.

### "ssrf: private (RFC1918 / ULA) rejected"
Source server is on an internal network. Either:
- Add a public DNS entry pointing at the source, or
- Flip `server_settings.migration_allow_private_hosts=1` and
  restart panel-api (operator accepts the SSRF risk).
