# M26 Security Tab — Runbook

Operational reference for the admin Security tab shipped in Steps 1–9
(2026-04-24). See `plans/m26-security-tab.md` for the full plan and
ADRs 0053 (CrowdSec) + 0054 (UFW). ADR-0055 (ModSecurity-per-domain)
was SUPERSEDED on 2026-04-26 — CrowdSec AppSec covers the WAF role
(see ADR-0060 + `plans/m27-crowdsec-extensions.md`); the entire ModSec
stack (apt packages, nginx module, schema columns, panel UI tab) was
removed. This runbook still covers the M26 foundation; the M27
extensions live in their own section below.

## What it is

Two-tab admin page at `/jabali-admin/security`:

| Tab | Purpose | Backed by |
|-----|---------|-----------|
| CrowdSec | View metrics + active decisions; add/remove manual bans; allowlists; alerts; Console enroll/disenroll; per-scenario remediation; AppSec geoblock; bouncers; hub | LAPI on `/run/crowdsec/api.sock` (mode 0660 group `jabali`); `cscli` shell-out for hub/list/console/profiles operations; AppSec listener on `127.0.0.1:7422` |
| UFW | Status + rules; add/delete rules; enable/disable firewall (typed-YES gate) | `ufw status numbered verbose`, `ufw allow/deny`, `ufw enable/disable` |

## Architecture at a glance

```
SPA  ──► panel-api /api/v1/admin/security/*  ──UDS──►  panel-agent
         (RequireAdmin gate)                            (shells out to ufw / cscli;
                                                         rewrites jabali-appsec.yaml
                                                         and profiles.yaml; reloads
                                                         crowdsec/nginx as needed)
```

Every destructive action (UFW enable/disable, mass rule changes) requires
`{"confirm":"YES"}` in the agent payload AND a typed-YES Modal in the UI.

## Install smoke

After a fresh install or `jabali update`:

```bash
# 1. CrowdSec running on unix socket
test -S /run/crowdsec/api.sock && echo OK
sudo -u jabali cscli decisions list -o json | head -1

# 2. UFW present + status (initial state: inactive on fresh install)
ufw status verbose | head -3

# 3. AppSec engine on TCP loopback :7422 + jabali-appsec config loaded
ss -tlnp | grep ':7422'
cat /etc/crowdsec/appsec-configs/jabali-appsec.yaml | head -10
# Expect: inband_rules → base-config + vpatch-* + generic-*

# 4. ModSecurity is GONE on healthy hosts
dpkg -l 2>/dev/null | grep -E 'libnginx-mod-http-modsecurity|^ii.*modsecurity-crs' && echo "STALE: run install.sh cleanup_modsecurity"
test ! -d /etc/nginx/modsec && echo OK

# 5. Panel API surface alive
curl -k --unix-socket /run/jabali-panel/api.sock \
  http://x/api/v1/admin/security/ufw/status \
  -H "Cookie: <admin-session>"
```

## Common operations

### Enabling UFW for the first time

UFW is installed but inactive on fresh systems (Step 1 explicitly leaves
the firewall off so install scripts don't lock the operator out).

To enable:

1. SSH into the host (so you have one good session in case the next
   step does something unexpected).
2. `/jabali-admin/security?tab=ufw` → review existing rules → confirm
   port 22/tcp is `allow` for the SSH source you trust. If absent, add
   `allow 22 tcp from <trusted-cidr>` first.
3. Click **Enable firewall** → type `YES` → submit.
4. From a SECOND terminal, verify SSH still works.
5. If locked out, recover via the host console — see "UFW lockout
   recovery" below.

### WAF — replaced by CrowdSec AppSec

ModSecurity was removed 2026-04-26. The WAF role is now CrowdSec AppSec
(`/etc/crowdsec/appsec-configs/jabali-appsec.yaml` + listener on
`127.0.0.1:7422` + nginx-bouncer in-band dial). Default rule set:

- `crowdsecurity/base-config` (plumbing)
- `crowdsecurity/vpatch-*` (CVE virtual-patching corpus)
- `crowdsecurity/generic-*` (CRS-style XSS / SSTI / WordPress upload)

The geoblock pre_eval hook in this same file is operator-managed via
`/jabali-admin/security?sub=appsec` (mode + countries). Removing
either rule glob is operator-supported via the Recommended hub picker
on `/jabali-admin/security?sub=hub`.

### Banning in CrowdSec — IP, range, country, AS

The "Add decision" Drawer on the CrowdSec tab supports all four scopes
CrowdSec exposes. Pick a scope, enter the value, set duration + reason.

| Scope | Example value | cscli equivalent |
|-------|---------------|------------------|
| IP | `203.0.113.7` | `cscli decisions add --scope Ip --value 203.0.113.7 --duration 4h --reason manual` |
| Range (CIDR) | `203.0.113.0/24` | `cscli decisions add --scope Range --value 203.0.113.0/24 --duration 4h --reason manual` |
| Country | `RU` (ISO 3166-1 alpha-2) | `cscli decisions add --scope Country --value RU --duration 24h --reason geo-block` |
| AS (ASN) | `AS64500` or `64500` | `cscli decisions add --scope AS --value 64500 --duration 24h --reason asn-block` |

Country + AS bans require the GeoIP + ASN enrichers. Both are installed
by default on fresh CrowdSec hosts via the `crowdsecurity/linux`
collection. Verify with `cscli parsers list | grep -E 'geoip|asn'`. If
missing: `cscli parsers install crowdsecurity/geoip-enrich` +
`cscli parsers install crowdsecurity/asn-enrich` + `systemctl restart crowdsec`.

Bouncers pick up new decisions on next LAPI pull (~30s with stock
config). The firewall-bouncer translates IP/range decisions into
iptables/nftables; country + AS decisions need a bouncer that
supports them (the default `crowdsec-firewall-bouncer` does).

Removing: click **Delete** on the decision row → confirm. Same as
`cscli decisions delete --id <id>` from the host.

### AppSec geoblock — server-wide country filter (ADR-0060)

Admin UI card on the CrowdSec tab writes
`/etc/crowdsec/appsec-rules/jabali-geoblock.yaml` + reloads crowdsec.
Three modes:

- **Off** — rule loaded but inert
- **Allow-list** — only requests from listed countries pass (plus empty,
  so private IPs + unresolvable geo still work)
- **Deny-list** — requests from listed countries → 403

Enforcement runs automatically. `install_crowdsec_appsec` starts the
AppSec engine on `127.0.0.1:7422`; `install_crowdsec_nginx_bouncer`
installs the upstream `crowdsec-nginx-bouncer` package, registers a
pinned bouncer named `jabali-nginx`, and writes
`/etc/crowdsec/bouncers/crowdsec-nginx-bouncer.conf` with
`APPSEC_URL=http://127.0.0.1:7422` + `ALWAYS_SEND_TO_APPSEC=true` +
`APPSEC_FAILURE_ACTION=passthrough`. The bouncer hooks nginx's
`access_by_lua` phase, so every vhost is protected with no per-server
wiring.

`API_URL=` (empty) — LAPI stays socket-only through the firewall-bouncer.
The nginx-bouncer talks AppSec-only over the loopback TCP port. No
public port is opened; port 7422 is localhost-bound.

To verify a country ban is taking effect:

```bash
# Resolve an IP in the blocked country via a public GeoIP source.
# Then from a host in that country (or via a proxy):
curl -I https://your-panel.example.com/
# Expect: HTTP/2 403
```

CrowdSec AppSec metrics expose hit counts:

```bash
cscli metrics show appsec
```

**Troubleshooting:**

- Requests pass that shouldn't → check
  `cscli parsers list | grep geoip-enrich` — enricher must be installed
  (install.sh does this, but a manual `cscli parsers remove …` regresses).
- All traffic 403 / nginx error log shows bouncer connect failures →
  verify AppSec is listening: `ss -lnt 'sport = :7422'` and
  `journalctl -u crowdsec -n 50`. `APPSEC_FAILURE_ACTION=passthrough`
  means bouncer-↔-engine connectivity failures fail open, so 403s on
  every request mean the rule is evaluating and denying — check
  `cscli metrics show appsec` for drop counts.
- Bouncer not enforcing at all → `cscli bouncers list` should show
  `jabali-nginx`; `nginx -T | grep -i crowdsec` should show the Lua
  access hook active.
- Geoblock locked you out → edit
  `/etc/crowdsec/appsec-configs/jabali-appsec.yaml` on the host, set
  `# jabali-mode: off` in the header, wipe the `pre_eval:` block, then
  `systemctl reload crowdsec`. UI then shows off-mode. (Earlier path
  `/etc/crowdsec/appsec-rules/jabali-geoblock.yaml` was wrong — pre_eval
  hooks live in appsec-CONFIG, not appsec-rules.)

### Enrolling in CrowdSec Console (optional) — M27

CrowdSec Console gives you a hosted dashboard + CTI community
blocklists. Free tier exists. Optional — out of the box CrowdSec
runs fully self-contained.

**From the admin UI** (M27 Step 4, ADR-0062):
1. Create a free account at https://app.crowdsec.net/security-engines?distribution=linux,
   grab the enrollment key
2. Open the "Console" sub-tab on the CrowdSec tab, paste key, click Enroll
3. Accept the pending instance in the Console web UI
4. UI flips to "Enrolled as `<login>`" view (queries `/etc/crowdsec/online_api_credentials.yaml`
   + `cscli capi status`); share-preferences toggles appear inline

**Disenroll + re-enroll with a different key** (added 2026-04-26):
The Console card now shows a red **Disenroll** button when enrolled.
Click → typed-confirm → agent runs `rm /etc/crowdsec/online_api_credentials.yaml`
+ `systemctl reload crowdsec`. Card flips back to the enroll form so
you can paste a fresh key. CLI equivalent:

```bash
sudo rm /etc/crowdsec/online_api_credentials.yaml
sudo systemctl reload crowdsec
sudo cscli console enroll <new-key>
```

cscli has no `console disenroll` verb — wiping the credentials file
is the upstream-recommended path (ADR-0062 amended 2026-04-26).

### M27 — Allowlists, Alerts, Captcha, Per-scenario overrides

The CrowdSec tab gained four cards beyond M26's status/metrics/decisions:

**Allowlist (never ban)** — ADR-0061. Server-wide IP/CIDR list. jabali
maintains one allowlist named `jabali-admin-allowlist` via `cscli
allowlists`; LAPI is truth. Use it to add your office IP, home CIDR,
or CI runner range so no scenario or AppSec rule can ever 403 you.
Allowlists evaluate BEFORE scenarios AND before the AppSec geoblock.

**Alerts view** — read-only list of `cscli alerts list --since 24h
--limit 100`. Row click opens a Drawer with source + events + any
decisions issued. Alerts = scenario fires; Decisions = active bans.
A scenario can fire without producing a decision (logged-only
profile) and a decision can outlive the alert that caused it.

**Captcha remediation** — M27 Step 5. Toggle + provider (hCaptcha /
reCAPTCHA v2 / Cloudflare Turnstile) + site key + secret key.
The secret is WRITE-ONLY: GET /api/v1/admin/security/crowdsec/captcha
never returns it. Empty secret on PUT means "keep existing". When
enabled, the agent rewrites FOUR keys in
`/etc/crowdsec/bouncers/crowdsec-nginx-bouncer.conf`
(`CAPTCHA_PROVIDER`, `SITE_KEY`, `SECRET_KEY`, `FALLBACK_REMEDIATION`)
and reloads nginx. The rest of the conf (M26-written AppSec settings)
is preserved byte-for-byte.

Provider site keys — 10000000-ffff-ffff-ffff-000000000001 is
hCaptcha's public test site key (always passes); useful for smoke-
testing the integration before plugging in production keys.

**Per-scenario remediation override** — ADR-0063. Table listing every
installed scenario with an inline Select: Default (ban) / Captcha /
Off (bypass). Saves rewrite a marker-bounded block at the TOP of
`/etc/crowdsec/profiles.yaml`:

```
# jabali-begin-overrides
# DO NOT HAND-EDIT — rewritten by jabali on Save. ...
# jabali-end-overrides
```

Everything below the end marker is preserved. Pre-flight
`crowdsec -t` before reload; on failure the `.bak` is restored. The
Captcha option is greyed out when Captcha remediation (above) is
disabled.

To roll back the override layer: `sed -i '/# jabali-begin-overrides/,/# jabali-end-overrides/d' /etc/crowdsec/profiles.yaml && systemctl reload crowdsec`.

## Troubleshooting

### CrowdSec LAPI unreachable in the UI

Symptom: CrowdSec tab shows "Service: down" and "LAPI: unreachable".

```bash
# Is crowdsec running?
systemctl status crowdsec

# Is the socket present + correct mode/owner?
ls -l /run/crowdsec/api.sock
# Expect: srw-rw---- 1 root jabali ... (mode 0660)

# Is the panel-agent in group jabali?
id jabali-agent 2>/dev/null || ps aux | grep jabali-agent  # process owner
groups <process-owner>

# Test from panel-agent's perspective
sudo -u <agent-user> curl --unix-socket /run/crowdsec/api.sock http://x/v1/decisions
```

If the socket is mode 0755 instead of 0660, the install drop-in's
`ExecStartPost=chmod 0660` regressed — check `/etc/systemd/system/crowdsec.service.d/`.

### CrowdSec AppSec blocking legitimate panel/tenant traffic

Symptom: requests to `/jabali-admin` (or a tenant site) return 403 from
nginx-bouncer; `cscli metrics show appsec` Blocked counter ticks.

```bash
# Identify which rule fired:
sudo journalctl -u crowdsec -n 200 --no-pager | grep -i appsec

# Quickest mitigation: remove the offending hub item via the UI
# /jabali-admin/security?sub=hub → find rule → Remove. Or drop the
# whole rule glob from /etc/crowdsec/appsec-configs/jabali-appsec.yaml
# (e.g. comment out 'crowdsecurity/generic-*') then:
sudo systemctl reload crowdsec
```

If the panel hostname itself is mistakenly hit, add the panel IP/CIDR
to the allowlist via `/jabali-admin/security?sub=allowlist` — that
bypasses AppSec inspection for trusted sources.

### UFW lockout recovery (LXC console path)

If `ufw enable` cuts your SSH session:

```bash
# From the host (LXC container console, IPMI, cloud-provider console):
ufw disable
# OR factory reset:
ufw --force reset
ufw default deny incoming
ufw default allow outgoing
ufw allow 22/tcp                  # SSH
ufw allow 80,443/tcp              # web
ufw allow 25,465,587,993/tcp      # mail
ufw allow 53/udp                  # DNS
ufw enable
```

After reset, the SQL `firewall_rules` history (if M14 backups+history
ever wires it) is out of sync — re-add any custom rules from the UI.

### "ufw status" output drops the Default line

Symptom: UFW tab shows "default in:" / "default out:" empty.

Cause: `ufw status numbered verbose` doesn't include the Default line
on some Debian versions. The agent issues a 2nd `ufw status verbose`
call to recover it. If still empty, check that the agent has root and
that ufw isn't reporting "Status: inactive" (defaults are only shown
when active).

## Defaults

| Setting | Default | Why |
|---------|---------|-----|
| UFW | `inactive` | Avoid lockout on fresh install — operator opts in |
| UFW default in/out | `deny in / allow out` | Standard server posture once enabled |
| AppSec geoblock | `mode=off` | Operator opts in via `/jabali-admin/security?sub=appsec` |
| AppSec rules | `base-config + vpatch-* + generic-*` | Default-on WAF (CVE virtual-patching + CRS-style generic) |
| CrowdSec collections | `linux + sshd + nginx + base-http-scenarios + http-cve + wordpress + whitelist-good-actors + appsec-virtual-patching + appsec-generic-rules` | Default install bundle (operator removable via Recommended hub picker) |

## Files

| Path | Purpose | Owner / mode |
|------|---------|--------------|
| `/run/crowdsec/api.sock` | LAPI unix socket | `root:jabali` 0660 |
| `/etc/crowdsec/appsec-configs/jabali-appsec.yaml` | AppSec config — inband_rules + geoblock pre_eval (rewritten by agent) | `root:root` 0644 |
| `/etc/crowdsec/acquis.d/jabali-appsec.yaml` | AppSec listener acquisition (`127.0.0.1:7422`) | `root:root` 0644 |
| `/etc/crowdsec/online_api_credentials.yaml` | CrowdSec Console enrollment (present iff enrolled; wiped by Disenroll) | `root:root` 0600 |
| `/etc/crowdsec/profiles.yaml` | Per-scenario remediation override (marker-bounded jabali block) | `root:root` 0644 |
| `/etc/crowdsec/bouncers/crowdsec-nginx-bouncer.conf` | nginx-bouncer config (APPSEC_URL, captcha keys) | `root:root` 0640 |
| `/etc/ufw/user.rules` | UFW rule store (touched only by `ufw` CLI) | `root:root` 0640 |

## References

- ADR-0053 — Why CrowdSec over fail2ban (+ packagecloud upstream)
- ADR-0054 — Why UFW over raw iptables
- ADR-0055 — Per-domain ModSecurity (SUPERSEDED 2026-04-26)
- ADR-0060 — AppSec geoblock (server-wide country filter)
- ADR-0061 — CrowdSec allowlists — LAPI is truth
- ADR-0062 — CrowdSec Console enrollment (operator-driven disenroll)
- ADR-0063 — Per-scenario remediation override via profiles.yaml
- M25 ADR-0050 — Unix socket lockdown (LAPI socket rationale)
- CrowdSec docs — https://docs.crowdsec.net/docs/next/
- CrowdSec Hub — https://hub.crowdsec.net/
- CrowdSec AppSec — https://docs.crowdsec.net/docs/next/appsec/intro/
