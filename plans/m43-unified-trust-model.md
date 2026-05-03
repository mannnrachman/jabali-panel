# M43 — Unified trust model (collapse decision brains into CrowdSec)

**Status:** drafted 2026-05-03 · branch `m43/unified-trust-plan` · NOT YET dispatchable

**ADR target:** new **0089** — CrowdSec is the single IP-trust source of truth.

**Companion to / replaces parts of:** M26 (UFW admin tab), M27 (CrowdSec extensions + AppSec).

## Problem

Three independent decision points exist for inbound HTTP. Each has its own trust state. They can disagree.

| Brain | What it bans / throttles | State source | Disagreement risk |
|---|---|---|---|
| **CrowdSec scenarios + AppSec** | IPs scoring above threshold; inline AppSec verdicts | LAPI DB + AppSec rule files | — (canonical) |
| **nginx limit_req** | per-IP rate over short window | nginx shm zone | key may differ from CrowdSec IP behind CDN; throttle 429 visible to scenarios but **on the wrong key** |
| **UFW** | static admin allow/deny IPs + ports | `/etc/ufw/user*.rules` | **fully detached** from CrowdSec; admin can blackhole IPs CS would otherwise observe |

**NOT a fourth brain (clarification):** "Bulwark" in this stack is the **webmail UI** (M6, `install.sh:install_bulwark`). Next.js app served on `mail.<panel-hostname>`. It does NOT inspect requests, score bots, or issue bans. Tenant app behind nginx+CrowdSec like any other. Listed only to pre-empt confusion with WAF tools also named "bulwark" — there is no custom WAF in this stack. This removes a typical fragmentation failure mode (custom enforcement logic that attackers study first).

## Authority hierarchy (target end-state)

After M43, decisions are layered, not parallel. One authority per decision class.

```
  ┌───────────────────────────────────────────────────────┐
  │ Risk authority — "should this IP be blocked?"         │
  │   CrowdSec (scenarios + AppSec)                       │
  └───────────────────────────────────────────────────────┘
                       │ decisions stream (LAPI)
                       ▼
  ┌───────────────────────────────────────────────────────┐
  │ Enforcement — applies CrowdSec verdicts               │
  │   firewall-bouncer (nftables drop / captcha redirect) │
  │   nginx-bouncer (HTTP 4xx + Lua decisions cache)      │
  └───────────────────────────────────────────────────────┘

  ┌───────────────────────────────────────────────────────┐
  │ Anti-noise pre-filter (NOT security)                  │
  │   nginx limit_req on hard-cap paths only:             │
  │   /jabali-panel/login, /jabali-admin/login,           │
  │   /admin-api/* (write), /xmlrpc.php                   │
  └───────────────────────────────────────────────────────┘

  ┌───────────────────────────────────────────────────────┐
  │ Static baseline boundary (declarative, no IP rules)   │
  │   UFW: port allow/deny only                           │
  └───────────────────────────────────────────────────────┘
```

**Rules:**
- Only **CrowdSec** issues final block decisions on IPs.
- **nginx** never decides — only executes what bouncer hands it, plus rate caps on absolute-attack paths.
- **UFW** is declarative (port policy only). Never receives runtime decisions. Holds zero IP rules after Step 4.
- **limit_req** = anti-noise / scraping-damping. Cannot stop low-rate intelligent attacks. Burst smoothing only.

Collapses parallel-brain model into one risk authority with multiple enforcement points. Single trust ledger, multiple effectors.

**Concrete failure modes (caught in M41/M42 dogfood):**

1. Admin adds `ufw deny from 1.2.3.4` → CrowdSec stops seeing that IP's probe traffic → never builds a scenario fingerprint → can't enrich CTI/CAPI from the actual attacker pool.
2. CDN front-end → nginx `limit_req` throttles edge IP (CDN POP) → real attacker IP, captured in `X-Forwarded-For`, sails through limit_req. CrowdSec sees real IP, throttles wrong key. Two views of "rate".
3. CrowdSec ban expires (default 4h) → IP unbanned in firewall-bouncer → UFW deny rule, set 6 months ago, still drops. Stale state nobody audits.
4. AppSec rule whitelists `/wp-json/.*` for plugin compat → scenario `http-bf-wordpress-bf` watching the same path still escalates → conflicting verdict (allow at HTTP layer, ban at IP layer).

## Goals

- **One IP-trust ledger:** CrowdSec LAPI is the only datastore that decides "is this IP trusted/banned/captcha". Every other layer queries it.
- **No parallel banlist:** UFW reduced to L4 port policy (admin-defined accept/deny by port, not by IP).
- **Single rate model:** nginx `limit_req` removed for application paths; CrowdSec scenarios own behavioral rate. Hard caps remain only on absolute-attack surfaces (`/jabali-panel/login`, `/jabali-admin/login`, `/admin-api/*` write paths, `/xmlrpc.php`).
- **Single Security UI surface:** scenarios + AppSec rules visible in one tab with a unified test bench (paste request, see all verdicts).
- **Unified expiry:** every ban has a TTL; surfaced and editable in one place.

## Non-goals

- Not removing UFW. Port allow/deny still useful (close 3306 if MariaDB ever rebinds, ensure :22 ssh closed by default, etc).
- Not touching M34 per-user **egress** firewall. Different threat (outbound from compromised user), different cgroup match — independently correct.
- Not touching CrowdSec firewall-bouncer mechanism. Stays the enforcer.
- Not migrating off CrowdSec. Doubling down, not replacing.

## Steps (9, dispatchable in waves of 3)

### Wave A — Audit + UI consolidation (additive, safe)

**Step 1 — Decision brain inventory (read-only audit).** Walk every spot a request can be denied: `/etc/nginx/conf.d/*.conf`, `/etc/nginx/sites-enabled/*`, vhost templates in panel-api, `/etc/ufw/user*.rules`, CrowdSec scenarios + AppSec rules. Output: markdown table at `docs/security/decision-brains.md`. NOT shipping policy yet — surfacing what's there.

**Step 2 — Unified decision log.** Tail every layer's drop/throttle log into a single panel-api event source `security.decision.fired`. nginx `limit_req` 429s, UFW drops (via auditd UID=0 packet log if feasible — else ufw.log scrape), CrowdSec ban events. One M14 channel. Operator sees every IP drop in one place. Answers "who dropped this packet?" without grepping 4 logs.

**Step 3 — Security tab unified policy view.** New sub-tab "Trust" under Security. Three panels:
- IP verdicts (CrowdSec decisions list with TTL + UFW deny IP rules — flags any UFW IP-deny as "should migrate to CrowdSec")
- Rate caps (lists every active `limit_req` zone + its trigger threshold; tagged "anti-noise, not security")
- AppSec rules (existing M27 view, hoisted into Trust tab)

No write actions yet, just visibility.

### Wave B — Migration (potentially behavior-changing, gate on dogfood)

**Step 4 — Migrate UFW IP denylist to CrowdSec decisions.** New CLI `jabali ufw migrate-ip-bans` reads any `ufw ... deny from IP` rule, creates equivalent CrowdSec decision (`cscli decisions add --ip X --duration 8760h --reason "ufw-migrated"`), removes the UFW rule. Idempotent. Runs once on `jabali update` post-step (with `--dry-run` first output). UFW left holding only port rules.

**Step 5 — Drop nginx `limit_req` on app paths.** Audit every vhost template; remove `limit_req` directives except on:
- `/jabali-panel/login` (login brute force absolute hard cap)
- `/jabali-admin/login`
- `/admin-api/*` mutating routes (rate cap behind Kratos auth)
- WordPress `/xmlrpc.php` (legacy DDoS surface)

CrowdSec `http-bf-wordpress-bf`, `http-cve`, AppSec `appsec-virtual-patching` already cover the rest. Frame retained limit_req as "anti-noise" not "security" in all comments + UI strings.

**Step 6 — UFW UI demoted to "Ports" tab.** Rename Security → UFW tab to Security → Ports. UI no longer accepts `from <ip>` rules — text says "IP rules live in Trust tab". Existing per-port rules (allow 22, deny 3306) stay editable. Help text: "UFW is the static baseline; CrowdSec handles dynamic IP decisions".

### Wave C — Single test bench + ADRs (closing)

**Step 7 — Trust test bench.** New page in Trust tab. Form: IP input (or paste of full HTTP request). Backend runs:
- `cscli decisions list -i <ip> -o json` — current CrowdSec verdict
- AppSec dry-run against pasted request (POST to `127.0.0.1:7422` with `X-Crowdsec-Appsec-Verbose: 1`)
- limit_req zone match (pure compute from regex over zone keys)
- UFW port match

Returns single verdict matrix — what every brain says about this single request. Reveals disagreement at a glance.

**Step 8 — ADR-0089 (CrowdSec is single IP source of truth).** Document the authority hierarchy verbatim. Supersedes the "UFW for IP bans" assumption baked into M26. References M27 AppSec (ADR-0060), M34 egress (ADR-0084 — explicitly out of scope of M43). Spell out: nginx is enforcer, UFW is declarative baseline, limit_req is anti-noise.

**Step 9 — Runbook + dogfood checklist.** `plans/m43-unified-trust-runbook.md`. Cover:
- "Admin wants to permablock an IP" → use CrowdSec, not UFW.
- "CDN unmasks real IP via X-Forwarded-For" → CrowdSec `enrich-real-ip` config; without it, decisions track CDN edge.
- "Who dropped this packet?" → check `security.decision.fired` event source; matrix tells you which layer.
- Rollback: `jabali ufw migrate-ip-bans --revert` restores UFW rules from a saved snapshot file `/var/lib/jabali/m43-ufw-snapshot.json`.

## Risks

- **CDN X-Forwarded-For trust.** If panel admin runs behind Cloudflare without setting CrowdSec `trusted_ips:`, the migration bans CDN POPs. **Step 4 must refuse to run** if `cscli config show` doesn't list any `trusted_ips`. Operator confirmation prompt.
- **MariaDB :3306 leak via UFW removal.** Step 6's "no IP rules" UI must NOT remove existing port rules silently. Audited via golden-file diff in CI.
- **CrowdSec downtime = no IP decisions.** Already true (firewall-bouncer fails open by default). M43 doesn't change that. ADR-0089 documents and recommends `bouncer.deny_default=true` as opt-in.
- **Behind-CDN AppSec false positives.** AppSec sees CDN IP, applies geoblock — wrong country. Step 4 prompt must include "is panel front-ended by CDN? if yes, set X-Forwarded-For trust before continuing".

## Verification — what "done" means

1. `cscli decisions list` is the only place an IP ban exists. UFW has no `from <ip>` rules.
2. `nginx -T | grep -c limit_req_zone` ≤ 4 (login + admin-login + admin-api + xmlrpc).
3. Trust tab renders in <500ms with 1000 active CrowdSec decisions.
4. `security.decision.fired` event source shows entries from all three brains within 5s of a fired event.
5. Test bench: paste a request that should AppSec-block + scenario-permit → matrix shows the disagreement clearly.
6. Rollback `jabali ufw migrate-ip-bans --revert` restores byte-identical UFW state.

## Out of scope (next-milestone candidates)

- **CrowdSec → fail2ban / sshd:** sshd already in CrowdSec acquis. No fail2ban in stack.
- **Bot-mgmt / CAPTCHA flow tuning:** M27 added it, M43 doesn't touch.
- **CrowdSec multi-server / LAPI cluster:** single-host stays. ADR if/when we go multi.
- **WAF custom rules in admin UI:** AppSec edit-in-UI is its own milestone.

## Open questions for operator

1. Are we OK demoting UFW from "IP banlist" to "port policy"? Reversible via Step 9 rollback.
2. Hard cap on `/jabali-panel/login`: keep current `5r/m` or tighten to `3r/m`?
3. CDN: does jabali-panel.com run behind Cloudflare? Affects Step 4 trusted_ips config.
4. Default ban duration for migrated UFW IPs: 1y (current implicit) or 90d (encourage rotation)?
