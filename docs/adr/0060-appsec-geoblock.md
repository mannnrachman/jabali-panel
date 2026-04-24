# ADR-0060: AppSec geoblock (server-wide country filter)

**Status:** Accepted — 2026-04-24
**Supersedes:** —
**Related:** ADR-0053 (CrowdSec over fail2ban), ADR-0054 (UFW), ADR-0055 (ModSecurity)

## Context

M26 Step 7 shipped admin UI for CrowdSec decisions + bouncers + hub.
Decisions operate at L3/L4 via `crowdsec-firewall-bouncer` (iptables /
nftables). That's the right layer for transient ad-hoc bans
("block 203.0.113.7 for 4h") but a heavy hammer for standing policy
like "never accept HTTP from CN for this server."

CrowdSec also ships an AppSec engine that evaluates incoming HTTP
requests via `pre_eval` hook expressions before routing into application
rules. Upstream docs include a worked example:
https://doc.crowdsec.net/docs/next/appsec/rules_examples/#5-geoblocking

```yaml
pre_eval:
  - filter: IsInBand == true && GeoIPEnrich(req.RemoteAddr)?.Country.IsoCode not in ["FR", "US", ""]
    apply:
      - DropRequest("Forbidden Country")
```

Operators have asked for country-level geoblock. Two paths:

**Path A — `cscli decisions add --scope Country --value RU`** (already
shipped). Issues a LAPI decision with scope=country. The firewall-bouncer
translates the decision into iptables rules keyed off CrowdSec's internal
GeoIP DB. Works at L3/L4, drops TCP before nginx sees it. Short TTL by
design — every decision has an expiry.

**Path B — AppSec `pre_eval` rule.** YAML rule file loaded by CrowdSec's
AppSec engine. Nginx proxies requests to the AppSec endpoint via
`auth_request`; AppSec evaluates `GeoIPEnrich(req.RemoteAddr)?.Country.IsoCode`
and replies 200 (pass) or 403 (block). Standing policy, no TTL, reloadable
without dropping connections.

Both are legitimate and coexist:

| | scope=Country decision (A) | AppSec rule (B) |
|---|---|---|
| Layer | L3/L4 (iptables) | L7 (nginx subrequest) |
| Granularity | Whole host | Per-vhost (opt-in via nginx) |
| TTL | Yes (expiry required) | No (standing policy) |
| Activation | `cscli decisions add` / admin Decision Drawer | YAML rule + admin AppSec card |
| Typical use | Temporary block during active abuse | "Never accept HTTP from these ASNs/countries" |
| Reversibility | `cscli decisions delete` / TTL expiry | Edit YAML + reload |

M26 already ships Path A end-to-end (decisions with scope country).
This ADR adds Path B for standing policy.

## Decision

Ship a server-wide AppSec geoblock rule, administered via the
`/jabali-admin/security?tab=crowdsec` page.

### Scope — server-wide only (not per-domain)

Per-domain country policy is deferred. Reasons:
- Keeps the first iteration simple — one YAML rule, one DB row, one UI card
- Most operators ban at the network perimeter; differential per-site
  policy is an optimisation
- ModSec already covers per-domain L7 policy for fine-grained WAF rules

Future work could add a `per_domain_overrides` column or a separate
rule file per vhost — out of scope for M26.

### Modes

Three-state enum: `off`, `allow`, `deny`.

- `off` — rule file present + parseable but emits no `pre_eval` hooks.
  Crowdsec keeps it loaded so a flip to allow/deny is a reload, not an
  install.
- `allow` — pass only when `Country.IsoCode in [<list>, ""]`. Empty
  string is always included so private IPs (RFC 1918, loopback) where
  GeoIP can't resolve still pass — otherwise an admin turning on allow
  mode locks out their own health checks.
- `deny` — reject when `Country.IsoCode in [<list>]`. No empty string —
  unresolvable IPs pass through for this mode (don't second-guess
  GeoIP gaps as "might be Russia").

### Enforcement path

`install.sh install_crowdsec_appsec` drops the AppSec acquisition
config + base collection + initial off-mode rule. The engine listens
on TCP `127.0.0.1:7422` (loopback only — no public bind).

`install.sh install_crowdsec_nginx_bouncer` installs the upstream
`crowdsec-nginx-bouncer` package and registers a pinned bouncer named
`jabali-nginx`. The bouncer hooks nginx's `access_by_lua` phase, so
every vhost is protected with no per-server snippet. Config:

```
ENABLED=true
API_URL=              # empty — LAPI stays socket-only via firewall-bouncer
API_KEY=<generated>
APPSEC_URL=http://127.0.0.1:7422
APPSEC_FAILURE_ACTION=passthrough
ALWAYS_SEND_TO_APPSEC=true
SSL_VERIFY=false
```

### Why TCP loopback, not unix socket

The bouncer's Lua HTTP client (`lua-resty-http request_uri`) speaks TCP
only — no unix socket support. We considered keeping AppSec on a unix
socket + a custom `auth_request` proxy shim (nginx can speak unix
sockets), but that sacrifices the upstream-maintained Lua bouncer for
a hand-rolled wiring we own. TCP loopback on `127.0.0.1:7422` keeps
the official bouncer path while staying consistent with ADR-0050's
"no public ports" principle — the listener is reachable only from the
host itself.

### Why `API_URL=` (empty)

The nginx-bouncer can both fetch LAPI decisions *and* forward requests
to AppSec. We only want AppSec — LAPI enforcement already happens at
L3/L4 via `crowdsec-firewall-bouncer`, and duplicating it at L7 would
double-query LAPI on every request. Empty `API_URL` + populated
`APPSEC_URL` tells the bouncer to skip LAPI and run AppSec-only, which
is exactly the split we want.

### Data model

Authoritative state lives in `server_settings`:

```
appsec_geoblock_mode      VARCHAR(10)   NOT NULL DEFAULT 'off'
appsec_geoblock_countries VARCHAR(1000) NOT NULL DEFAULT ''
```

Comma-separated ISO 3166-1 alpha-2 codes. 1000-char cap comfortably
covers the full ISO list (~250 codes × 3 chars with separators ≈ 750).

Agent writes `/etc/crowdsec/appsec-rules/jabali-geoblock.yaml` with
two `# jabali-...` comment markers so the `get` handler can read
mode+countries back without a YAML round-trip.

### Reload strategy

`systemctl reload crowdsec` (SIGHUP) re-reads rule files without
dropping the LAPI socket. Fall back to `restart` if reload fails
(older packaging without `ExecReload`).

## Consequences

### Good

- Standing country policy without creating + re-creating decisions
- Coexists cleanly with scope=Country decisions (operator picks based on
  whether the ban is transient or permanent)
- Single YAML rule = easy to inspect + grep from the host
- DB-as-truth stays intact (per ADR-0002)
- Upstream-maintained nginx bouncer: protection applies to every vhost
  automatically, no per-server snippet to maintain, no risk of operator
  forgetting to include it on a new site

### Neutral

- AppSec evaluation adds a Lua HTTP round-trip per request — measurable
  latency on high-RPS hosts. `APPSEC_FAILURE_ACTION=passthrough` means
  bouncer-↔-engine connectivity failures fail open (availability over
  strictness).
- TCP loopback listener on `127.0.0.1:7422` — not publicly bound but
  adds one more local port. Acceptable per ADR-0050 (loopback ≠ public).

### Risks

- **Allow-list misconfiguration can self-lock the admin.** The UI warns
  when an allow mode has no countries (blocks everything). Empty-string
  tolerance (private IPs pass) covers the common localhost-healthcheck
  case, but not the "operator in hotel WiFi connecting from
  unexpected geo" case. Mitigation: operator accesses the UI before
  locking + keeps an SSH backdoor
- **GeoIP DB freshness.** CrowdSec's MaxMind DB updates on its cron;
  IP → country mapping can be stale. Acceptable — country bans are
  coarse-grained by design.

## Implementation

- Migration `000067_add_server_settings_appsec_geoblock.up.sql`
- Model field `AppSecGeoblockMode` + `AppSecGeoblockCountries`
- Agent commands `security.crowdsec.appsec.geoblock.{get,set}`
- Panel-api `GET/PUT /admin/security/crowdsec/appsec/geoblock`
  via `RegisterSecurityAppSecRoutes`
- UI card `AppSecGeoblockCard` on the CrowdSec tab
- install.sh helpers:
  - `install_crowdsec_appsec` — AppSec engine on `127.0.0.1:7422` +
    acquisition config + base collection + initial off-mode rule
  - `install_crowdsec_nginx_bouncer` — upstream bouncer package +
    pinned `jabali-nginx` bouncer + AppSec-only config
- Runbook section on admin UI usage + verification commands
