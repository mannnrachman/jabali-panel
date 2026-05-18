# M47 — Email Deliverability Suite (+ MTA-STS / TLS-RPT)

**Status:** Blueprint (reconciled to origin/main post-M49; pre-advisor)
**ADR target:** 0103 (verified free — files: 0102, 0105, 0106; 0103/0104 never written)
**Milestone #:** M47
**Depends on:** M6/M6.x (Stalwart + Bulwark; SPF/DKIM/DMARC shipped), M15 (PDNS/DNSSEC — owns DNS TXT), M14 (events)

## 1. Goal

Close the #1 hosting support-ticket class ("mail bounces / spam / queue
stuck") + add modern SMTP-security policy (MTA-STS, TLS-RPT). NOT a new
MTA — surfaces and governs what Stalwart already does, and publishes the
policy DNS/HTTPS jabali can already serve.

| # | Capability | Why |
|---|------------|-----|
| 1 | Mail queue UI (admin + per-domain) | "stuck mail" tickets; queue is invisible today |
| 2 | Per-user outbound throttle + abuse detection | compromised acct → spam → IP blacklisted → every tenant's mail dies |
| 3 | RBL / blacklist monitoring | know the IP is listed before customers do |
| 4 | DMARC aggregate ingestion + UI | DMARC shipped but rua is write-only; operators blind |
| 5 | Deliverability score per domain | one widget: SPF/DKIM/DMARC/PTR/RBL/MTA-STS green/red |
| 6 | **MTA-STS policy** (per-domain, testing→enforce) | downgrade-attack / opportunistic-TLS gap; modern requirement |
| 7 | **TLS-RPT** (publish + ingest reports) | visibility into who failed STARTTLS to you |

## 2. Constraints / locked decisions

- Stalwart = source of truth for queue + delivery. Read via its admin
  API at the **pinned `http://127.0.0.1:8446`** (NOT :8080 — that was a
  stale blueprint figure; real pin per `mailbox_jmap.go`,
  ADR-0045/0050), HTTP Basic ("admin", token from
  `stalwart-admin.token`). NEVER add a TCP loopback elsewhere (M25
  load-bearing).
- DB-as-truth (ADR-0002): throttle policy, RBL state, DMARC/TLS-RPT
  aggregates, MTA-STS policy state in `jabali_panel`; reconciler/agent
  converge Stalwart + DNS. No panel-needed state only-in-Stalwart.
- Agent does NOT open outbound (ADR-0001/0050). RBL lookups + DMARC/
  TLS-RPT report fetch (IMAP/HTTP) run in panel-api, not the agent.
- Throttle enforcement is Stalwart-native (its rate-limit/sieve),
  driven by the agent writing Stalwart config — not a jabali shim.
- MTA-STS/TLS-RPT DNS (`_mta-sts.<d>` TXT, `mta-sts.<d>` policy host,
  `_smtp._tls.<d>` TXT) goes through the **existing M15 PDNS DNS
  reconciler** — no new DNS path. MTA-STS policy file served by the
  existing nginx vhost stack at `https://mta-sts.<d>/.well-known/mta-sts.txt`.
- Per-user abuse + RBL + TLS failures feed M14 (ADR-0056) — reuse.
- Wire contracts to Stalwart's admin API: enumerate property KINDS
  (`feedback_schema_enumerate_kinds_not_names`), pin with a contract
  test (`feedback_cross_boundary_contracts`, `feedback_verify_wire_contract`).

## 3. ADRs

| ADR | Title |
|-----|-------|
| 0103 | Email deliverability: Stalwart-admin-API (`:8446`) queue/state, DB-as-truth policy, panel-side RBL/DMARC/TLS-RPT fetch, MTA-STS via M15 PDNS + nginx |

## 4. Wave / step plan (10 steps, inline per `feedback_never_agents`)

0. Migration **000139** (verified next-free; M49 took 000138):
   `mail_outbound_policy`, `mail_rbl_state`, `dmarc_aggregate`,
   `mta_sts_policy` (domain, mode[none|testing|enforce], max_age,
   updated), `tlsrpt_aggregate`. Schema only; `utf8mb4_unicode_ci` on
   FK cols (`feedback_mariadb_collation_fk`). Data seeds app-side
   (`feedback_migration_data_seed_ordering`).
1. Agent `mail.queue.list|retry|delete` — Stalwart admin-API
   (`127.0.0.1:8446`, Basic) passthrough; arg-sanitised; never echo
   creds; contract test pins the response KINDS.
2. panel-api `/admin/mail/queue` + per-domain scoped view; list
   envelope `{data,total,page,page_size}` (`feedback_verify_wire_contract`).
3. Outbound throttle: `mail_outbound_policy` + reconciler converges
   Stalwart rate-limit config via agent `mail.throttle.apply`; sane
   default cap, per-package opt-out.
4. Abuse eventsource (`internal/eventsources/mail_abuse.go`): poll
   Stalwart per-account send stats; breach → M14 `mail.abuse.detected`
   (auto-throttle + alert).
5. RBL monitor: panel-api periodic curated-RBL check of public IP(s)
   → `mail_rbl_state` → M14 `mail.rbl.listed` (critical); badge on
   Server Status + Email tab. Hard cache, low freq, curated set only.
6. DMARC ingest: panel-api pulls aggregate reports from the rua
   mailbox M6.x provisions (confirm readable; else add provisioning
   sub-step) → parse XML → `dmarc_aggregate` → per-domain UI.
7. **MTA-STS:** per-domain `mta_sts_policy`; reconciler emits
   `_mta-sts.<d>` + (DNSSEC-signed) policy host via M15 PDNS; policy
   file `https://mta-sts.<d>/.well-known/mta-sts.txt` served by the
   nginx vhost stack; default mode=testing, operator promotes to
   enforce. Cert for `mta-sts.<d>` via the existing per-domain LE path.
8. **TLS-RPT:** emit `_smtp._tls.<d>` TXT (rua=mailto: the same
   aggregate inbox); ingest+parse TLS-RPT JSON reports →
   `tlsrpt_aggregate` → per-domain UI; STARTTLS-failure spike → M14.
9. Deliverability score card: per-domain widget aggregating
   SPF/DKIM/DMARC/PTR/RBL/MTA-STS → green/amber/red + fix hints.
   Tests (agent arg-san, repo sqlmock, eventsource, Stalwart contract
   test, DNS-emit test), runbook, ADR→Accepted, BLUEPRINT + memory.

## 5. Scars honored

List envelope; schema-only migration + collation + app-side seed;
agent no-outbound; reuse M14/M15/PDNS not reinvent; Stalwart admin
pin is `:8446` (wire-contract verified, not the stale `:8080`);
contract test before trusting Stalwart JSON; `npm run build` before UI
green; branch-only until fully ship-ready
(`feedback_no_partial_blueprint_to_main`); inline execution
(`feedback_never_agents`); ADR/migration numbers re-checked vs Gitea
main, never the github mirror.

## 6. Open risks for advisor

1. Stalwart admin-API surface for queue ops + per-account send stats —
   verify exact endpoints + response KINDS on a live Stalwart (M6
   version pin) before Steps 1/4. Don't trust upstream docs blindly.
2. RBL query volume/ethics — curated set, hard cache, low frequency.
3. DMARC/TLS-RPT rua: confirm M6.x provisions a readable aggregate
   inbox; if not, Step 6 gains a provisioning sub-step (shared by 8).
4. MTA-STS policy host needs a valid LE cert for `mta-sts.<d>` — a SAN
   on the domain cert vs its own cert; decide in Step 7 (reuse the
   per-domain LE path; mode=testing until the cert + DNS verify).
5. Promoting MTA-STS testing→enforce is operator-gated + irreversible-
   ish for senders mid-flight; typed confirm + a documented rollback
   (mode=none) — mirror the M48 destructive-op discipline.
