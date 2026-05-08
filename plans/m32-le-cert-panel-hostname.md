# M32 — Let's Encrypt cert for the panel hostname

**Goal.** Replace the always-self-signed cert at `/etc/jabali/tls/panel.{crt,key}` with a Let's Encrypt cert covering `<panel-hostname>` and `mail.<panel-hostname>` whenever the panel hostname is publicly routable. Browsers stop showing "Not Secure" on `https://<panel-hostname>:8443/` (admin panel) and on `https://mail.<panel-hostname>/` (Bulwark webmail).

Branch: `m32/le-cert-panel-hostname`. ADR target: **0066**.

---

## Constraints + invariants

- **Self-signed stays the fallback.** If LE issuance fails or the hostname is not routable, the existing self-signed cert remains. No outage path. Same status-machine pattern as `ssl_certificates`: try LE → on failure, keep current cert + retry every 3h.
- **Routability gate.** LE must NOT be attempted when the hostname is non-routable. Skip when (a) hostname matches `*.local|*.localdomain|localhost`, (b) hostname's public-DNS A record doesn't resolve to this VPS's `public_ipv4` from `server_settings`, or (c) port 80 isn't reachable from outside (skipped iff a + b are clean — port-80 probe is best-effort).
- **HTTP-01 via existing nginx :80.** Reuse the nginx default vhost served on `:80` already present from install.sh. Add a tiny `location /.well-known/acme-challenge/` block pointing at a panel-owned webroot under `/var/www/jabali-panel-acme/`. No new listener.
- **Cert covers the panel-hostname + `mail.<hostname>` SAN.** Same SAN set the self-signed cert already uses, so Bulwark's vhost (which serves `mail.<panel-hostname>`) becomes Firefox-clean too. Existing `provision_tls_cert` SAN logic stays as fallback.
- **Cert path stays at `/etc/jabali/tls/panel.{crt,key}` 0640 root:jabali.** Every consumer (nginx, jabali-panel.service, jabali-bulwark.service) already reads from there. Deploy-hook copies LE artifacts into that path after every renewal.
- **Renewal goes through certbot's existing systemd timer.** Certbot deploy-hook script at `/etc/letsencrypt/renewal-hooks/deploy/jabali-panel-cert.sh` re-syncs the cert into `/etc/jabali/tls/`, sets perms, then reloads nginx + jabali-panel.service + jabali-bulwark.service. No new timer.
- **Reload chain ordering.** nginx first (so :443 user-domain vhosts continue to terminate TLS), then jabali-panel (reads cert at startup), then jabali-bulwark (Next custom server reads from `/etc/jabali/tls/`). Bulwark restart is a few seconds of webmail downtime — accept.
- **Reconciler owns the periodic re-attempt.** New row kind `panel_cert` (or singleton table `panel_certificate` — tbd Step 1) tracks status / next_retry_at / last_error. Pattern mirrors `ssl_certificates` but scoped to the panel hostname (one row, not per-domain).
- **No auto-issue on private/lab installs.** First-time install on hostname matching the routability gate skip-list keeps self-signed. UI surfaces "Public-routable hostname not detected — using self-signed cert" with a Retry button that re-runs the gate.
- **Admin email is required.** LE registration needs an email; we already have `server_settings.admin_email`. Block issue path with a clear UI message if it's empty.
- **Staging-first toggle.** First attempt can be against LE staging (`--staging`) so admins testing don't burn the LE rate limit. UI Switch defaults OFF; Step 5 spike on test VM can flip it to validate the round-trip without prod rate limits.
- **Hostname change = re-issue.** When `server_settings.hostname` changes (M6.4 already detects this), wipe the cert state row, force re-attempt at next reconciler tick.
- **No HSTS preload.** We set `Strict-Transport-Security: max-age=15552000` on `:8443` only — never the preload list (admin opt-in if they want it).

---

## Steps

### Step 1: data model + routability gate

**Files:**
- `panel-api/internal/db/migrations/000079_panel_certificate.up.sql` (new) + `.down.sql`
- `panel-api/internal/models/panel_certificate.go` (new)
- `panel-api/internal/repository/panel_certificate_repository.go` (new)
- `panel-api/internal/services/panel_cert_routability.go` (new — `CheckRoutable(ctx, hostname, publicIPv4) (bool, reason string, error)`)
- `panel-api/internal/repository/panel_certificate_repository_test.go`
- `panel-api/internal/services/panel_cert_routability_test.go`

**Schema:**
```sql
CREATE TABLE panel_certificate (
  id              TINYINT UNSIGNED NOT NULL DEFAULT 1 PRIMARY KEY, -- singleton
  hostname        VARCHAR(255) NOT NULL,
  status          VARCHAR(32)  NOT NULL DEFAULT 'self_signed',
                  -- 'self_signed' | 'pending_acme' | 'issued' | 'pending_acme_retry' | 'failed'
  cert_pem_path   VARCHAR(255) NOT NULL DEFAULT '/etc/jabali/tls/panel.crt',
  issued_at       DATETIME NULL,
  expires_at      DATETIME NULL,
  last_error      TEXT NULL,
  attempt_count   INT UNSIGNED NOT NULL DEFAULT 0,
  next_retry_at   DATETIME NULL,
  staging         TINYINT(1) NOT NULL DEFAULT 0,
  use_le          TINYINT(1) NOT NULL DEFAULT 0, -- admin toggle; default OFF
  updated_at      TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  CONSTRAINT panel_certificate_singleton CHECK (id = 1)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
```

**Routability gate logic:**
1. If `hostname` matches `^(localhost$|.+\.local(domain)?$)` → skip; reason = `non-routable hostname suffix`.
2. `net.LookupHost(hostname)` from the panel-api process. If error or zero results → skip; reason = `dns lookup failed`.
3. Compare each A record to `server_settings.public_ipv4`. If none match → skip; reason = `dns points elsewhere`.
4. Otherwise → routable.

`CheckRoutable` returns `(routable bool, reason string, err error)`. Only the first three checks gate; port-80 probe stays in Step 4 (issuance attempt itself).

**Verification:**
- Unit tests cover all four routability outcomes + IPv6 future-proofing (skip-don't-fail when only AAAA exists).
- Migration applies + rolls back cleanly.

---

### Step 2: agent command — `ssl.panel.issue`

**Files:**
- `panel-agent/internal/commands/ssl_panel_issue.go` (new) + `_test.go`
- `panel-agent/internal/commands/registry.go` — register the command
- `install/letsencrypt/jabali-panel-cert.sh` (new — deploy-hook installed by install.sh into `/etc/letsencrypt/renewal-hooks/deploy/`)

**`ssl.panel.issue` payload:**
```json
{ "hostname": "mx.example.com", "extra_hostnames": ["mail.mx.example.com"], "email": "admin@example.com", "staging": false }
```

**Command body** (in order, abort on any fail):
1. `mkdir -p /var/www/jabali-panel-acme && chown root:www-data && chmod 0750`.
2. Verify nginx already serves the ACME challenge path (Step 3 wires this; agent just asserts, doesn't write nginx config).
3. Reuse `certbot.Runner.Issue(hostname, "/var/www/jabali-panel-acme", email, staging, extraHostnames)`.
4. On success: invoke the deploy-hook script with `RENEWED_LINEAGE=/etc/letsencrypt/live/<hostname>` so first-issue-time copies cert to `/etc/jabali/tls/panel.{crt,key}` (root:jabali 0640) and reloads nginx → jabali-panel → jabali-bulwark.
5. Return JSON: `{ status, issued_at, expires_at, cert_path }`.

**Deploy-hook script (`jabali-panel-cert.sh`)** — installed by install.sh, called by certbot on every renewal AND on first-issue:
```bash
#!/usr/bin/env bash
set -euo pipefail
cn="$(hostname -f 2>/dev/null || hostname)"
src="${RENEWED_LINEAGE:-/etc/letsencrypt/live/${cn}}"
[[ -d "$src" ]] || { echo "no LE lineage at $src"; exit 0; }
install -m 0640 -o root -g jabali "$src/fullchain.pem" /etc/jabali/tls/panel.crt
install -m 0640 -o root -g jabali "$src/privkey.pem"   /etc/jabali/tls/panel.key
systemctl reload nginx           || true
systemctl restart jabali-panel   || true
systemctl restart jabali-bulwark || true
```

**Verification:**
- Unit test: stubbed certbot runner success → deploy-hook runs → cert files present at expected path with expected mode/owner (testdata fixture).
- Unit test: stubbed certbot rate-limit → command returns `failed` with `last_error` populated.

---

### Step 3: install.sh wiring

**Files:**
- `install.sh` — `provision_tls_cert` keeps current self-signed code path; new `bootstrap_panel_acme_webroot` adds nginx :80 location block on the default vhost; `install_jabali_panel_cert_hook` installs the deploy-hook script at `/etc/letsencrypt/renewal-hooks/deploy/jabali-panel-cert.sh`.

**nginx :80 default vhost** gains:
```
location ^~ /.well-known/acme-challenge/ {
    default_type "text/plain";
    root /var/www/jabali-panel-acme;
    try_files $uri =404;
    auth_basic off;
    allow all;
}
```
This must precede the existing 80→443 redirect block. install.sh already owns this template — Step 3 just inserts the location and bumps the template version.

**Verification:**
- Fresh install on a `.local` host: panel cert stays self-signed, no LE attempt; nginx still has the `/.well-known/acme-challenge/` location for forward-compat.
- Fresh install on a routable host with A-record pointing here (test VM 192.168.100.150 won't qualify; live-VM smoke is Step 6): the location block is present, deploy-hook script is installed, but no LE attempt fires — Step 4 wires the trigger.

---

### Step 4: REST endpoints + reconciler hook

**Files:**
- `panel-api/internal/api/admin_panel_certificate.go` (new) — `GET /admin/panel-certificate`, `POST /admin/panel-certificate/issue`, `POST /admin/panel-certificate/toggle` (use_le on/off)
- `panel-api/internal/reconciler/panel_certificate_reconciler.go` (new) — singleton hook running every reconciler tick
- `panel-api/internal/api/admin_panel_certificate_test.go`

**Reconciler logic** (per tick):
1. Read `panel_certificate` row.
2. If `use_le=0` → noop.
3. Else compute next state:
   - `self_signed` + routable → set `pending_acme`, queue agent call.
   - `pending_acme_retry` + `next_retry_at <= now` → queue agent call.
   - `issued` + `expires_at - now < 30d` → certbot's own renewal timer handles it; reconciler doesn't fire `ssl.panel.issue` again.
4. On `ssl.panel.issue` failure → status=`pending_acme_retry`, attempt_count++, next_retry_at=now+3h, last_error stored.

**REST endpoints:**
- `GET /admin/panel-certificate` → returns row + computed `routable` + `routable_reason` (calls Step 1's `CheckRoutable` live).
- `POST /admin/panel-certificate/toggle` body `{ use_le: bool, staging: bool }` → flips toggles, schedules immediate reconciler attempt if turning on.
- `POST /admin/panel-certificate/issue` → forces an immediate attempt (bypasses `next_retry_at`), logs admin user.

**Verification:**
- Mock agent + mock routability returning routable → POST issue → row transitions to `issued` with stub artifacts.
- Mock agent returning rate-limit → row to `pending_acme_retry`, next_retry_at set.
- Reconciler test: 5 ticks with varying state → agent called only on `self_signed→pending_acme` and `pending_acme_retry→tryAgain` transitions.

---

### Step 5: admin UI (Server Settings → Storage→ adjacent SSL card)

Wait — Storage tab is for File Manager + Disk Quotas (M cf9b406). The panel-cert UI is closer to General (Identity / Hostname). Two options:

- **A. Embed in General tab.** New card "Panel SSL" below SSH Access. Tight coupling: hostname change in Identity card invalidates LE cert; co-location makes that easy to surface.
- **B. New "Panel SSL" tab.** Cleaner separation, but six tabs starts feeling crowded.

**Recommend A.** Card "Panel SSL" with:
- Status badge: `Self-signed` / `Pending Let's Encrypt` / `Issued by Let's Encrypt` (with expires_at) / `Failed`.
- Routability check result (live, refetched every load): green check + "Public-routable" or red x + reason.
- Toggle: "Use Let's Encrypt for this hostname" (disabled if not routable).
- Sub-toggle: "Use Let's Encrypt staging (testing only)".
- Button: "Retry now" (only when status=failed/pending_acme_retry).
- Last error in a collapsible `<details>` on failure rows.

**Files:**
- `panel-ui/src/shells/admin/settings/PanelSSLCard.tsx` (new)
- `panel-ui/src/shells/admin/settings/ServerSettingsPage.tsx` — embed card in `GeneralSettingsTab`
- `panel-ui/src/hooks/usePanelCertificate.ts` (new — TanStack Query)

**Verification:**
- Vitest: card renders all 4 status states from fixture.
- Vitest: routable=false → toggle disabled + tooltip explains why.

---

### Step 6: e2e + ADR + runbook + live-VM smoke

**Files:**
- `panel-ui/tests/e2e/panel-ssl.spec.ts` (new)
- `docs/adr/0066-le-cert-panel-hostname.md` — accepted
- `docs/runbooks/panel-ssl.md` — what each status means, how to manually run `certbot certonly --webroot -w /var/www/jabali-panel-acme -d <hostname>` if reconciler is stuck, how to roll back to self-signed (set `use_le=0`)
- `BLUEPRINT.md` — M32 section

**E2E spec mocks:**
- /admin/panel-certificate fixture (4 status variants) → assert badge + toggle states.
- POST /admin/panel-certificate/toggle round-trip → state refresh.

**Live-VM smoke** on the public VPS that prompted this milestone:
1. Set `mx.jabali-panel.com` A-record → VPS public IP at registrar.
2. `jabali update -f`.
3. Settings → General → Panel SSL → toggle "Use Let's Encrypt" with staging ON.
4. Wait one reconciler tick (~30s); confirm status flips to `issued (staging)`.
5. Browser hard-refresh `https://mx.jabali-panel.com:8443/` → "Not secure" gone (browser will warn on staging cert; that's expected).
6. Toggle staging OFF → status flips through `pending_acme_retry` → `issued`.
7. Browser confirms padlock + valid Let's Encrypt cert chain.
8. `https://mail.mx.jabali-panel.com/` (Bulwark) — same cert covers it via SAN, padlock there too.

**Exit criteria:**
- All 6 steps merged.
- Migration 000079 + rollback clean.
- Vitest + Playwright green.
- ADR-0066 accepted.
- Runbook published.
- Live-VM proves staging→prod round-trip + Bulwark SAN coverage.

---

## Risk register

| Risk | Mitigation |
|---|---|
| LE rate limit (50 certs/registered-domain/week) burned during testing | Staging-first toggle; clearly named in UI |
| Hostname change while LE cert active | Hostname-change handler (already in M6.4 reconciler) wipes panel_certificate row → next tick re-attempts on the new hostname |
| nginx :80 default vhost conflicts with a per-domain :80 vhost on `<hostname>` (e.g., admin hosts an actual website on the panel hostname) | Document: panel hostname must NOT be reused as a customer domain. install.sh + DomainCreate handler refuse to add a domain matching `server_settings.hostname` |
| Bulwark restart blip during deploy-hook | Restart is ~3-5s; webmail UX accepts it. Emit notification on each renewal so admin is aware |
| ACME challenge collision when admin hosts on :80 | nginx `location ^~ /.well-known/acme-challenge/` precedes any catchall; the `^~` modifier prevents regex locations from overriding |
| Panel cert expires unrenewed | certbot's existing systemd timer (`certbot.timer`) runs daily; deploy-hook runs on every renewal; reconciler also flags `expires_at - now < 14d` as critical alert (M14 channel) |
| Self-signed fallback breaks when admin toggles use_le=0 after LE was active | Toggle-off path: keep current LE cert until expiry, only re-generate self-signed when LE expires AND use_le is still 0. Avoids unnecessary cert churn |
| Renewal-hook not idempotent | Script uses `install -m 0640` (atomic replace), `systemctl reload || true`. Safe to run repeatedly — certbot does on every cron tick post-renewal |

---

## Out of scope (defer to M32.1+)

- DNS-01 challenge (covers wildcard certs / hosts behind firewall on :80). Today: HTTP-01 only.
- Multiple panel hostnames (load-balanced fleet). Single-hostname only.
- Customer-supplied custom CA (corporate-issued cert upload UI). M32.1 if asked.
- HSTS preload submission. Admin opt-in form / external process.
- Auto-detect routability change (user updates DNS while panel is running). Admin clicks "Retry now"; or wait for next 3h reconciler tick.

---

## Implementation order summary

```
Step 1 (data model + routability)
  └─> Step 2 (agent ssl.panel.issue + deploy-hook)
        └─> Step 3 (install.sh nginx :80 challenge + hook install)
              └─> Step 4 (REST + reconciler hook)
                    └─> Step 5 (admin UI card)
                          └─> Step 6 (e2e + ADR + runbook + live-VM smoke)
```

Strictly sequential — each step depends on the prior file shape. ~12-15 commits estimated.

Step 1 dispatchable.
