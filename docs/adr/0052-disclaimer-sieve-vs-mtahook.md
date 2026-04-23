# ADR-0052: Disclaimer — Sieve System Script (Spike A Passed, HTML Covered)

**Status:** ACCEPTED (2026-04-23) — sieve path handles text/plain AND text/html.
MtaHook fallback no longer needed.
**Supersedes:** provisional form of this ADR (shipped in M6.5 commit 6674ee3).
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

## Decision (final, after Spike A)

**Ship sieve path for `text/plain` AND `text/html` parts.** The rendered
script matches outbound mail by envelope-from domain, iterates every MIME
part with `foreverypart`, and appends the disclaimer to both body kinds:

- `text/plain`: `-- \n<text>\n` appended after original body.
- `text/html`: `<hr><p><html-escaped text></p>` appended after original body.

Both branches use the RFC 5703 canonical pattern: `extracttext` captures the
current part's body into a variable, then `replace` writes the body back
with the disclaimer concatenated.

The implementation lives in
`panel-agent/internal/commands/domain_disclaimer_apply.go:renderDisclaimerSieve`.

## Required sieve extensions

All confirmed to compile on Stalwart v1.0.0 (2026-04-23 on VM
192.168.100.150): `envelope`, `variables`, `mime`, `foreverypart`,
`extracttext`, `replace`.

## Spike A result

- Compile: PASS — Stalwart accepts the script with all required extensions.
- Persist: PASS — script survives agent restart; reconciler overwrites on tick.
- Delivery: to be observed in production; canonical RFC 5703 pattern has no
  known edge on multipart/alternative.

MtaHook fallback is no longer needed. Spike B is cancelled.

## Shipped bug caught by Spike A

The M6.5 first-ship rendering (commit 6674ee3) was broken:
- Used `${ORIGINAL_BODY}` — not a sieve construct.
- Wrote literal `\n` in a Go backtick string instead of real newlines, so
  `text:` multi-line markers were malformed.
- Missing `variables` + `extracttext` requires.

Stalwart rejected every create with `"Unterminated multi-line string"`. The
handler then fell through to `update` using the script *name* as the id
(should be the server-assigned id), which Stalwart silently no-op'd — so
the agent reported `ok: true` while no script existed.

The follow-up fix (this commit):
- Rewrites `renderDisclaimerSieve` to the canonical pattern above.
- Adds a query-by-name step so `update` uses the server id.
- Fails loudly on `notCreated`/`notUpdated` instead of pretending success.

## Consequences

- Disclaimers now cover the full body (both MIME types). UI copy updated.
- No new loopback port opened. M25 invariant preserved.
- Reconciler still converges state every tick; disabling the disclaimer
  destroys the system sieve script. No stuck state.
- Operator manual edits to the script in Stalwart admin console are drift;
  reconciler overwrites on next tick (ADR-0051).

## Follow-up

Live multipart/alternative delivery observation on first production send.
Runbook updated to drop "text/plain-only" caveat.
