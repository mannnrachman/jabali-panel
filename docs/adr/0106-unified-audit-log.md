# ADR-0106 — Unified audit log (M49)

**Date**: 2026-05-17
**Status**: proposed
**Deciders**: shuki (requested), Claude (design)
**Related**: ADR-0002 (DB is source of truth), ADR-0003 (one write path),
ADR-0015 (admin impersonation JWT claim), ADR-0016 (break-glass CLI login),
ADR-0056 (M14 notification dispatcher / Redis streams), ADR-0093 (automation
tokens), ADR-0100 (M46 `db_admin_audit`), M49

## Context

There is no consolidated record of who did what. Audit data is
scattered: M46 `db_admin_audit` (DB-admin ops only), automation-token
last-used (M44), impersonation claims (ADR-0015), break-glass CLI
(ADR-0016), and otherwise only `journald`. Reconstructing an incident
timeline during the 2026-05-16 security engagement required hand-joining
journald with several tables — table-stakes for a hosting control panel
is missing, both for **operator incident response / compliance** and for
giving **end users a "recent account activity" view** (a primary
account-compromise detection surface: a user spotting an SSH key or
login they didn't make).

Two audiences, one source of truth. The design tension: a per-user view
is the classic IDOR / cross-tenant-leak trap, and an audit log that can
be mutated or that leaks request bodies is worse than none.

## Decision

A single append-only `audit_events` aggregate, fed by **one recorder
over a dedicated async audit path**, exposed through **two
server-scoped views**.

> **Design correction (2026-05-17, pre-implementation review).** The
> first draft said "fed by one recorder on the existing M14 bus".
> Grounding in code before Step 1 showed `notifications.Envelope` is
> notification-shaped (`EventKind/Severity/Title/Body/Deeplink/
> ChannelIDs/UserID` — **no structured payload**), and extending its
> wire is documented in `envelope.go` as a breaking change requiring a
> queue drain. Piggybacking a tamper-evident audit record on the
> notification Envelope is a category error. The recorder gets its own
> stream; M14 is used **only** to fan out the alert-worthy *subset*.
> The original line is kept struck-through below for provenance.

1. **Append-only, tamper-evident.** No `UPDATE`/`DELETE` of audit rows
   in code or repo. Each row carries `prev_hash`→`row_hash` computed by
   a **single-writer** chain consumer, giving cheap integrity for
   incident/compliance and a `jabali audit verify` recompute path.
   Retention is a **whole-partition drop past N days** (N from
   `server_settings`, default generous), itself recorded as an audit
   event — never a selective delete.

2. **One write path (ADR-0003), dedicated async audit stream.** A
   recorder middleware captures mutating API calls (actor from
   `ginctx` Claims, action = method + route *template*, subject,
   target, result, RequestID, source IP); explicit domain emitters
   cover non-REST-shaped events (impersonation, break-glass,
   token mint/revoke, security toggles, DB-admin). The recorder
   publishes to a **dedicated Redis stream `jabali:audit:queue`**
   (mirroring the M14 `jabali:notifications:queue` shape, but its own
   wire — a full structured audit record, not an `Envelope`),
   consumed by the **single-writer chain consumer** (decision #1)
   which computes `prev_hash`→`row_hash` and persists. Emission
   **must never block or fail the user action** (the M44
   `BumpLastUsed` discipline): async, best-effort. **Redis-down
   fallback:** the recorder falls back to a buffered direct-DB insert
   path (still off the request goroutine, `prev_hash`/`row_hash`
   left NULL and back-filled by the consumer when it recovers) so an
   audit event is never silently lost — audit reliability is not
   sacrificed to Redis availability (contrast M44 replay-store which
   is fail-*closed*; audit is fail-*open-but-recorded*).
   **M14 is used only for the alert-worthy subset.** Events that
   should notify an operator/user (impersonation start/stop,
   break-glass login, security toggles) ALSO emit a *separate*
   `notifications.Envelope` via the existing M14 `Queue.Publish` —
   that is fan-out/alerting, distinct from the durable audit record.
   The audit record's existence never depends on M14.

3. **Dual scope, server-enforced.** Every row has `subject_user_id`.
   `GET /api/v1/admin/audit` (RequireAdmin) sees all rows raw.
   `GET /api/v1/me/activity` (RequireKratosSession) is scoped to
   `subject_user_id = caller` **via a repository method, never a
   client filter**. A missing/blank subject ⇒ invisible to the user
   view (safe-fail). Server-internal/security events have no user
   subject and are therefore structurally excluded from the user
   view — exclusion by data shape, not an enumerated denylist.

4. **No request bodies, ever.** Persist action + target + result +
   structured `meta` only. Eliminates the PII/secret-leak class by
   construction.

5. **Impersonation visibility = default-ON, operator opt-out.** The
   per-user view shows admin impersonation of that user.
   `server_settings.audit_show_impersonation` (default `true`) allows
   an operator to disable it. Recorded here as a deliberate policy
   choice: hiding admin access from the accessed user defeats the
   audit log's trust purpose; the toggle exists for operators with a
   contractual reason, not as a silent default.

6. **Fold in, don't fork.** M46 `db_admin_audit` becomes a typed
   producer into `audit_events`; existing rows migrate; the old
   table is kept as a compatibility alias-view for one release
   (M50 drops it). No parallel audit store survives.

## Alternatives considered

- **Per-feature audit tables (status quo).** Rejected: that *is* the
  problem — no cross-feature timeline, every reader re-joins.
- **journald / syslog as the audit store.** Rejected: not queryable
  per-subject, not tamper-evident, not user-exposable, rotates
  independently of retention policy.
- **Mutable audit rows (status transitions in place).** Rejected:
  defeats tamper-evidence; an audit log you can edit is evidence of
  nothing.
- **Synchronous in-request recording.** Rejected: an audit write
  failing or slowing a user action is a worse outcome than a
  best-effort async emit with a buffered DB fallback.
- **Piggyback the M14 notification `Envelope` as the audit
  transport.** Rejected (this was the first-draft assumption; see the
  2026-05-17 design-correction note in §Decision): `Envelope` carries
  `EventKind/Severity/Title/Body/Deeplink/ChannelIDs/UserID` and no
  structured payload; `envelope.go` documents that extending its wire
  is a breaking change requiring a queue drain. A durable
  tamper-evident audit record is not a notification. M14 stays in
  scope, but only for fan-out of the alert-worthy subset.
- **Per-user sub-chains instead of one global chain.** Deferred: a
  global single-writer chain is simpler and its integrity story is
  stronger for v1; revisit only if the single writer bottlenecks
  (noted as advisor risk #1 in the M49 plan).
- **Client-supplied scope filter for `/me/activity`.** Rejected
  outright: that is the IDOR vulnerability; scope must be
  server-derived from the session identity.

## Consequences

### Positive

- One incident-response timeline; compliance-grade tamper-evidence.
- A user-facing compromise-detection surface that pairs with M47/M5
  outbound-abuse and 2FA work (a user whose account starts misbehaving
  can see the unfamiliar activity that preceded it).
- Reuses `ginctx` + the M14 Redis-Streams *pattern* (a second
  stream + an in-process single-writer consumer, same shape as the
  notification dispatcher — no new daemon, no new infra), and reuses
  M14 itself for alert fan-out of the sensitive subset.
- Secret/PII leakage into audit is impossible by construction
  (no bodies) rather than by review vigilance.

### Negative / costs

- Single-writer hash-chain serialises audit persistence (mitigated:
  dedicated `jabali:audit:queue` consumer; sub-chains are the escape
  hatch if needed).
- A second Redis stream + consumer goroutine to own/operate
  (acceptable: it is the same proven shape as the M14 dispatcher, and
  the chain consumer had to exist anyway for `prev_hash`/`row_hash`).
- A correctness burden on the recorder: every emitter must set
  `subject_user_id` correctly. Mitigated by safe-fail (blank subject
  = invisible to users, never cross-tenant-leaked) and an explicit
  `/me/activity` cross-tenant IDOR test gate.
- Retention as whole-partition-drop trades fine-grained retention for
  append-only integrity — acceptable and documented.
- One-release window where `db_admin_audit` exists as an alias-view
  (reader-compat) before the M50 drop.

## Future work

- M50: drop the `db_admin_audit` compatibility view.
- Optional signed (not just hashed) chain export for external
  compliance attestation.
- Per-subject sub-chains if the global writer bottlenecks.

## References

- `plans/m49-unified-audit-log.md` — milestone blueprint + 8-step wave plan
- ADR-0056 (M14 dispatcher), ADR-0093 (M44 async last-used pattern reused for non-blocking emit)
