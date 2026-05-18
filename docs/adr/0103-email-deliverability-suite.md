# ADR-0103: Email deliverability suite (queue/throttle/RBL/DMARC + MTA-STS/TLS-RPT)

**Status:** Proposed
**Date:** 2026-05-18
**Milestone:** M47

## Context

M6/M6.x shipped Stalwart + Bulwark with SPF/DKIM/DMARC, but the mail
queue is invisible, outbound abuse is undetected (a compromised account
blacklists the shared IP for every tenant), RBL listing is discovered
only via customer complaints, DMARC `rua` is write-only, and there is
no MTA-STS / TLS-RPT (a downgrade-attack + TLS-visibility gap that is
now a baseline expectation). This is the #1 hosting support-ticket
class.

## Decision

Build an operator+tenant deliverability surface **on top of** the
existing stack — not a new MTA:

- **Stalwart is the source of truth** for queue + delivery state, read
  via its admin API at the pinned **`http://127.0.0.1:8446`** (HTTP
  Basic, token from `stalwart-admin.token`; ADR-0045/0050 — the
  earlier `:8080` figure was a stale blueprint value). No new TCP
  loopback (M25 load-bearing). Wire contract pinned by a test against
  a live Stalwart (`feedback_verify_wire_contract`).
- **DB-as-truth (ADR-0002)** for policy: `mail_outbound_policy`,
  `mail_rbl_state`, `dmarc_aggregate`, `mta_sts_policy`,
  `tlsrpt_aggregate` in `jabali_panel`; reconciler/agent converge
  Stalwart config + DNS. Migration is schema-only; seeds are
  application-side (`feedback_migration_data_seed_ordering`).
- **Agent opens no outbound** (ADR-0001/0050): RBL lookups and
  DMARC/TLS-RPT report fetch run in panel-api.
- **Throttle is Stalwart-native** (its rate-limit/sieve), driven by the
  agent writing Stalwart config — not a jabali shim.
- **MTA-STS / TLS-RPT DNS** (`_mta-sts.<d>`, `mta-sts.<d>`,
  `_smtp._tls.<d>`) is emitted through the **existing M15 PDNS DNS
  reconciler** (DNSSEC-signed); the MTA-STS policy file is served by
  the existing nginx vhost stack at
  `https://mta-sts.<d>/.well-known/mta-sts.txt` with a cert from the
  existing per-domain LE path. No new DNS or web path.
- Abuse / RBL / STARTTLS-failure signals feed **M14** (ADR-0056) —
  reuse, don't rebuild.
- MTA-STS default `mode=testing`; promotion to `enforce` is
  operator-gated with a typed confirm + documented rollback
  (`mode=none`) — destructive-op discipline (M48 pattern).

## Consequences

- Operators see + control the queue, outbound abuse, RBL status, DMARC
  and TLS-RPT aggregates, and a per-domain deliverability score.
- Modern SMTP security (MTA-STS/TLS-RPT) without a new subsystem —
  reuses PDNS (M15), nginx, the LE path, and M14.
- Stalwart admin-API coupling is contained behind the agent + a
  contract test; the `:8446` pin is load-bearing and version-checked
  on a live Stalwart before Steps 1/4.
- Migration 000139 (re-checked next-free vs Gitea main, not the
  mirror — the collision scar).

## Advisor amendments (2026-05-18)

- **Stalwart wire surface is a Wave-1 gate, not assumed.** The
  `:8446` figure in this ADR is a candidate, not verified — Stalwart
  0.16.0 returns 404 to unauthenticated mgmt requests (anti-
  enumeration). Before Step 1, the M6 install code is read to find
  the `stalwart-admin.token` path + the listener carrying
  `protocol="http"`, then the surface is live-confirmed and this ADR
  is amended with a concrete **version + port + endpoint** table.
  Endpoint shape (Context7, 0.15; verify 0.16 prefix): `GET
  /queue/messages` → `{data:{items,total,status}}`, `POST
  /queue/retry`, DELETE cancel.
- **MTA-STS (Step 7) splits into 7a (per-domain DNS+vhost+LE-cert
  scaffolding — an LE-rate-limit growth axis) and 7b (policy state
  machine + promotion gating).** Rollback: `mode=none` is reversible
  but bounded by the cached `max_age` (RFC 8461 §5.1); default
  `max_age` starts small (86400).
- **Retention:** `dmarc_aggregate`/`tlsrpt_aggregate` default 90-day,
  pruned app-side via a timer (Steps 6/8); migration 000139 notes it.
- **RBL set is fixed at blueprint time** (Spamhaus ZEN, SpamCop,
  Barracuda BRBL, SURBL; paid via operator-configured creds only;
  cache ≥1 h, poll 4–6 h). **PTR is informational only** (provider-
  controlled; show current vs expected, no "fix" action).
