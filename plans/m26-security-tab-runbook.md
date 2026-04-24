# M26 Security Tab — Runbook

Operational reference for the admin Security tab shipped in Steps 1–9
(2026-04-24). See `plans/m26-security-tab.md` for the full plan and
ADRs 0053 (CrowdSec), 0054 (UFW), 0055 (ModSecurity).

## What it is

Three-tab admin page at `/jabali-admin/security`:

| Tab | Purpose | Backed by |
|-----|---------|-----------|
| CrowdSec | View metrics + active decisions; add/remove manual bans; inspect bouncers + hub items | LAPI on `/run/crowdsec/api.sock` (mode 0660 group `jabali`); `cscli` shell-out for hub/list operations |
| ModSecurity | Engine mode (Off / DetectionOnly / On); OWASP CRS paranoia 1..4; per-domain enable; audit-log tail | `/etc/nginx/modsecurity.conf` + `/etc/modsecurity/crs/crs-setup.conf`; `/var/log/modsec_audit.log` |
| UFW | Status + rules; add/delete rules; enable/disable firewall (typed-YES gate) | `ufw status numbered verbose`, `ufw allow/deny`, `ufw enable/disable` |

## Architecture at a glance

```
SPA  ──► panel-api /api/v1/admin/security/*  ──UDS──►  panel-agent
         (RequireAdmin gate)                            (shells out to ufw / cscli;
                                                         edits modsecurity.conf;
                                                         dispatches reconciler for
                                                         per-domain ModSec changes)
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

# 3. ModSecurity module loaded + engine off by default
nginx -V 2>&1 | grep -q modsecurity && echo "module compiled in"
grep -E '^SecRuleEngine' /etc/nginx/modsecurity.conf
# Expect: SecRuleEngine Off

# 4. CRS paranoia level resolves
grep -E '^\s*SecAction.*paranoia_level' /etc/modsecurity/crs/crs-setup.conf | head -1

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

### Changing ModSecurity paranoia

Two ways:

| From UI | From CLI |
|---------|----------|
| `/jabali-admin/security?tab=modsec` → drag the Paranoia slider → **Apply** | `sudo sed -i -E 's/setvar:tx\.paranoia_level=[0-9]+/setvar:tx.paranoia_level=2/' /etc/modsecurity/crs/crs-setup.conf && sudo nginx -t && sudo systemctl reload nginx` |

UI is preferred — it goes through the agent's atomic write + `nginx -t`
gate. The CLI variant skips that gate and a typo will leave nginx in
the previous-config state until it's restarted.

Higher paranoia = more rules fire = more false positives. Defaults to
1 (loose). Bump to 2 only after a week of `DetectionOnly` mode shows
no legitimate-traffic hits.

### Enabling ModSecurity on a domain

1. Set global engine to `DetectionOnly` first (so you see what would
   block without actually blocking).
2. Toggle the per-domain switch on the ModSec tab.
3. Wait ~10s for the reconciler to re-render the vhost and reload nginx.
4. Watch the audit-log tail for 24-48h.
5. Flip global engine to `On` once the audit shows no legitimate hits.

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
- Allow-list locked you out → edit
  `/etc/crowdsec/appsec-rules/jabali-geoblock.yaml` on the host, set
  `# jabali-mode: off` in the header, wipe the `pre_eval:` block, then
  `systemctl reload crowdsec`. UI then shows off-mode.

### Enrolling in CrowdSec Console (optional)

CrowdSec Console gives you a hosted dashboard + crowd-sourced threat
intel feeds. Free tier exists. Optional — out of the box CrowdSec
runs fully self-contained.

```bash
# Get an enrollment token from console.crowdsec.net
sudo cscli console enroll <token>
# Approve from the Console web UI
sudo systemctl restart crowdsec
```

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

### ModSecurity blocking legitimate panel/webmail traffic

Symptom: `/jabali-admin` returns 403; `/admin/security?tab=modsec` audit
log shows hits with rule IDs in the 9xxxxx range.

```bash
# Quickest mitigation: drop engine to DetectionOnly
# UI: ModSec tab → DetectionOnly → Apply
# CLI: sudo sed -i 's/^SecRuleEngine On/SecRuleEngine DetectionOnly/' /etc/nginx/modsecurity.conf && sudo systemctl reload nginx

# Then identify which rule is hitting:
sudo tail -200 /var/log/modsec_audit.log | grep -E 'id "[0-9]+"' | sort -u

# Whitelist a specific rule on a specific URI by adding to crs-setup.conf:
SecRule REQUEST_URI "@beginsWith /jabali-admin" "id:1001,phase:1,pass,nolog,ctl:ruleRemoveById=941100"
# Then `nginx -t && systemctl reload nginx`
```

If the panel itself is blocked by CRS, the per-domain ModSec switch on
the panel hostname's vhost is the safest mitigation — flip it off and
keep ModSec on the tenant vhosts only.

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
| ModSecurity engine | `Off` | Surface CRS gradually — flip to DetectionOnly first |
| CRS paranoia | `1` | Lowest false-positive rate — bump only after audit-log review |
| CrowdSec scenarios | `crowdsecurity/linux` collection | Default install bundle |

## Files

| Path | Purpose | Owner / mode |
|------|---------|--------------|
| `/run/crowdsec/api.sock` | LAPI unix socket | `root:jabali` 0660 |
| `/etc/nginx/modsecurity.conf` | Global ModSec engine config | `root:root` 0644 |
| `/etc/modsecurity/crs/crs-setup.conf` | OWASP CRS tunables (paranoia, etc.) | `root:root` 0644 |
| `/etc/nginx/modsec/main.conf` | ModSec rule include — sourced from each per-domain vhost when modsec_enabled | `root:root` 0644 |
| `/var/log/modsec_audit.log` | JSON audit events (read by UI tail) | `root:adm` 0640 |
| `/etc/ufw/user.rules` | UFW rule store (touched only by `ufw` CLI) | `root:root` 0640 |

## References

- ADR-0053 — Why CrowdSec over fail2ban (+ packagecloud upstream)
- ADR-0054 — Why UFW over raw iptables
- ADR-0055 — Per-domain ModSecurity (Debian paths)
- M25 ADR-0050 — Unix socket lockdown (LAPI socket rationale)
- CrowdSec docs — https://docs.crowdsec.net/docs/next/
- ModSecurity-nginx — https://github.com/owasp-modsecurity/ModSecurity-nginx
- OWASP CRS — https://coreruleset.org/
