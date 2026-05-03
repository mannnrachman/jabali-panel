# Decision-brain inventory (M43 Step 1)

**Status:** generated 2026-05-03 from live VM `192.168.100.150` + repo source.
**Purpose:** surface every spot inbound HTTP / TCP can be denied, so the M43 Authority Hierarchy has a real diff target.

This document is read-only audit. It does NOT mandate changes — Steps 4–6 of M43 are the consolidation work. This file gets the truth on paper first.

## Summary table

| Brain | Current scope | Source-of-truth file(s) | Live count on `192.168.100.150` |
|---|---|---|---|
| **CrowdSec scenarios** | log-driven IP scoring across nginx access/error, sshd auth, php-fpm errors, apparmor denies | `/etc/crowdsec/scenarios/`, `/etc/crowdsec/acquis.yaml`, hub collections (linux, sshd, nginx, base-http-scenarios, http-cve, wordpress, whitelist-good-actors) | **17 scenario files** active (after `cscli scenarios list`) |
| **CrowdSec AppSec** | inline request inspection, geoblock + virtual-patching + generic OWASP-CRS-style | `/etc/crowdsec/appsec-configs/jabali-appsec.yaml`, `/etc/crowdsec/appsec-rules/*` | listening on **127.0.0.1:7422**, ruleset `base-config + vpatch-* + generic-*` |
| **firewall-bouncer** | enforcer of CrowdSec decisions at netfilter | `/etc/crowdsec/bouncers/crowdsec-firewall-bouncer.yaml` | active, deny via nftables family `inet`, table `crowdsec` |
| **nginx-bouncer** | enforcer of CrowdSec decisions at HTTP layer (Lua) | `/etc/crowdsec/bouncers/crowdsec-nginx-bouncer.conf`, nginx Lua module | active, polls LAPI on TCP `:8081` (parked socket flip — see `feedback_crowdsec_lapi_socket.md`) |
| **nginx limit_req** | per-IP rate cap on selected user-domain vhosts | `panel-agent/internal/commands/nginx_ratelimits.go` (writes `/etc/nginx/conf.d/jabali-rate-limits.conf` zones + per-vhost `limit_req` directive) | rendered when `domains.rate_limit_rps` set; uses `$binary_remote_addr` |
| **UFW** | port allow + admin-defined deny rules | `/etc/ufw/user.rules`, `/etc/ufw/user6.rules` | **port-only currently** — 13 allow rules (22, 53, 80, 443, 8443, 25, 465, 587, 993, 995, 4190, dns udp 53). **Zero `from <IP>` deny rules** on this VM. |
| **panel-api domain ALLOW directives** | per-domain custom nginx snippets — admin can drop `limit_req` / `deny` lines via Domain → Advanced | `panel-api/internal/api/domains.go` allowlist (lines ~810–830) | tenant-controllable but gated by `nginx_directive_allowlist` |

## Where decisions converge

- **Final IP-level enforcement:** `firewall-bouncer` is the only thing that holds a permanent nftables-level drop chain (table `inet crowdsec`). nginx-bouncer caches the same decisions for HTTP-layer 4xx / captcha redirect. This is the single chokepoint per the Authority Hierarchy.
- **Final HTTP-rate enforcement:** nginx `limit_req` directive emitted per-vhost by reconciler when `rate_limit_rps` set on a domain. Returns 429.
- **Final L4 enforcement:** UFW chains in `iptables-restore`-rendered files. Today: ports only.

## Where they fragment

| # | Failure mode | Risk window |
|---|---|---|
| 1 | Admin runs `ufw deny from <ip>` from CLI; CrowdSec never sees the IP's probes; CTI/CAPI enrichment loses the signal. | UFW IP rule outliving the threat |
| 2 | nginx `limit_req` keys on `$binary_remote_addr`. Behind a CDN with X-Forwarded-For unmasked, throttles edge IP not real client. CrowdSec scenarios may key on `X-Forwarded-For` or raw remote depending on acquis config. | CDN deployment without `enrich-real-ip` |
| 3 | CrowdSec ban TTL expires (default 4h); firewall-bouncer drops the rule. UFW deny set 6 months ago still matches. Operator confusion. | UFW IP rules persisted past their useful life |
| 4 | AppSec rule whitelists `/wp-json/.*` for plugin compat; scenario `http-bf-wordpress-bf` watching same path keeps escalating. Two verdicts for one request. | Operator-tuned whitelist not propagated to all engines |
| 5 | nginx-bouncer LAPI poll over TCP `:8081` (M26 default; socket flip parked per feedback note). If LAPI stops accepting TCP, decisions are stale until restart. | Parked TODO; not currently a fragmentation source |

## Who-dropped-this-packet today

To answer "why did that 4xx happen?" today, an operator currently checks:

1. `journalctl -u jabali-panel` for app-layer reject (auth, validation)
2. `tail /var/log/nginx/<host>-error.log` for nginx limit_req zone hits (logs `limiting requests`)
3. `tail /var/log/nginx/<host>-access.log | grep " 403 "` for nginx-bouncer 403s
4. `cscli decisions list -i <ip>` for CrowdSec ban verdict
5. `journalctl _SYSTEMD_UNIT=ufw.service` (or `dmesg | grep "[UFW BLOCK]"`) for UFW drops

Five log surfaces. M43 Step 2 collapses these into one M14 event source `security.decision.fired`. Step 7 (test bench) gives the same answer for a hypothetical request without having to reproduce it.

## Reconciler-owned vs admin-owned

| Layer | Reconciler writes? | Admin can override? |
|---|---|---|
| CrowdSec scenarios | partial — install.sh seeds; admin tunes via Security tab | yes (allowlists, per-scenario overrides — M27) |
| CrowdSec AppSec | install.sh seeds; admin toggles geoblock | partial (M27 geoblock card) |
| firewall-bouncer | install.sh writes config; never edited live | no |
| nginx-bouncer | install.sh writes config; never edited live | no |
| nginx limit_req | reconciler writes per-vhost from `domains.rate_limit_rps` | yes (UI on Domain page) |
| UFW ports | install.sh seeds defaults; admin via Security tab | yes |
| UFW IPs | not currently (no UI emits `from <IP>`) | yes via raw CLI only |

## What M43 changes (preview)

- Step 4 ⇒ migrate any UFW IP rules to CrowdSec decisions, leave UFW with port rules only. **On `192.168.100.150` today: zero UFW IP rules to migrate.** Migration is a no-op on this host but the CLI ships for hosts with a long IP-deny history.
- Step 5 ⇒ reframe nginx `limit_req` as anti-noise pre-filter. Rate-limit zones still emitted per-vhost when admin sets `rate_limit_rps` (no behavior change), but UI copy + comments shift from "security" to "anti-noise / scraping damping".
- Step 6 ⇒ Security UFW tab renamed to "Ports". UI never accepts `from <IP>` (was already true; this just makes it explicit in copy).

## Live-host dump command (operator can re-run anytime)

```sh
{
  echo "=== UFW IP rules ===" ; ufw status numbered | grep -E ' from '
  echo "=== UFW port rules ===" ; ufw status numbered | grep -vE ' from '
  echo "=== CrowdSec decisions ===" ; cscli decisions list -o human
  echo "=== Active limit_req zones ===" ; nginx -T 2>/dev/null | grep limit_req_zone
  echo "=== AppSec rules ===" ; cscli appsec-configs list ; cscli appsec-rules list
  echo "=== Bouncers ===" ; cscli bouncers list
}
```
