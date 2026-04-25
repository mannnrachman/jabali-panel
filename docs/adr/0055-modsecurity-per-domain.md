# ADR-0055: ModSecurity-nginx + OWASP CRS, per-domain toggle

**Status:** SUPERSEDED (2026-04-26) — replaced by ADR-0060 (CrowdSec AppSec geoblock) and the M27 AppSec work. CrowdSec AppSec covers the WAF role (in-band rule matching against virtual-patching + generic CRS-style rules) without a duplicate inspection layer. ModSecurity stack (apt packages, nginx module, /etc/nginx/modsec/, schema columns `domains.modsec_enabled` + `server_settings.modsec_global_enabled` + `server_settings.modsec_paranoia_level`, agent commands, panel-api routes, Security tab) all removed; migration `000074_drop_modsec_columns` drops the schema, `cleanup_modsecurity` in install.sh removes the apt packages on existing hosts.

**Original status:** Accepted (2026-04-24)
**Driven by:** Plan `plans/m26-security-tab.md` (M26 Step 1 — security foundation).

## Context

The panel needs a Web Application Firewall (WAF) to block common application-layer attacks (SQLi, XSS, path traversal, RCE patterns) against tenant sites served by nginx. Server-side WAF complements the network-level CrowdSec firewall-bouncer (ADR-0053): CrowdSec drops abusive *source IPs* at the kernel; ModSecurity inspects *request bodies* against rule patterns and blocks malicious payloads regardless of origin.

Three considerations shape the choice:

1. **Engine choice.** ModSecurity v3 (libmodsecurity3) is the industry standard for nginx — Trustwave-maintained, OWASP CRS-compatible, packaged in Debian as `libnginx-mod-http-modsecurity`. Coraza (Go-native, in-process) is the modern alternative but lacks first-class nginx packaging on Debian; would force us to ship a custom nginx build or drop in a sidecar Caddy. Rejected for v1.
2. **Rule set.** OWASP CRS v4 is the industry-standard rule corpus. Debian ships it as `modsecurity-crs` (`/usr/share/modsecurity-crs/`). Custom rules out of scope for v1; operators get the OWASP paranoia slider.
3. **Per-domain toggle vs. global.** A global enable would block known-good tenant apps (e.g. WordPress admin paths matching SQLi heuristics). Per-domain `modsec_enabled` lets operators turn it on for sensitive sites and leave others at default. The toggle lives in the existing per-domain config row (one boolean column, ADR-0002 — DB is truth for config). Global paranoia level (1–4, OWASP CRS native scale) lives in `server_settings` (also DB-truth).

## Decision

ModSecurity-nginx + OWASP CRS v4 are installed by `install_modsecurity()` in `install.sh` Step 1.

Install:
1. `apt install libnginx-mod-http-modsecurity modsecurity-crs`.
2. Write `/etc/nginx/modsec/main.conf`:
   ```
   Include /etc/nginx/modsecurity.conf
   Include /etc/modsecurity/crs/crs-setup.conf
   Include /usr/share/modsecurity-crs/rules/*.conf
   ```
   Paths match Debian packaging (`/etc/nginx/modsecurity.conf`, `/etc/modsecurity/crs/crs-setup.conf`). Upstream-tarball paths (`/etc/modsecurity/modsecurity.conf`, `/usr/share/modsecurity-crs/crs-setup.conf`) do NOT match what Debian ships — confirmed by VM smoke on 192.168.100.13.
3. The `libnginx-mod-http-modsecurity` package's stock `/etc/nginx/modsecurity.conf` ships with `SecRuleEngine DetectionOnly`. Install.sh edits it to `SecRuleEngine Off` (Step 1 default — visible globally but blocks nothing). The global toggle flips to `On` in M26 Step 4 (admin Security tab).
4. `modules-enabled/50-mod-http-modsecurity.conf` is a stock symlink (apt creates it). Install.sh leaves it alone.

No `modsecurity on;` directive is added to any nginx vhost in Step 1. Per-vhost wiring lands in Step 5 — the existing nginx vhost template gets a conditional `modsecurity on; modsecurity_rules_file /etc/nginx/modsec/main.conf;` block emitted only when `domains.modsec_enabled = true`. The reconciler regenerates vhosts on flag change (per ADR-0009 + ADR-0004).

## Alternatives considered

- **Coraza (Go-native).** No first-class Debian packaging; would force a custom nginx or a sidecar. Rejected for v1; revisit if Coraza lands in Debian or if libmodsecurity3 stagnates.

- **Cloudflare / external WAF.** Out-of-scope for self-hosted panel; some tenants are on bare-metal with no front-CDN.

- **Single global enable, no per-domain toggle.** Would either lock down every tenant (false positives → support burden) or stay off forever (no value). Per-domain is the only model that scales.

- **Custom rule editor in v1.** Out of scope. OWASP CRS paranoia level (1–4) is the only knob exposed in M26 v1. Rule editing lands in a future milestone if demand materialises.

- **Global ON in Step 1.** Would block tenant traffic before the operator has any UI to turn it off. Step 1 leaves engine `Off`, Step 4 lands the toggle, operator opts in.

## Consequences

- Two new apt packages on every fresh install (`libnginx-mod-http-modsecurity` + `modsecurity-crs`). Both are Debian-official, low-churn.
- nginx config-test runs against the modsec include after Step 1; if `modsecurity-crs` is mis-installed, `nginx -t` fails on first reload — fail-closed behaviour, surfaces install bugs immediately.
- Per-domain `modsec_enabled` boolean is added to the `domains` table in Step 4 (migration TBD). Vhost regeneration paths (ADR-0009) re-run the existing reconciler pass — no new infrastructure.
- Audit log lives at `/var/log/modsec_audit.log` (default per `modsecurity.conf`). The Security tab tails the last 50 lines via panel-agent (ADR-0001). Long-term log rotation handled by Debian's stock `logrotate.d/modsecurity`.
- Engine state semantics: `Off` (no inspection), `DetectionOnly` (logs but allows), `On` (inspects + blocks). M26 v1 exposes `Off` ↔ `On` only — `DetectionOnly` is a power-user mode reachable via direct config edit if needed.
