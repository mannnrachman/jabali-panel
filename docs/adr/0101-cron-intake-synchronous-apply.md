# ADR-0101 — Cron Job Intake owns synchronous agent-apply (REST + CLI)

**Date**: 2026-05-16
**Status**: accepted
**Deciders**: shuki (architecture review), Claude (design)
**Related**: amends ADR-0083 (shared-ops packages); relates to ADR-0004
(reconciler-driven convergence), ADR-0029 (M8 cron via systemd-user timers)

## Context

A 2026-05-16 architecture review (`/improve-codebase-architecture`)
found the **Cron Job Intake** existed as two implementations with no
shared seam: `panel-api/internal/api/cron.go` (REST `create`/`update`)
and `panel-api/cmd/server/cron_cmd.go` (CLI `add`/`update`). Two
concrete drifts shipped because of it:

1. The CLI skipped the "user must have a Linux account" gate the REST
   path enforced (fixed earlier 2026-05-16 by single-sourcing
   `cronvalidate.ValidateLinuxAccount`).
2. **Apply semantics diverged**: REST agent-applies the systemd timer
   synchronously on create (rolls the row back + 502s on failure); the
   CLI only persisted the row and relied on the next **Reconciler**
   tick (≤60s) to materialise it. Same input, different success
   contract and different failure visibility.

ADR-0083 already prescribed extracting `cronops` (the `dbops`/`userops`
shape) but it was never done.

## Decision

Extract **Cron Job Intake** as `internal/cronops`, the single module
owning the create/update sequence end-to-end:
validate (name → Linux account → schedule → command) → owned-docroot
resolution → persist → **synchronous Agent apply**. The REST handler
and the cobra `RunE` become thin adapters: marshal input, translate
`cronops` typed sentinel errors to HTTP status / CLI text.

**Synchronous apply is owned by Intake for both callers.** The CLI
gains the same immediate success/failure contract as REST: a
`jabali cron add` that cannot materialise (bad command, agent down,
no Linux account) fails the command, it does not silently leave a row
for the reconciler to choke on.

**This does not contradict ADR-0004.** The **Reconciler** remains the
sole owner of *re-apply / drift convergence* — it still re-materialises
timers from DB state every tick. ADR-0101 only fixes *first* apply:
the producer of a **Cron Job** applies it once, synchronously, at
intake; the Reconciler keeps it converged thereafter. "First apply by
the writer, ongoing convergence by the reconciler" is the same split
domains/DNS already use.

`cronops` therefore takes an `agent.AgentInterface` dependency — the
ADR-0083 shape extended (its sibling `dbops` validates+persists only;
cron additionally applies because a persisted-but-unapplied **Cron
Job** is a stuck state, not an eventually-consistent one).

## Alternatives considered

- **Defer apply to the reconciler for both callers** (drop REST's
  inline apply). Most ADR-0004-literal, but removes synchronous
  apply-failure feedback operators rely on (a mistyped cron command
  would "succeed" then silently never run). Rejected.
- **Keep the split, document it.** Codifies a surprise (CLI eventual,
  REST synchronous) as intended. Rejected — it is the exact friction
  the review surfaced.

## Consequences

- One test surface (`cronops` table suite) replaces a DB-mocked REST
  test plus a cobra test that cannot reach the DB path.
- The next cron intake drift is structurally impossible; a third caller
  (automation API) gets correct intake for free.
- CLI behavior change: `jabali cron add/update` now applies
  synchronously and can fail where it previously always "succeeded"
  and deferred. This is the intended correctness fix; noted in the
  runbook.
- `internal/cronops` depends on the agent — acceptable and bounded
  (ADR-0083 amendment): apply is part of the **Cron Job Intake**
  invariant.
