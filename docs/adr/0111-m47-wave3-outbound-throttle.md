# ADR-0111 — M47 Wave 3: outbound throttle reconciler

**Status:** Accepted
**Date:** 2026-05-20

## Context

Wave 3 of M47 needed a way to cap outbound mail per user / per domain
/ globally, pushed into Stalwart's outbound queue so they bite at the
SMTP layer (not at the panel). mig 000139 already provisioned
`mail_outbound_policy`; the rest was unimplemented.

A live spike on `.150` (Stalwart 1.0.0) pinned the `MtaOutboundThrottle`
wire shape (see `project_stalwart_mtaouthound_throttle_pin`):

- `key` is a MAP (not array) — `{"senderDomain": true, "rcpt": true}`.
- `rate.period` is in MILLISECONDS (3600000 = 1h).
- `match` requires `{"match": {}, "else": "true"}` even for the
  always-fire case.
- CLI verbs: `create <Type> --json <body>` returns `Created <Type>
  <id>` on stdout; `update <Type> <id> --json`; `delete <Type> --ids
  <id>`.

## Decision

1. **Reuse `internal/stalwartadmin`** (ADR-0110). Extend `Client` with
   `Create` / `Update` / `Delete` thin wrappers — same subprocess
   shape, same `validateTypeName`/`validateID` argv guards. Parse the
   `Created <Type> <id>` stdout to return the upstream-assigned id.
2. **Wire-pinned Go types** in `internal/stalwartadmin/throttle.go`:
   `MtaOutboundThrottlePayload` + `HourlyRate` / `DailyRate`
   constructors + `NewAlwaysFireMatch` so callers can't drift from
   the pinned shape. Unit test pins exact JSON output (`"period":3600000`,
   `"key":{"senderDomain":true}`, etc).
3. **mig 000144** adds three columns to `mail_outbound_policy`:
   - `stalwart_id` — upstream id assigned on first Create (drives the
     diff between create/update/delete branches).
   - `last_applied_at` — observability for the operator.
   - `last_error` — non-NULL when the most recent reconcile pass
     failed; cleared on success.
4. **Reconciler step** `reconcileMailThrottles` runs on every
   `ReconcileAll` pass (right after `reconcileDBTuning`). State
   machine:
   - `enabled && stalwart_id==""`  → Create
   - `enabled && stalwart_id!=""`  → Update (idempotent)
   - `!enabled && stalwart_id!=""` → Delete, clear id
   - `!enabled && stalwart_id==""` → no-op
   - Errors stamp `last_error` + retain `stalwart_id` → self-healing.
5. **Scope → Stalwart key mapping**:
   - `global` → empty key map (applies to every message)
   - `user`   → `key: {sender: true}`
   - `domain` → `key: {senderDomain: true}`
6. **Single rate window (v1)** — Stalwart's `rate` object holds ONE
   bucket. We use the hourly cap when set, else daily. Per-row
   support for BOTH hourly + daily caps would need two Stalwart
   objects per row (paired); deferred. The daily cap is logged in
   the description string so operators still see what was intended.
7. **Match condition (v1)** — always-fire via `{match:{}, else:"true"}`.
   Stalwart's Expression grammar (`match.match`) supports `==`,
   tenant filters, etc — pinning that grammar against a live host is
   a separate spike; deferred. Until then, per-user throttles
   actually cap **every** user collectively (because key=sender alone
   doesn't filter — it groups). v2: pin the Expression grammar so
   `sender == 'user@example.com'` filters the throttle to one mailbox.
8. **Admin UI + REST** — `/jabali-admin/mail/throttles`. CRUD via
   `/api/v1/admin/mail/throttles`. The list view surfaces
   `stalwart_id` / `last_error` per row so the operator sees both
   sides of the convergence at a glance.
9. **Inline delete dispatch** in the admin DELETE handler — when the
   client is available we synchronously Delete the Stalwart-side
   object BEFORE removing the DB row (avoids leaving a stranded
   Stalwart throttle when the row is gone and the reconciler can't
   see it anymore).

## Verification

- `TestMtaOutboundThrottlePayload_WireShape` pins the exact JSON
  shape against the live-spike-validated bytes.
- `TestClient_Create_ParsesAssignedID` pins the stdout parse.
- 6 reconciler test cases cover every state-machine branch + the
  scope→key mapping + the keep-id-on-error retry.
- Live verification on .150 created → updated → deleted a
  MtaOutboundThrottle round-trip as part of the spike, confirming
  the wire shape against Stalwart 1.0.0.

## Out of scope / follow-ups

- Per-user expression filtering (needs Stalwart Expression grammar
  pinned).
- Per-day cap as a separate Stalwart object (would double-write per
  row).
- UI for monitoring how often a throttle actually fires (would need
  Stalwart's `Metric` schema object).
