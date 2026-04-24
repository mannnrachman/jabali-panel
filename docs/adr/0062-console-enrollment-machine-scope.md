# ADR-0062: CrowdSec Console enrollment — enroll-only, operator manages disenroll

**Status:** Accepted — 2026-04-24
**Amended:** 2026-04-25 — exposed `cscli console status/enable/disable` for share preferences (see Amendment section)
**Related:** ADR-0002 (DB as truth for config), ADR-0053 (CrowdSec over fail2ban)

## Context

CrowdSec Console (app.crowdsec.net) is an optional hosted dashboard
that gives operators a cross-instance view of alerts + decisions,
plus access to Community Threat Intelligence (CTI) blocklists.
Free tier exists. Enrollment is machine-scoped — one instance binds
to one Console account via an enrollment key.

M26 runbook documents the manual `cscli console enroll <key>`
workflow. Operators have asked for a one-button UI so they don't
need ssh. M27 Step 4 ships that button.

## Decision

Ship enroll-only. No disenroll verb, no server-side enrollment-state
detection, no polling.

### Why enroll-only

The M27 pre-flight probe against CrowdSec v1.7.7 revealed:

1. **No `cscli console disenroll` subcommand exists.** Console's
   subcommand set is `{disable, enable, enroll, status}`. "disable"
   turns off console options (custom/manual/tainted/context/
   console_management), not enrollment itself. Disenroll is operator-
   initiated from app.crowdsec.net (remove instance from the account).

2. **Enrollment state is not reliably distinguishable from config
   files.** `/etc/crowdsec/online_api_credentials.yaml` is populated
   by the baseline CAPI auto-registration whether Console enrollment
   happened or not — the `login`/`password` fields look identical
   pre- and post-enrollment. `cscli console status -o json` returns a
   table of console-OPTIONS (share preferences), not enrollment
   boolean.

   jabali could mirror state in a `server_settings.crowdsec_console_
   enrolled` flag, but that invites drift: operator runs
   `cscli console enroll` or `cscli capi register` from the host →
   flag is wrong. Easier: don't lie.

### Workflow

1. Operator creates account at app.crowdsec.net, gets enrollment key
2. Paste key into UI, click Enroll (with optional display name)
3. UI shows success + instruction to accept the pending instance at
   app.crowdsec.net
4. Operator accepts in the web UI; machine becomes visible in Console

Disenroll: operator clicks "Remove instance" in app.crowdsec.net.
Fully destructive disenroll (rotate online_api_credentials + restart
crowdsec) is documented in the runbook as a manual recipe for the
rare operator who wants to burn the link.

### Wire contract

- `POST /admin/security/crowdsec/console/enroll` body
  `{key, name?: string, enable?: string[], disable?: string[]}` →
  `{pending: true}`

No GET, no DELETE. Card is one-shot.

### cscli invocation shape (probed 2026-04-24)

```
cscli console enroll <key> [--name <instance_name>]
                          [--enable <option>] [--disable <option>]
                          [--tags <tag>]...
```

Agent validates `key` is non-empty alnum-plus-dashes (enrollment
keys are long alnum strings, but the regex is a cheap client-side
sanity check; cscli validates server-side).

### No DB writes

jabali DB gets no new columns for this feature. CrowdSec manages all
enrollment state in `/etc/crowdsec/online_api_credentials.yaml`.

## Consequences

### Good

- Smallest possible feature surface; no drift risk
- Enrollment token never persisted by jabali (operator pastes → agent
  runs cscli → no in-memory copy after the call returns)
- No background polling cost

### Neutral

- Operator must visit app.crowdsec.net to accept + verify + disenroll.
  Runbook documents the walkthrough

### Risks

- Enrollment key is a credential. Agent receives it over the
  authenticated control-plane RPC; key is passed as argv to cscli
  (visible in /proc for the duration of the call). Mitigation: call
  is short-lived; no log line records the key

## Implementation

- Agent handler `security.crowdsec.console.enroll` in
  `panel-agent/internal/commands/security_crowdsec.go`
- Panel-api route `POST /admin/security/crowdsec/console/enroll`
- UI card `ConsoleCard` on the CrowdSec tab — single Input + Button,
  with success Alert pointing to app.crowdsec.net

## Amendment — 2026-04-25: expose share preferences

Original scope shipped enroll-only. Operator feedback: "cscli has
`status`/`enable`/`disable` for the five share options — why hide them
in the UI when we already expose enroll?" Valid — the rest of the
cscli console surface is just preference toggles, same DB-less
passthrough as enroll.

Added:
- `security.crowdsec.console.status` — wraps `cscli console status -o json`;
  returns `{items: [{name, enabled, description}]}` for the five options
  (custom / manual / tainted / context / console_management).
- `security.crowdsec.console.enable` — `cscli console enable <option>`.
- `security.crowdsec.console.disable` — `cscli console disable <option>`.

Panel-api routes:
- `GET  /admin/security/crowdsec/console/status`
- `POST /admin/security/crowdsec/console/options/:option/enable`
- `POST /admin/security/crowdsec/console/options/:option/disable`

UI `ConsoleCard` now includes a share-preferences Table below the
enroll form with an inline Switch per option. Toggles apply
immediately (no Apply button — per-row mutation same as the ModSec
per-domain table).

Core decision unchanged: no server-side enrollment-state detection,
no disenroll verb. Share preferences function whether or not Console
is enrolled (they gate local forwarding). Pre-enrollment they're
effectively inert; post-enrollment they shape what reaches Console.
