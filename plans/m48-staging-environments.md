# M48 — Staging Environments (generalized)

**Status:** Blueprint (pre-advisor)
**ADR target:** 0104 (verify free at write-time)
**Milestone #:** M48
**Depends on:** M2 (domains/vhosts), M7 (DB), M10 (WP clone — the proven primitive), per-user slices

## 1. Goal

1-click **staging copy of any site** (not just WordPress): clone
docroot + database(s) into an isolated staging domain, edit safely,
then **push-to-live** (or pull-from-live to refresh staging). Dev
differentiator vs cheap panels; turns the WP-clone primitive into a
general capability.

| Capability | Detail |
|------------|--------|
| Create staging | from a live domain → `staging.<domain>` (or chosen label): copy docroot, clone+rewire DB(s), new vhost, SSL, NOT in DNS-public by default (hosts-file/basic-auth gated) |
| Push to live | staging → live: docroot sync + DB migrate, with a pre-push live backup (M30 restic) as the rollback |
| Pull from live | refresh staging from current live (discard staging changes) |
| Diff / dry-run | show file + DB changes before push |
| Delete staging | reap docroot + DB + vhost + slice |

## 2. Constraints / locked decisions

- DB-as-truth (ADR-0002): a `site_staging` aggregate row is the
  control record; reconciler/agent converge filesystem + vhost + DB.
- Reuse, don't reinvent: docroot copy + DB clone already exist in the
  WP-clone path + `dbops`; M48 generalizes them behind a
  `stagingops` module (ADR-0083 shape) — engine-aware (MariaDB +
  Postgres), app-agnostic.
- **Push-to-live is destructive.** Mandatory pre-push live backup via
  M30 restic; push is gated behind an explicit typed confirm; the
  backup snapshot id is recorded on the staging row for one-click
  rollback. Non-negotiable (mirrors the M30/per-user-slices
  destructive-cutover discipline).
- Per-user isolation: staging runs under the SAME user's slice/FPM
  pool (no privilege gain); staging docroot under the user's tree.
- Staging is NOT publicly resolvable by default (no auto DNS record);
  reachable via panel-issued preview host + basic-auth. Avoids
  duplicate-content + accidental indexing.
- Wire contracts (agent copy/clone commands) pinned by tests.
- App config rewrite: WP wp-config / URL search-replace handled (reuse
  M10); generic apps get docroot+DB copy + a documented "you may need
  to update hardcoded URLs" (don't fake deep per-CMS rewrites for v1
  — honest scope, like the M5/M35 honest-scope discipline).

## 3. ADRs

| ADR | Title |
|-----|-------|
| 0104 | Generalized staging: site_staging aggregate, stagingops module, mandatory pre-push backup, non-public-by-default preview |

## 4. Wave / step plan (8 steps, inline)

0. Migration `site_staging` (id ULID, domain_id, user_id, label,
   state[creating|ready|pushing|pulling|deleting|failed],
   db_map JSON, last_push_backup_id, created/updated). Schema only.
1. `internal/stagingops` skeleton — Create/PushToLive/PullFromLive/
   Delete/Diff; typed sentinels; narrow seams; engine-aware DB clone
   via existing `dbops` primitives. No agent calls yet (pure plan).
2. Agent commands: `staging.docroot.copy` (rsync, user-owned, slice),
   `staging.vhost.apply` (preview host + basic-auth, NOT in DNS),
   reuse db clone. Arg-sanitised.
3. Create flow end-to-end: stagingops.Create → copy + DB clone +
   vhost + self-signed/LE-preview SSL; reconciler converges; status
   poll (mirror DatabasesCard install-poll pattern).
4. Diff: file tree + DB schema/row-count diff summary (cheap, not a
   full data diff) → UI panel before push.
5. Push-to-live: **pre-push M30 backup (record snapshot id)** →
   docroot sync (staging→live) → DB migrate → reconcile → on failure,
   surface the one-click restore (snapshot id). Typed confirm.
6. Pull-from-live: refresh staging from live (discard staging);
   confirm (destroys staging changes).
7. Delete + reap; reconciler cleans orphaned staging on stale state.
8. UI (Domains → "Staging" action + a Staging tab), tests (stagingops
   table, agent arg-san, repo sqlmock), runbook (push/rollback),
   ADR→Accepted, BLUEPRINT + memory. E2E (Playwright) for create+push
   happy path against mock agent.

## 5. Scars honored

Destructive op = mandatory backup + typed confirm + recorded rollback
id (M30/slices lesson); reuse WP-clone/dbops not reinvent; honest v1
scope (generic-app URL rewrite documented, not faked); list envelope;
schema-only migration + collation; agent no-outbound; branch-only
until ship-ready; inline.

## 6. Open risks for advisor

1. DB clone for large DBs — time/lock; reuse dbops dump/restore, cap +
   background job + status (M46 db_admin_jobs pattern).
2. Preview-host SSL: LE for a non-public host won't validate (HTTP-01
   needs public DNS) — use self-signed for preview, document; OR
   panel-hostname-SAN (M6.4 pattern). Decide in Step 3.
3. Push-to-live DB migrate semantics: full replace vs schema-merge?
   v1 = full replace of the cloned DBs (backup is the safety net);
   schema-merge is out of scope (honest).
4. Disk: staging doubles a site's footprint — enforce per-package
   quota check before create (M18 limits integration).
