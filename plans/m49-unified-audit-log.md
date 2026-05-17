# M49 — Unified Audit Log (admin + per-user activity)

**Status:** Blueprint (pre-advisor)
**ADR target:** 0106 (0105 taken by M32.x panel-cert-split on gitea main; 0103=M47, 0104=M48 reserved)
**Milestone #:** M49 (M46 highest shipped; M47 email-deliverability, M48 staging in-plan)
**Depends on:** M14 (notification dispatcher / Redis streams — event source),
M20 (Kratos identity — actor resolution), `ginctx` (request-scoped Claims +
RequestID), M46 (`db_admin_audit` — folded in, not parallel)

## 1. Goal

One queryable, append-only, tamper-evident timeline of
security-sensitive actions, with **two scoped views over one store**:

| Capability | Detail |
|------------|--------|
| Admin forensics view | `GET /api/v1/admin/audit` (RequireAdmin): every event, raw action/target, filter actor/subject/action/result/time/source-IP |
| Per-user activity view | `GET /api/v1/me/activity` (RequireKratosSession): ONLY rows whose `subject_user_id` = caller — their own actions + actions taken *on* their account; curated/redacted; compromise-detection surface |
| Recorder | One-write-path: middleware records mutating API calls + explicit high-value domain events; published to a dedicated `jabali:audit:queue` Redis stream → single-writer chain consumer (NOT the M14 Envelope — see §6 design correction). M14 fans out only the alert-worthy subset |
| Tamper-evidence | Per-row `prev_hash` chain (single-writer); cheap integrity for incident/compliance |
| Retention | Append-only; prune job by age (notifications-style); no UPDATE/DELETE ever |
| Surface | Admin "Audit Log" page + user-shell "Account Activity" page (`SearchableTable`, list envelope); `jabali audit query` CLI |

## 2. Constraints / locked decisions

- **DB-as-truth (ADR-0002):** `audit_events` aggregate is append-only.
  No UPDATE/DELETE in code or repo — retention is a partition/prune
  job. Mutation of an audit row is a defect, not a feature.
- **One write path (ADR-0003):** a recorder middleware captures
  mutating API calls (actor from `ginctx` Claims, action = method +
  route template, subject, target, result, RequestID, source IP) +
  explicit domain emitters for events that aren't a single REST
  mutation: impersonation start/stop (ADR-0015), break-glass CLI
  login (ADR-0016), automation-token mint/revoke (M44), DB-admin ops
  (M46 — fold `db_admin_audit` in), security toggles
  (CrowdSec/UFW/AppArmor/egress).
- **Dedicated audit stream, never blocks the request.** The recorder
  publishes a full structured audit record to its own Redis stream
  `jabali:audit:queue` (M14 dispatcher *shape*, own wire — NOT the
  notification `Envelope`, which has no structured payload; see §6).
  Async (M44 `BumpLastUsed` discipline) — never fails/slows the user
  action. **Redis-down fallback:** buffered direct-DB insert off the
  request goroutine (`prev_hash`/`row_hash` NULL, back-filled by the
  consumer on recovery) so audit is never silently lost — audit is
  fail-*open-but-recorded*, not fail-closed. M14 (`Queue.Publish`) is
  used **only** to fan out the alert-worthy subset (impersonation,
  break-glass, security toggles) as a *separate* notification — the
  audit record never depends on M14.
- **Dual scope, server-enforced.** `subject_user_id` on every row.
  `/me/activity` is subject-scoped via a **repo method**, never a
  client filter param (the `RequireOwner`/domain-404 discipline that
  held under live testing). Missing/blank subject ⇒ invisible to the
  user view = safe failure mode.
- **Redaction is structural, not a denylist.** Never persist request
  bodies (PII/passwords/secrets) — action + target + result only.
  The user view excludes server-internal/security-stack events
  *because they have no `subject_user_id` and therefore don't match
  the subject filter*, not via an allow/deny list (safer than
  enumerating).
- **Impersonation visibility = default-ON, operator toggle.** The
  per-user view shows admin impersonation of that user
  (transparency/compliance). `server_settings.audit_show_impersonation`
  (default true) lets an operator opt out. This is a deliberate
  policy decision, recorded in the ADR — not a silent default.
- **Fold in, don't fork:** M46 `db_admin_audit` becomes a typed
  producer into `audit_events` (migrate existing rows; drop the
  parallel table in the same migration or alias-view it for one
  release — decide in Step 0).
- List envelope `{data,total,page,page_size}`; schema-only migration;
  branch-only until ship-ready; inline.

## 3. ADRs

| ADR | Title |
|-----|-------|
| 0106 | Unified audit log: append-only hash-chained `audit_events`, one-write-path recorder over a dedicated `jabali:audit:queue` stream (M14 alert-subset only — design corrected 2026-05-17), dual admin/subject scope, impersonation-visibility default-on toggle, M46 `db_admin_audit` fold-in |

## 4. Wave / step plan (8 steps, inline)

0. Migration `audit_events` (id ULID, ts, actor_user_id NULL,
   actor_kind[user|admin|automation|system|cli], subject_user_id NULL
   (idx `(subject_user_id, ts)`), action, target_type, target_id,
   result[ok|denied|error], source_ip, request_id, prev_hash,
   row_hash, meta JSON). Fold-in plan for `db_admin_audit`
   (migrate rows → drop or alias-view). Schema only + collation.
1. `internal/audit` pkg — `Recorder` (async publish to
   `jabali:audit:queue` + buffered DB fallback when Redis is down),
   hash-chain computer (single-writer consumer), typed event
   constructors; narrow seams; sqlmock repo. No middleware wiring yet.
2. Recorder middleware on mutating routes (POST/PATCH/PUT/DELETE):
   derives actor/subject/target/result/request-id; **bodies never
   captured**. Arg/route-template normalised (no high-cardinality
   ids in `action`).
3. Domain emitters wired: impersonation (ADR-0015), break-glass
   (ADR-0016), token mint/revoke (M44), security toggles, M46
   db-admin → unified store. M46 parallel-table read path retired.
4. Hash-chain consumer: single goroutine consumes `jabali:audit:queue`,
   computes `prev_hash`→`row_hash`, persists; on startup also
   back-fills `row_hash` for any rows the Redis-down DB fallback
   inserted with NULL hashes (chain stays contiguous). Gap/restart
   safe (chain head in DB).
5. Admin API + UI: `GET /admin/audit` (filters, list envelope) +
   admin "Audit Log" `SearchableTable` page. Actor vs subject
   rendered ("admin X did Y to user Z").
6. Per-user API + UI: `GET /me/activity` (subject-scoped repo
   method, curated field set) + user-shell "Account Activity" page.
   Impersonation rows gated by `audit_show_impersonation`.
7. Retention prune job (age-based, reconciler-tick or timer; never
   touches the chain head); `jabali audit query` CLI (admin: any;
   `--me` resolves caller). Tamper-verify subcommand
   (`jabali audit verify` recomputes the chain).
8. Tests (audit pkg unit + chain integrity + repo sqlmock + recorder
   table + cross-tenant `/me/activity` IDOR test), runbook
   (incident-query + chain-verify), ADR→Accepted, BLUEPRINT + memory.
   E2E (Playwright) admin + user activity views happy path.

## 5. Scars honored

Append-only (no mutate-audit); one-write-path recorder; async-emit
never blocks the request (M44 lesson); **never persist request
bodies** (secret-leak scar); `/me/activity` is the classic
cross-tenant/IDOR trap → server-side subject scope via repo method,
blank-subject = invisible (safe-fail), mirrored on the
domain-404/RequireOwner pattern that survived live testing; fold-in
not fork (M41 dbops "write once" discipline vs the M46 parallel
table); list envelope; schema-only migration + collation; M14
dispatcher *pattern* reuse (own stream, same proven shape — not the
notification Envelope, not a new daemon) + M14 for alert fan-out
only; honest v1 scope (sensitive mutations +
auth/security events, NOT every GET; structured fields, no free-text);
branch-only; inline.

## 6. Open risks for advisor

### Resolved pre-implementation (2026-05-17 code-grounded review)

- **R0 — "emit via M14 bus" was wrong.** `notifications.Envelope`
  is notification-shaped (no structured payload; extending its wire
  is a documented breaking change). **Resolved:** dedicated
  `jabali:audit:queue` stream (M14 *shape*, own wire) + single-writer
  chain consumer + Redis-down buffered-DB fallback; M14 used only to
  fan out the alert subset. ADR-0106 §Decision corrected (with
  struck-through provenance) + Alternatives updated. This is the
  category of error the pre-advisor gate exists to catch — caught
  before Step 1 code.

### Open

1. **Chain integrity vs throughput.** A single-writer hash-chain
   serialises audit persistence. Mitigate: the chain consumer is the
   only writer (M14 consumer group, one partition for `audit.*`);
   if volume is a problem, per-`subject` sub-chains (decide Step 4).
   Is global-chain integrity worth the single-writer? (recommend yes
   for v1 — compliance value; revisit if it bottlenecks.)
2. **M46 fold-in cutover.** Drop `db_admin_audit` immediately vs
   keep a compatibility view for one release. Recommend
   migrate-rows + alias-view for one release (no reader breaks),
   drop in M50. Decide Step 0.
3. **Impersonation visibility.** Confirm default-ON +
   `server_settings` opt-out is the right call (vs default-off /
   no-toggle). Recommend default-ON: hiding admin access from the
   accessed user defeats the audit log's trust purpose.
4. **Retention vs compliance.** A prune job deletes old rows →
   conflicts with "append-only/tamper-evident". Resolve: prune is a
   *whole-partition drop past N days*, recorded as its own audit
   event, never a selective delete; N is `server_settings`-driven,
   default generous (e.g. 365d). Confirm the model.
5. **Volume/cardinality.** Recording every mutation could be noisy.
   v1 = security-sensitive mutations + auth/impersonation/token/
   security-toggle/db-admin/file-DB-domain-mutations; explicitly NOT
   every GET, NOT read endpoints. Confirm the inclusion list in the
   ADR.
