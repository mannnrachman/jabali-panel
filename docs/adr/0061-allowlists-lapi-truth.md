# ADR-0061: CrowdSec allowlists — LAPI is truth, no DB mirror

**Status:** Accepted — 2026-04-24
**Related:** ADR-0002 (DB as truth for config), ADR-0053 (CrowdSec over fail2ban), ADR-0060 (AppSec geoblock)

## Context

M26 shipped admin UI for CrowdSec decisions, bouncers, and hub. One
load-bearing absence: there's no way from the panel to tell LAPI
"never ban this IP no matter what scenario fires." Every allowlist
workflow today is `ssh + cscli decisions add --type trusted`, which
conflates an explicit allow with a fake 3650d ban — and it doesn't
apply to AppSec (M26 Step 9 / ADR-0060) because AppSec evaluates
before LAPI sees the request.

CrowdSec 1.6+ ships a first-class `cscli allowlists` subcommand family
with its own LAPI table. Allowlists are evaluated ahead of scenarios
and decisions — a match means "skip everything else for this IP." The
upstream design:

- Named allowlist (description is free text)
- Zero-or-more values (IP or CIDR per value)
- Per-value comment (free text)
- Optional expiration per value

Once we adopt allowlists, the operator can add their office IP or CI
runner CIDR and stop worrying about an over-eager scenario locking
them out.

## Decision

Ship a server-wide "jabali-admin-allowlist" administered via the
`/jabali-admin/security?tab=crowdsec` page.

### Source of truth — LAPI, not jabali DB

Allowlists live in LAPI's SQLite DB the same way decisions and
bouncers do. jabali does NOT mirror them in MariaDB. The admin UI
hits the agent, the agent shells out to `cscli allowlists`, and cscli
reads/writes LAPI directly.

This is the same rule ADR-0002 drew around decisions: **runtime
state** (decisions, bouncers, alerts, allowlists) stays in the
service that owns it. **Config state** (per-domain modsec toggle,
AppSec geoblock mode, captcha creds) lives in `server_settings` or
per-resource tables in jabali DB.

Mirroring allowlists in jabali DB would invite the drift problem
(operator runs `cscli allowlists add` from the host → jabali UI
disagrees with reality) with no benefit — every jabali allowlist read
goes through the agent anyway, so a DB mirror is pure overhead.

### Single server-wide allowlist — scope deferred

First cut ships ONE allowlist called `jabali-admin-allowlist`. Created
on first add if missing. All admin-managed entries live in it.

Per-domain allowlists or multi-allowlist UIs are deferred. Reason: same
as AppSec geoblock (ADR-0060) — the first iteration should be
server-wide only. Operators who need differential-per-site policy can
`cscli` from the host.

### Wire contract

- `GET /admin/security/crowdsec/allowlists` → `{items: [{value, reason, created_at}]}`
- `POST /admin/security/crowdsec/allowlists` body `{value, reason}` → 201 `{value}`
- `DELETE /admin/security/crowdsec/allowlists?value=<urlencoded>` → 204

`value` is IP or CIDR. Agent validates via `net.ParseIP` or
`net.ParseCIDR` before shelling to `cscli` — defense-in-depth even
though cscli also validates.

DELETE uses a query parameter (not `:value` path segment) because
Gin's `:value` matches a single path segment — `192.0.2.0/24` would
route to `value=192.0.2.0` and 404 the `/24` tail. Query param dodges
segmentation without introducing `*value` wildcard quirks.

### cscli subcommand shape (probed 2026-04-24, CrowdSec v1.7.7)

```
cscli allowlists create <name> -d "<description>"
cscli allowlists add    <name> <value> [-d "<comment>"]
cscli allowlists remove <name> <value>
cscli allowlists inspect <name> -o json
```

Comment flag is `-d`, not `--reason`. Agent maps jabali's `reason`
field to `-d` on the CLI.

## Consequences

### Good

- Admin-first allow workflow; no ssh required
- LAPI stays authoritative (no drift)
- Plays nice with AppSec: CrowdSec evaluates allowlists before AppSec
  pre_eval, so an allowlisted IP bypasses geoblock as well as scenarios

### Neutral

- "jabali-admin-allowlist" name is baked into the agent handler;
  operators who want multiple allowlists still need cscli from host

### Risks

- An over-wide CIDR (e.g., 0.0.0.0/0) disables all protection.
  Mitigation: agent validates via `net.ParseCIDR` only — no semantic
  check against CIDR width. UI adds a Popconfirm warning for any CIDR
  with prefix length ≤ 16.

## Implementation

- Agent handlers `security.crowdsec.allowlists.{list,add,remove}`
  in `panel-agent/internal/commands/security_crowdsec.go`
- Panel-api routes in `RegisterSecurityCrowdsecRoutes`
- UI card `AllowlistsCard` on the CrowdSec tab, above the Decisions table
