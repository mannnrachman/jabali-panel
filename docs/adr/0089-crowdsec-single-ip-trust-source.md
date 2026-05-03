# ADR-0089 — CrowdSec is the single IP-trust source of truth

**Status:** ACCEPTED (2026-05-04)

**Supersedes:** the implicit "UFW for IP bans" assumption baked into M26 (ADR-0053 + ADR-0054), without invalidating either ADR.

**Companion to:** ADR-0060 (CrowdSec AppSec geoblock), ADR-0084 (per-user egress firewall — explicitly NOT in scope here).

## Context

Through M26 the panel grew three independent decision points for inbound HTTP / TCP:

1. **CrowdSec scenarios + AppSec** — log-driven IP scoring + inline request inspection.
2. **nginx limit_req** — per-IP rate cap on selected user-domain vhosts.
3. **UFW** — port allow/deny rules and (operator-edited) IP rules.

Each held its own state. They could disagree. Concrete failure modes:
- Admin runs `ufw deny from <ip>` from CLI; CrowdSec stops seeing the probes; CTI/CAPI enrichment loses the signal.
- nginx `limit_req` keys on `$binary_remote_addr`. Behind a CDN with X-Forwarded-For unmasked, throttles edge IP not real client.
- CrowdSec ban TTL expires; firewall-bouncer drops the rule. UFW deny set 6 months ago still matches.
- AppSec rule whitelists `/wp-json/.*`; scenario `http-bf-wordpress-bf` watching same path keeps escalating.

Operators couldn't answer "who dropped this packet?" without grepping five log surfaces.

## Decision

**CrowdSec LAPI is the only datastore that decides whether an IP is trusted, banned, or captcha-gated.** Every other layer either enforces decisions issued by CrowdSec, or stays out of IP-trust entirely.

### Authority hierarchy

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
  │   nginx limit_req on hard-cap paths only              │
  └───────────────────────────────────────────────────────┘

  ┌───────────────────────────────────────────────────────┐
  │ Static baseline boundary (declarative, no IP rules)   │
  │   UFW: port allow/deny only                           │
  └───────────────────────────────────────────────────────┘
```

### Rules

1. **Only CrowdSec issues final block decisions on IPs.** Other layers query, don't decide.
2. **nginx never decides** — only executes what bouncer hands it, plus rate caps on absolute-attack paths (login/admin-api/xmlrpc).
3. **UFW is declarative port policy.** Holds zero IP rules after M43 Step 4 migration. The admin UI no longer accepts `from <ip>`.
4. **nginx `limit_req` is anti-noise / scraping-damping**, not a security layer. It's blind to identity and attack patterns; cannot stop low-rate intelligent attacks. Keep ONLY for burst-smoothing on absolute-attack paths.

### What changed in code (M43)

- `panel-api/cmd/server/ufw_cmd.go` — `jabali ufw migrate-ip-bans` Cobra CLI: walks `ufw status numbered`, creates `cscli decisions add --reason ufw-migrated --duration 2160h` (90d) per rule, then `ufw delete`s it. Snapshot at `/var/lib/jabali/m43-ufw-snapshot.json`; `--revert` restores byte-identical UFW state.
- Hard guard: refuses to run when UFW has IP rules AND CrowdSec lacks `trusted_ips:` AND `--no-cdn` flag absent — prevents banning Cloudflare POPs.
- `panel-ui/.../AdminSecurityUfw.tsx` — Drawer no longer shows "From" IP input. Replaced with an Alert pointing to the CrowdSec tab.
- `panel-ui/.../AdminSecurityPage.tsx` — UFW tab renamed "Ports (UFW)".
- `panel-ui/.../AdminSecurityTrust.tsx` — new "Trust" sub-tab (default landing) showing IP verdicts, rate caps, AppSec links, and a single-IP test bench (`POST /admin/security/trust/test`) that asks every brain at once and renders the matrix.
- `panel-api/internal/eventsources/security_decision.go` — new `security.decision.fired` event source aggregates UFW BLOCK + nginx limit_req throttles into a single M14 envelope per 5-min window. Answers "who dropped this packet?" without grepping logs.
- `panel-agent/internal/commands/nginx_ratelimits.go` — comment block reframes per-vhost `limit_req` as anti-noise pre-filter, NOT security.
- `panel-api/cmd/server/serve.go` — fixed long-standing seed bug where `if !mutated { return }` early-exit also gated `EnsureDefaults` for managed_ips, VAPID, page_templates, and notification_event_settings. New event kinds added later were never seeded on existing hosts.

## Consequences

### Positive

- Single trust ledger — `cscli decisions list` answers definitively.
- "Who dropped this packet?" answerable from one M14 event source + one test-bench POST.
- Rule-class boundary clear in the UI: CrowdSec for IPs, Ports for ports.
- 90-day TTL on migrated bans nudges operators to re-confirm — reduces stale "permaban" surface.
- Behavioral rate detection consolidated in CrowdSec scenarios where it can correlate across paths and times. nginx `limit_req` keeps its narrow burst-smoothing role without claiming security responsibility.

### Negative

- Operators who relied on `ufw deny from <ip>` muscle memory must re-learn `cscli decisions add`. CLI hint output and Trust tab help text bridge this.
- CrowdSec downtime = no IP decisions. Already true (firewall-bouncer fails open by default); this ADR doesn't change failure mode but makes it more impactful — there's no UFW IP fallback. Recommend `bouncer.deny_default=true` as opt-in for high-paranoia hosts.
- Behind-CDN deployments require explicit `trusted_ips:` configuration before migration. Hard-guarded but adds one config step.
- 90d TTL on migrated bans means previously-permanent bans now rotate. By design — 6-month-old IP bans on residential IPs are usually wrong (DHCP reassignment).

### Risks

- **CDN X-Forwarded-For trust.** Already mitigated by Step 4 hard-guard on `cscli config show`.
- **Port rules silently dropped.** Mitigated: `--revert` restores from snapshot; CI golden-file diff would catch a regression in vhost templates that drops port rules.
- **Operator removes a CrowdSec decision a UFW rule was masking.** No mitigation — but CrowdSec's CTI/CAPI enrichment provides better signal than UFW's blind allowlist ever did.

## Out of scope (for ADR-0089)

- ADR-0084 per-user egress firewall stays. Different threat (outbound from compromised user), different cgroup match — independently correct, not a fourth IP-trust brain.
- Multi-host LAPI cluster. Single-host today; will need a follow-up ADR if/when we go multi.
- AppSec custom-rule edit-in-UI. Its own milestone.
- fail2ban / sshd_config tweaks. sshd already in CrowdSec acquis; no fail2ban in stack.

## Verification

1. `cscli decisions list` is the only place an IP ban exists. UFW has no `from <ip>` rules: `ufw status | grep -E ' from ' | grep -v Anywhere | wc -l` returns `0`.
2. `nginx -T | grep -c limit_req_zone` ≤ 4 (panel/admin-login/admin-api/xmlrpc when added; 0 today since no panel-side cap).
3. Trust tab `/jabali-admin/security?tab=trust` loads in <500ms with 1000 active CrowdSec decisions.
4. `security.decision.fired` event source publishes after a UFW BLOCK or nginx limit_req entry within 5min.
5. Test bench: paste an IP that's CrowdSec-banned; matrix shows `crowdsec=deny, ufw=allow` (clean state) — disagreement only when stale UFW rule still present.
6. Rollback `jabali ufw migrate-ip-bans --revert --yes` restores byte-identical UFW state.

## References

- `plans/m43-unified-trust-model.md` — full step-by-step blueprint.
- `plans/m43-unified-trust-runbook.md` — operator runbook.
- `docs/security/decision-brains.md` — Step 1 inventory.
