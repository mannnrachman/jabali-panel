# M47 — Email Deliverability Suite

**Status:** Blueprint (pre-advisor)
**ADR target:** 0103 (verify free at write-time; 0101/0102 in-flight on PRs #8/#12)
**Milestone #:** M47 (M46 highest shipped)
**Depends on:** M6/M6.x (Stalwart + Bulwark, SPF/DKIM/DMARC already shipped)

## 1. Goal

Close the #1 hosting support-ticket class ("my mail bounces / lands in
spam / queue stuck"). Build operator + tenant visibility and control
on top of the existing Stalwart stack. NOT a new MTA — surfaces and
governs what Stalwart already does.

Five capabilities:

| # | Feature | Why |
|---|---------|-----|
| 1 | **Mail queue UI** (admin + per-domain) | "stuck mail" tickets; today the queue is invisible |
| 2 | **Per-user outbound throttle + abuse detection** | compromised account → spam → IP blacklisted → every tenant's mail dies |
| 3 | **RBL / blacklist monitoring** | know the server IP is listed before customers do |
| 4 | **DMARC aggregate report ingestion + UI** | DMARC shipped but reports are write-only; operators are blind |
| 5 | **Deliverability score per domain** | one panel showing SPF/DKIM/DMARC/PTR/RBL status, green/red |

## 2. Constraints / locked decisions

- Stalwart is the source of truth for queue + delivery state. Read via
  its admin API over the pinned `127.0.0.1:8080` (ADR-0050/0045) —
  NEVER add a TCP loopback elsewhere (M25 load-bearing).
- DB-as-truth (ADR-0002): throttle policies, RBL state, DMARC
  aggregates persisted in `jabali_panel`; reconciler/agent converge
  Stalwart config. No state only-in-Stalwart that the panel needs.
- Agent does NOT open outbound (ADR-0001/0050). RBL lookups + DMARC
  report fetch (IMAP/HTTP) go through panel-api, not the agent.
- Throttle enforcement is Stalwart-native (its rate limiting / sieve),
  driven by the agent writing Stalwart config — not a jabali shim.
- Per-user abuse signals feed M14 (ADR-0056) — reuse, don't rebuild.
- Wire contracts to Stalwart's API: enumerate property KINDS
  (`feedback_schema_enumerate_kinds_not_names`); pin with a contract
  test (`feedback_cross_boundary_contracts`).

## 3. ADRs

| ADR | Title |
|-----|-------|
| 0103 | Email deliverability: Stalwart-API-sourced queue/state, DB-as-truth for policy, panel-side RBL/DMARC fetch |

## 4. Wave / step plan (7 steps, inline per `feedback_never_agents`)

0. Migration: `mail_outbound_policy` (per-user/domain rate caps),
   `mail_rbl_state`, `dmarc_aggregate` (reporter, domain, pass/fail
   counts, window). Schema only. `utf8mb4_unicode_ci` on FK cols.
1. Agent `mail.queue.list` / `mail.queue.retry` / `mail.queue.delete`
   — Stalwart admin-API passthrough; arg-sanitised; never echo creds.
2. panel-api `/admin/mail/queue` + per-domain scoped view (list
   envelope `{data,total,page,page_size}` — `feedback_verify_wire_contract`).
3. Outbound throttle: `mail_outbound_policy` + reconciler converges
   Stalwart rate-limit config; agent `mail.throttle.apply`. Default
   sane cap (e.g. N/hour/account) opt-out per package.
4. Abuse detection event source (`internal/eventsources/mail_abuse.go`)
   — poll Stalwart send stats per account; threshold breach →
   M14 `mail.abuse.detected` (auto-throttle + alert).
5. RBL monitor: panel-api periodic check of public IP(s) against a
   curated RBL set → `mail_rbl_state` → M14 `mail.rbl.listed`
   (critical). UI badge on Server Status + Email tab.
6. DMARC ingest: panel-api pulls aggregate reports (from the
   rua mailbox the DKIM/DMARC setup already provisions) → parse XML →
   `dmarc_aggregate` → per-domain UI (pass rate, top failing sources).
7. Deliverability score card: one per-domain widget aggregating
   SPF/DKIM/DMARC/PTR/RBL → green/amber/red + fix hints. Tests
   (agent arg-san, repo sqlmock, eventsource, contract test for the
   Stalwart queue API), runbook, ADR→Accepted, BLUEPRINT + memory.

## 5. Scars honored

List envelope; migration schema-only + collation; agent no-outbound;
M14 reuse; Stalwart API contract test; `npm run build` before UI green;
branch-only until ship-ready (`feedback_no_partial_blueprint_to_main`);
inline execution.

## 6. Open risks for advisor

1. Stalwart admin-API surface for queue ops + send stats — verify it
   exposes per-account counters (Context7/Stalwart docs) before Step 4.
2. RBL query volume/ethics — cache hard, low frequency, curated list
   only (not 100 RBLs hammered).
3. DMARC rua mailbox: confirm M6.x provisions a readable aggregate
   inbox; if not, Step 6 gains a provisioning sub-step.
