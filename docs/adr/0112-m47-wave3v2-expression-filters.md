# ADR-0112 — M47 Wave 3 v2: Stalwart Expression filters + Wave 9b per-domain widget

**Status:** Accepted
**Date:** 2026-05-20
**Amends:** ADR-0111 (Wave 3 always-fire match deferral)

## Context

ADR-0111 deferred Stalwart Expression filtering — Wave 3 v1 shipped
every throttle with an always-fire match, which meant `key=sender` /
`key=senderDomain` GROUPED by axis but couldn't FILTER to a specific
mailbox or domain. An admin setting "100 msgs/hour for
user@example.com" actually capped EVERY user collectively at 100/hour
— wrong, dangerous, undocumented.

A second .150 spike pinned Stalwart's Expression `match` grammar via
introspection of MtaStageData's `addAuthResultsHeader` default
(`{"match":{"0":{"if":"local_port == 25","then":"true"}},"else":"false"}`).
Round-trip `create → get → delete` on a real MtaOutboundThrottle
confirmed the shape works on outbound throttles too.

## Decision

1. **`scope_ref` widened to `VARCHAR(320)`** (mig 000145). v1 was
   `CHAR(26)` for ULID; v2 stores the literal sender address (RFC
   5321 max 320 bytes) or sender domain string. No data loss — v1
   rows behaved as global throttles regardless of scope_ref content.
2. **Two new Match constructors** in `internal/stalwartadmin/throttle.go`:
   - `NewSenderFilterMatch(addr)` → `sender == '<addr>'`
   - `NewSenderDomainFilterMatch(d)` → `sender_domain == '<d>'`
   Both wrap-pin the live-verified JSON shape.
3. **Reconciler emits the filter** when `scope_ref` is set:
   - `scope=user`   + scope_ref → key=`sender` + sender filter
   - `scope=domain` + scope_ref → key=`senderDomain` + sender_domain filter
   - `scope=global` OR missing scope_ref → always-fire (backwards-compat)
4. **API handler validates `scope_ref` with regex** before persistence
   (`scopeRefEmailRe` / `scopeRefDomainRe`) AND rejects any value
   containing `'` or `\`. The reconciler embeds the literal verbatim
   into the expression string; a stray quote escapes the literal and
   degrades the throttle into always-fire (or always-skip) — a real
   security boundary, not a UX nicety.
5. **Wave 9b per-domain widget** — `/api/v1/admin/mail/deliverability`
   gains an optional `?domain=<name>` query that narrows DMARC + TLS-RPT
   counts to one domain. RBL stays server-wide (the IP is shared
   across all hosted domains) so it's omitted from the per-domain
   view. New UI section `DomainDeliverabilitySection` mounts in
   `DomainEdit` alongside MTA-STS.

## Trade-offs

- **Validator regex tightness**: domain regex rejects uppercase
  (RFC says case-insensitive but our scope_ref is the lookup key —
  forcing lowercase avoids two rows for `Example.com` vs
  `example.com`). UI input lowercases before POST.
- **No paired daily+hourly throttles** — Stalwart's rate object
  still takes ONE window per throttle. Splitting into paired
  Stalwart rows is a separate follow-up (would double-write per row
  + need a new `stalwart_id_daily` column).
- **No multi-condition expressions** — match is a single-rule object
  (`"0": {if, then}`). Multi-OR conditions (`sender == 'a' || sender == 'b'`)
  fit in one `if` expression-string. Multi-AND across different keys
  (e.g. throttle only when local_port == 25 AND sender_domain == X)
  is doable via expression string but not exposed in v2 UI.

## Verification

- `TestNewSenderFilterMatch_WireShape` + `TestNewSenderDomainFilterMatch_WireShape`
  pin EXACT JSON bytes against the .150-verified payload.
- `TestNewSenderFilterMatch_EmbedsRawInput` pins that the constructor
  is deliberately unescaped (forcing the API-layer guard).
- `TestThrottlePayloadFor_PerUserEmitsSenderFilter` /
  `_PerDomainEmitsSenderDomainFilter` / `_GlobalKeepsAlwaysFire` /
  `_UserScopeWithoutRefFallsBackToAlwaysFire` pin reconciler behaviour
  across all four scope branches.
- `TestValidateScopeRef_RejectsInjectionShapes` pins 12 injection
  shapes get rejected (empty, no-@, spaces, quotes, backslashes,
  uppercase, leading-dot, embedded expression operators).
- `TestValidateScopeRef_AcceptsValid` pins normal addresses + domains
  pass.
- Live .150 spike: `create MtaOutboundThrottle` with sender filter →
  `query` shows the throttle → `get <id>` confirms match shape
  serialised exactly as our Go marshal → `delete` cleans up.
