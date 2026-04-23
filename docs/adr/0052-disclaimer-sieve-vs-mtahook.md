# ADR-0052: Disclaimer — Sieve System Script with MtaHook Fallback Deferred

**Status:** Provisional (2026-04-23) — first ship uses sieve text/plain only; final decision pending live spike.
**Supersedes:** none.
**Related:** ADR-0051 (M6.5 DB-as-truth), ADR-0045 (Stalwart v0.16 pivot), M25 unix socket lockdown.

## Context

M6.5 Step 6 adds per-domain outbound disclaimer. The feature appends a text
block to outgoing mail from a domain. Two integration paths exist on Stalwart:

1. **Sieve system script** (`x:SieveSystemScript`): native, declarative, runs
   at outbound data stage. Requires `body` + `replace` + `foreverypart` sieve
   extensions to touch message parts. Stalwart schema confirms support.

2. **MTA hook** (`x:MtaHook`): HTTP callback invoked at a named SMTP stage.
   Stalwart POSTs the message; panel-api returns a modified body.

## Problem

The sieve `replace` action's behaviour on HTML body parts is undocumented for
Stalwart v0.16. RFC 5228 and RFC 5173 (body + foreverypart) describe
text-oriented matching; an HTML `text/html` part may or may not be writable
via `replace text:` portably.

The MtaHook fallback hits a separate problem: M25 (shipped 2026-04-23) closed
all loopback TCP ports in favour of unix sockets. Panel-api listens on
`/run/jabali-panel/api.sock`. If `x:MtaHook` only accepts `http://host:port/`
URLs and not `unix://`, the fallback path re-opens a loopback TCP port —
architectural conflict with M25.

## Decision (first ship)

**Ship sieve path for `text/plain` parts.** The rendered script matches
outbound mail by envelope-from domain, iterates every MIME part, and appends
the disclaimer to `text/plain` parts only. The implementation lives in
`panel-agent/internal/commands/domain_disclaimer_apply.go:renderDisclaimerSieve`.

**Defer HTML coverage + final decision pending two live spikes on 192.168.100.13:**

- **Spike A**: can sieve `replace` modify an HTML body part?
- **Spike B** (only if A fails): does `x:MtaHook` accept `unix://` URLs? If
  yes, host the endpoint on the existing panel-api unix socket. If no, HTML
  disclaimer coverage defers to M6.6 with a local Milter/MtaHook broker as a
  separate daemon (still no loopback TCP under any circumstances — M25 is
  load-bearing).

## Decision matrix

| Spike A | Spike B | Action |
|---------|---------|--------|
| passes | — | Sieve path, ship HTML in follow-up PR |
| fails | unix:// supported | MtaHook path via panel-api unix socket (M6.6) |
| fails | TCP-only | Defer disclaimer HTML coverage to M6.6 broker daemon |

## Consequences

- First ship is honest: text/plain-only disclaimers, UI copy says so, runbook
  documents the gap.
- No new loopback port opened. M25 invariant preserved.
- Reconciler still converges state every tick; disabling the disclaimer
  destroys the system sieve script. No stuck state.
- Operator manual edits to the script in Stalwart admin console are drift;
  reconciler overwrites on next tick (ADR-0051).

## Follow-up

Live spike tasks belong to M6.6. Until then, UI disclaimer tab + ADR describe
the limitation; reconciler phase `m65_disclaimer` pushes state idempotently.
