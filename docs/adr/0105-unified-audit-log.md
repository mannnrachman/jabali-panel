# ADR-0105 — Unified audit log (M49)

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

A single append-only `audit_events` aggregate, fed by **one recorder on
the existing M14 bus**, exposed through **two server-scoped views**.

1. **Append-only, tamper-evident.** No `UPDATE`/`DELETE` of audit rows
   in code or repo. Each row carries `prev_hash`→`row_hash` computed by
   a **single-writer** chain consumer, giving cheap integrity for
   incident/compliance and a `jabali audit verify` recompute path.
   Retention is a **whole-partition drop past N days** (N from
   `server_settings`, default generous), itself recorded as an audit
   event — never a selective delete.

2. **One write path (ADR-0003), M14-sourced (ADR-0056), async.** A
   recorder middleware captures mutating API calls (actor from
   `ginctx` Claims, action = method + route *template*, subject,
   target, result, RequestID, source IP); explicit domain emitters
   cover non-REST-shaped events (impersonation, break-glass,
   token mint/revoke, security toggles, DB-admin). Emission is
   async via the M14 bus and **must never block or fail the user
   action** (the M44 `BumpLastUsed` discipline). The M14
   consumer-group already guarantees persistence.

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
  best-effort-but-bus-backed async emit; M14 already solves durable
  async fan-out.
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
- Reuses the M14 bus + `ginctx` + consumer-group machinery — no new
  pipeline, no new daemon.
- Secret/PII leakage into audit is impossible by construction
  (no bodies) rather than by review vigilance.

### Negative / costs

- Single-writer hash-chain serialises audit persistence (mitigated:
  dedicated M14 consumer; sub-chains are the escape hatch if needed).
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
