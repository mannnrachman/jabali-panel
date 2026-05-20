# ADR-0110 — M47 Waves 4/6/8/9: Stalwart-report ingest + deliverability score

**Status:** Accepted
**Date:** 2026-05-20
**Supersedes:** the Wave 4/6/8 architecture sketched in `plans/m47-email-deliverability.md` (JMAP mailbox poll)

## Context

The original M47 plan had Waves 4/6/8 each building a separate JMAP
mailbox-poll loop to read RUA / TLS-RPT / ARF emails out of the
operator's report mailbox, parse them, and persist normalised rows.

A live spike on `.150` (mx.jabali-panel.local, Stalwart 1.0.0,
2026-05-20 — saved as `project_stalwart_native_report_storage`)
showed Stalwart already does this work and exposes the parsed
payloads as first-class schema objects:

- `DmarcExternalReport` — `report:DmarcReport` (RFC 7489)
- `TlsExternalReport`   — `report:TlsReport`   (RFC 8460)
- `ArfExternalReport`   — `report:ArfFeedbackReport` (RFC 5965)

All three share an envelope (`from`, `to`, `subject`, `receivedAt`,
`expiresAt`) and Stalwart exposes filters keyed on `receivedAt` and
`domain`. The Wave 6 standalone parser (PR #63, `internal/dmarcrua`)
still has value as a fallback for direct operator file-uploads, but
the **primary ingest source** collapses to ONE pattern:

> Poll Stalwart admin REST every 5 min for each report type with
> `receivedAt:>cursor`; persist into the matching aggregate table;
> dispatch an M14 envelope.

## Decision

1. **`internal/stalwartadmin` is a stalwart-cli subprocess wrapper**,
   not a hand-rolled HTTP client. Stalwart's admin REST uses HTTP/2 +
   hash-redirect URLs that change with every schema version; the
   upstream CLI tracks the schema and gives us a stable interface.
   - `Client.Query(ctx, typeName, filters...)` shells out to
     `/usr/local/bin/stalwart-cli` with `--json`, returns the raw
     JSON array stdout.
   - `Client.Get(ctx, typeName, id)` for singletons.
   - Both validate `typeName` / `id` / `filter` strictly (CamelCase /
     alphanumeric-only / no `-` prefix) — argv-injection guard, since
     filters become CLI flags.
   - install.sh `_install_stalwart_cli` already provisions the binary
     on every host, so the panel-api can rely on it being present.
2. **Three thin ingest sources** (one per report type) share the
   same shape: poll on `mailDmarcIngestTick = 5 * time.Minute`, cursor
   from each repo's `MostRecent*` method, slack the cursor backwards
   by 1h to catch out-of-order `receivedAt`, idempotency gate via
   `ExistsForReport` / `ExistsForStalwartID`.
3. **Schema additions**
   - `mig 000143` — `arf_report` table (DMARC + TLS-RPT tables
     already exist from mig 000139). UNIQUE index on `stalwart_id`
     drives idempotency on re-runs.
   - `models.ARFReport` + `models.TLSRPTAggregate` (the latter only
     adds Go shape; table existed from mig 000139, just unused).
   - Repos: `ARFReportRepository`, `TLSRPTAggregateRepository`,
     extended `DMARCAggregateRepository` with `MostRecentWindowEnd` +
     `CountFailuresSince`.
4. **M14 dispatch** — three new EventKinds (string literals;
   ADR-0056 doesn't enum these):
   - `mail.dmarc.report_received` — severity warning when >10% of
     records DKIM-fail.
   - `mail.tls.report_received` — severity warning when any
     `totalFailureSessionCount > 0`.
   - `mail.feedback.received` — always warning (an inbound ARF
     report is an operator-worthy event).
5. **Wave 9 score card** — single admin REST `GET /admin/mail/deliverability`
   returns a 0-100 score plus four `components` (RBL / DMARC failures /
   TLS-RPT failures / abuse reports), each capped at a 25-point
   deduction so the operator sees exactly which signal cost what.
   Mounted at `/jabali-admin/mail/deliverability`.

## Trade-offs

- **Subprocess vs HTTP client**: subprocess adds ~30 ms per call but
  costs ZERO maintenance when Stalwart's REST URL hashing rotates.
  At 5-min cadence × 3 ingest types = 36 execs/hour — invisible
  load.
- **Cursor in DB vs separate cursor table**: persisting in the data
  table avoids a separate `stalwart_report_cursor` table and its
  schema-drift risk. Cost: a `SELECT MAX(window_end)` per poll, but
  the existing index covers it cleanly.
- **Cursor slack (`1h`)**: trades a few duplicate-existence checks
  per pass against the risk of permanently missing late-arriving
  reports (Stalwart can buffer reports for hours when its outbound
  queue is delayed). The `ExistsForReport` gate makes duplicates
  cheap; missed reports are silent and hard to detect.
- **Wave 9 score is server-wide** (not per-domain). Per-domain
  breakdown is a follow-up — the data is already in the aggregate
  tables, just needs a UI surface.

## Verification

- `internal/stalwartadmin` unit tests pin: happy-path argv, empty
  output → `[]`, type-name rejection, filter-flag rejection, stderr
  surfaced in errors, get rejects bad ids.
- Live-VM verification of the ingest loops is deferred to operator
  smoke (see `plans/m47-rest-waves-runbook.md`) — needs real RUA
  reports to land in the report mailbox, which takes 24-48h after
  first DMARC publication.

## Companion findings

- `project_stalwart_native_report_storage` — the spike that drove
  this architecture.
- `project_stalwart_mtaouthound_throttle_pin` — Wave 3 throttle
  shape pinned at the same time (deferred to Wave 7d when MtaSts
  singleton sync also lands via this same client).
