# M27 — CrowdSec admin extensions (allowlists + alerts + console + captcha + per-scenario remediation)

> Construction plan. Every step is cold-start executable. Read the step brief, run the tasks, verify, commit on a feature branch, rebase onto `origin/main`, open a PR, merge.

## Objective

Extend the admin CrowdSec tab (`/jabali-admin/security?tab=crowdsec`) with five features:

1. **Allowlists** — server-wide IP/CIDR never-ban list (`cscli allowlists`). Prevents self-lockout.
2. **Alerts view** — read-only list of `cscli alerts list` with Drawer detail per alert.
3. **Console enrollment** — `cscli console enroll <token>` / `status` / `disenroll`. Unlocks CTI community blocklist pulls.
4. **Captcha remediation** — swap `ban` for captcha on configurable rules. hCaptcha or reCAPTCHA. Writes `/etc/crowdsec/bouncers/crowdsec-nginx-bouncer.conf` + server_settings for creds.
5. **Per-scenario remediation override** — rewrite `/etc/crowdsec/profiles.yaml` so specific scenarios map to `captcha` / `ban` / `off`. Pairs with #4 (captcha only selectable if #4 ENABLED).

No user-shell changes. Admin-only. Ship as M27.

## Constraints (load-bearing; do not violate)

- **ADR-0050 (M25 unix sockets)** — CrowdSec LAPI stays on `/run/crowdsec/api.sock`. Do not add any new TCP listener. AppSec on `127.0.0.1:7422` loopback (from M26, ADR-0060) stays as-is.
- **ADR-0002 (DB as truth for config)** — allowlists + alerts live in LAPI only (runtime state, same rule as decisions). Captcha creds + enabled toggle live in `server_settings` (config). Per-scenario remediation map lives in `/etc/crowdsec/profiles.yaml` (LAPI config, reloaded into memory — not mirrored in jabali DB).
- **Golden rule** — no direct shell from panel-api. Every privileged op flows through panel-agent NDJSON RPC.
- **Route family** — extend `RegisterSecurityCrowdsecRoutes` in `panel-api/internal/api/security_crowdsec.go`. Do NOT create new files per feature.
- **Agent handlers** — extend `panel-agent/internal/commands/security_crowdsec.go`. Same file, new handlers registered in existing `init()`.
- **Convention compliance** — per `docs/CONVENTIONS.md`: `SearchableTable` for lists with search, `Drawer` (not Modal) for create + edit, `Table.Column` children (not `columns=`), `Grid.useBreakpoint` for responsive, `destroyOnClose` on every Drawer.
- **Two reload targets** — captcha conf changes → `systemctl reload nginx` (bouncer config is read by the Lua bouncer at nginx reload). Profiles.yaml changes → `systemctl reload crowdsec` (LAPI re-reads profiles on SIGHUP).
- **Fresh install must stay green** — any new config files install.sh drops must be idempotent; install.sh must still pass end-to-end on a clean VM.
- **M26 load-bearing** — `install_crowdsec_nginx_bouncer` already registers the `jabali-nginx` bouncer and writes the full conf. Captcha changes EDIT the existing conf; they do NOT rewrite it from scratch (would clobber M26's AppSec settings).
- **Caveman mode compatible** — anti-pattern list at the end of this plan is the quick way to avoid reopening a closed regression.

## Out of scope

- User-shell security page (deferred, same boundary as M26)
- AppSec custom rules editor beyond M26's geoblock (YAML grammar too rich for form UI — host-edit + reload remains the escape hatch)
- CTI blocklist UI beyond enrolment — subscriptions configured in CrowdSec Console, not in jabali
- Per-domain CrowdSec policy — still deferred (see M26 out-of-scope list)
- Scenario parameter editor — only the remediation action is overridable, not the scenario's thresholds

## Numbering

- ADRs to write: **0061**, **0062**, **0063** (next free after 0060)
- Migration: **000068** (next free after 000067)
- Agent RPC verbs: `security.crowdsec.allowlists.*`, `security.crowdsec.alerts.*`, `security.crowdsec.console.*`, `security.crowdsec.captcha.*`, `security.crowdsec.profiles.*`
- Panel-api routes: all nested under existing `/admin/security/crowdsec/*`

---

## Waves + dependency graph

```
Sprint 0          Wave A            Wave B (serial)        Wave C (coupled)     Finale
┌────────────┐   ┌────────────┐   ┌──────────────────┐   ┌────────────────┐   ┌────────────┐
│ Step 1     │──▶│ Step 2     │──▶│ Step 3 (alerts)  │──▶│ Step 5 (captcha│──▶│ Step 7     │
│ ADRs +     │   │ Allowlists │   │  ↓               │   │   + mig 000068)│   │ Runbook    │
│ install.sh │   │            │   │ Step 4 (console) │   │    ↓           │   │ + E2E      │
│ deps + VM  │   │            │   │                  │   │ Step 6 (per-   │   │ + VM smoke │
│ probe      │   │            │   │                  │   │   scenario)    │   │            │
└────────────┘   └────────────┘   └──────────────────┘   └────────────────┘   └────────────┘
     gate            gate             serial                  coupled             gate
```

**All steps are serial** — per memory rule `feedback_never_agents` (no sub-agent dispatch ever) + `feedback_no_partial_blueprint_to_main`, execution is inline one-at-a-time. Step 2 gates everything: its route + handler patterns are the template later steps reuse verbatim (cscli passthrough). Steps 3 + 4 both touch `security_crowdsec.go` (agent + panel-api) + `AdminSecurityCrowdsec.tsx` + `useSecurityCrowdsec.ts` — same four files — so they ship in order, not in parallel. Steps 5 + 6 both edit the CaptchaRemediationCard + ProfilesCard pair; Step 6 requires Step 5's captcha-enabled flag to gate the captcha option. Step 7 closes out.

---

## Step 1 — ADRs + install.sh deps + branch setup

### Context brief

M27 adds five orthogonal admin-facing features. Before writing code, record the three decisions that are load-bearing enough to merit ADR signoff, and make sure install.sh installs any new upstream packages on a fresh VM.

The three ADRs:
- **0061 — Allowlists via LAPI, not DB.** Same rule as decisions (ADR-0002 carve-out). LAPI is truth. jabali queries live via `cscli allowlists list -o json`.
- **0062 — Console enrolment is optional + machine-scoped.** The enrollment token lives in `/etc/crowdsec/config.yaml` (CrowdSec manages it). jabali does NOT store the token. UI queries `cscli console status` live.
- **0063 — `/etc/crowdsec/profiles.yaml` as the per-scenario remediation source.** Upstream-documented config path. jabali rewrites the full file (not a drop-in) because profiles are evaluated in order and splitting them across files reorders evaluation. File is backed up to `.bak` before every write.

Install.sh side: `crowdsec` package + AppSec (M26) already pull in everything needed for allowlists/alerts/console. Captcha needs nothing new (the bouncer package is already installed in M26). Profiles.yaml already ships with the `crowdsec` package — do not drop our own copy in install.sh; `install_crowdsec_profiles` only runs if `/etc/crowdsec/profiles.yaml` doesn't exist (defensive, since the package ships one).

Branch: create `m27/crowdsec-extensions` off `origin/main`. Every subsequent step commits onto this branch. Rebase onto `origin/main` before the final PR.

### Tasks

1. `git fetch origin main && git checkout -b m27/crowdsec-extensions origin/main`
2. **VM cscli-shape probe** — before writing any agent code, probe the installed CrowdSec's CLI surface so Step 2/3/4/6 handlers match reality. Save transcripts to `plans/m27-probe.txt` and commit it:
   ```bash
   ssh -p 2222 root@192.168.100.150 "\
     cscli version && \
     cscli allowlists --help 2>&1 && \
     cscli allowlists add --help 2>&1 && \
     cscli allowlists list --help 2>&1 && \
     cscli alerts --help 2>&1 && \
     cscli console --help 2>&1 && \
     cscli scenarios list -o json | head -c 2000 && \
     (crowdsec -t 2>&1 || echo 'no -t flag')"
   ```
   If subcommand names differ (e.g., `cscli allowlists members add` vs `cscli allowlists add`), update Step 2/3/4/6 command strings here in this plan before proceeding. **Do not proceed to Step 2 on assumption** — the plan text reflects the 1.6+ nominal shape but CrowdSec minors rename subcommands.
3. **Secret storage convention probe** — before defining the captcha secret column shape (Step 5), check how other secrets live:
   ```bash
   grep -rE "VARCHAR.*(secret|password|key)" panel-api/internal/db/migrations/ | head -20
   grep -rE "Secret|Password|Key" panel-api/internal/models/server_settings.go | head -20
   ```
   If the convention is plaintext in server_settings (same as Kratos admin secret, VAPID private key, etc.), Step 5 follows it. If anything is envelope-encrypted, Step 5 must match. Record finding in this step's commit message.
4. Write `docs/adr/0061-allowlists-lapi-truth.md` — follow 0060's structure (Context → Decision → Consequences → Implementation). Record: allowlists are LAPI runtime state, not DB; wire contract mirrors decisions (`{scope, value, reason}` in, list out).
5. Write `docs/adr/0062-console-enrollment-machine-scope.md` — record: enrollment token is machine-bound (not jabali-bound), jabali never stores the token, admin UI runs `cscli console enroll/disenroll/status` via agent, no DB rows.
6. Write `docs/adr/0063-profiles-yaml-for-remediation-override.md` — record: profiles.yaml is the canonical LAPI config for mapping scenarios → remediation; jabali rewrites a marker-bounded block (NOT whole file); `.bak` backup before write; pre-flight via `crowdsec -t` (if the probe in task 2 confirmed the flag exists); reload via `systemctl reload crowdsec`.
7. `install.sh` — add one idempotent helper `install_crowdsec_profiles()` that: if `/etc/crowdsec/profiles.yaml` doesn't exist, drop the upstream default (fetched from `/usr/share/doc/crowdsec/examples/profiles.yaml.gz` if shipped, else write a minimal `default_ban_scenario`). Call it right after `install_crowdsec_nginx_bouncer` in the main install sequence.
8. `install.sh` — verify captcha template files ship with `crowdsec-nginx-bouncer` package (`/var/lib/crowdsec/lua/templates/captcha.html` + `ban.html`). If not (package regression), `_warn` — do not fail install.
9. Commit: `chore(m27): ADRs 0061-0063 + install.sh profiles + VM probe transcript`.

### Verify

```bash
# branch exists + tracks main
git branch --show-current  # → m27/crowdsec-extensions
git log --oneline origin/main..HEAD | wc -l  # → 1

# ADRs present
ls docs/adr/006[123]-*.md  # → 3 files

# install.sh syntax
bash -n install.sh && echo OK

# on fresh VM (ssh -p 2222 root@192.168.100.150):
bash install.sh  # idempotent re-run after M26 baseline
ls -l /etc/crowdsec/profiles.yaml  # exists
ls /var/lib/crowdsec/lua/templates/  # captcha.html + ban.html present
```

### Exit criteria

- `git log --oneline origin/main..HEAD` shows one commit on `m27/crowdsec-extensions`
- Three ADRs committed with status `Accepted — <date>`
- `install.sh` still passes `bash -n`
- Fresh VM install still green end-to-end (M26 baseline + M27 additions)

### Rollback

Delete branch. No DB change yet. `install_crowdsec_profiles` is idempotent and additive — leaving it in on a rollback is safe.

---

## Step 2 — Allowlists (WAVE A — gate)

### Context brief

Server-wide IP / CIDR never-ban list. `cscli allowlists` is upstream-supported since CrowdSec 1.6. Operator adds their office IP, home IP, CI runner CIDR; LAPI never produces a decision for those ranges even if a scenario fires. Prevents admin self-lockout.

Wire contract mirrors decisions:
- `GET /admin/security/crowdsec/allowlists` → `{items: Array<{value, reason, created_at}>}`
- `POST /admin/security/crowdsec/allowlists` body `{value, reason}` → `{value}`
- `DELETE /admin/security/crowdsec/allowlists?value=<urlencoded>` → `204`

**DELETE uses query param, NOT path param.** Gin's `:value` matches exactly one path segment — `DELETE /allowlists/192.0.2.0/24` would route to `:value="192.0.2.0"` and 404 the `/24` tail. Query param dodges the segmentation + doesn't need `*value` wildcard (which has its own trailing-slash quirks). Client URL-encodes the value.

`value` is IP or CIDR; agent validates via `net.ParseIP` / `net.ParseCIDR` before shelling to `cscli`.

CrowdSec treats allowlists as scoped objects (allowlist + members). jabali uses a single server-wide allowlist named `jabali-admin-allowlist`, created on first add if missing. Simplifies the wire contract to `{value, reason}` instead of `{allowlist_name, value, reason}`.

Panel-api: extend `RegisterSecurityCrowdsecRoutes` (do not create new file).
Panel-agent: extend `panel-agent/internal/commands/security_crowdsec.go`; register three new handlers in existing `init()`.
UI: new card `AllowlistsCard` in `AdminSecurityCrowdsec.tsx`, placed above the Decisions table. Drawer for add (convention: create uses Drawer, not Modal). SearchableTable wrapper since the list can grow. `destroyOnClose` on the Drawer.

### Tasks

1. **Agent handlers** (`panel-agent/internal/commands/security_crowdsec.go`):
   - `csAllowlistsListHandler` — `cscli allowlists inspect jabali-admin-allowlist -o json`; if "not found" → return empty list; else parse + return `{items: [...]}`.
   - `csAllowlistsAddHandler` — params `{value, reason}`. Validate value via `net.ParseIP` OR `net.ParseCIDR` (reject bare domain, bare ASN, country code — only IP and CIDR). Ensure allowlist exists (`cscli allowlists create jabali-admin-allowlist -d "..."` — check return code; "already exists" is success). Add member: `cscli allowlists add jabali-admin-allowlist <value> --reason "<reason>"`. Return `{value}`.
   - `csAllowlistsRemoveHandler` — params `{value}`. `cscli allowlists remove jabali-admin-allowlist <value>`. Return `{}`.
   - Register in `init()`:
     ```go
     Default.Register("security.crowdsec.allowlists.list", csAllowlistsListHandler)
     Default.Register("security.crowdsec.allowlists.add", csAllowlistsAddHandler)
     Default.Register("security.crowdsec.allowlists.remove", csAllowlistsRemoveHandler)
     ```
   - Unit tests: table-driven validation tests (valid IP, valid CIDR, bad input rejected).

2. **Panel-api handlers** (`panel-api/internal/api/security_crowdsec.go`):
   - `GET /admin/security/crowdsec/allowlists` → agent call `security.crowdsec.allowlists.list`, passthrough response.
   - `POST /admin/security/crowdsec/allowlists` → bind `{value, reason}`, agent call `security.crowdsec.allowlists.add`, return 201 + body.
   - `DELETE /admin/security/crowdsec/allowlists` with `?value=<urlencoded>` query → read via `c.Query("value")`; reject empty; agent call `security.crowdsec.allowlists.remove`; return 204.
   - Admin-only middleware already applied at the group level — do not re-apply.

3. **UI hook** (`panel-ui/src/hooks/useSecurityCrowdsec.ts`):
   - New types: `CrowdsecAllowlistEntry = {value: string; reason: string; created_at: string}`.
   - `useCrowdsecAllowlists()` — query, no refetch interval (list mutates only via our mutations).
   - `useAddCrowdsecAllowlist()` + `useRemoveCrowdsecAllowlist()` mutations. Invalidate `["security","crowdsec","allowlists"]` on success.

4. **UI card** (`panel-ui/src/shells/admin/security/AdminSecurityCrowdsec.tsx`):
   - New component `AllowlistsCard` rendered above the Decisions section.
   - SearchableTable wrapping antd Table; columns (via `<Table.Column>` children): Value, Reason, Created, Actions (Delete with Popconfirm).
   - "Add to allowlist" button opens Drawer with Form: `value` (input + placeholder "192.0.2.1 or 192.0.2.0/24"), `reason` (input).
   - `destroyOnClose` on Drawer. Form validates via a pattern (IP or CIDR), but server is authoritative.
   - Empty state: `<Empty description="No allowlist entries" />` with CTA to open the Drawer.

5. Commit: `feat(security-crowdsec): allowlists (ADR-0061)`.

### Verify

```bash
# Go side
cd panel-api && TMPDIR=/var/tmp go test -race ./internal/api/...
cd panel-agent && TMPDIR=/var/tmp go test -race ./internal/commands/...

# Build
cd panel-api && go build ./...
cd panel-agent && go build ./...
cd panel-ui && npm run typecheck

# On VM:
curl -sS -b /tmp/jabali-admin.cookies https://panel.local:8443/api/v1/admin/security/crowdsec/allowlists
# → {"items":[]}
# add from UI → verify cscli sees it
cscli allowlists inspect jabali-admin-allowlist -o json | jq '.members | length'
```

### Exit criteria

- All three handlers registered + tested (race-clean)
- UI card renders, add + delete both round-trip successfully on VM
- `cscli allowlists inspect jabali-admin-allowlist` shows UI-added entries
- Convention compliance: SearchableTable present, Drawer (not Modal), `Table.Column` children, `destroyOnClose`

### Rollback

Revert commit. Any allowlist entries stay in LAPI (no data to clean up — operator can `cscli allowlists remove` or delete the whole allowlist via cscli).

### Dependencies

- Gates Wave B (Steps 3 + 4): those steps copy this step's cscli-passthrough pattern.

---

## Step 3 — Alerts view (WAVE B — parallel with Step 4)

### Context brief

Alerts = scenario fires. Decisions = active enforcement. They're different: a scenario can fire without producing a decision (e.g., logged-only scenarios, or decisions that expired). The Decisions tab (shipped in M26) only shows active bans. The Alerts view shows every scenario fire over a time window, including the source, event count, and machine that fired the scenario.

Read-only. No mutations. Pure cscli passthrough.

Wire:
- `GET /admin/security/crowdsec/alerts` → `{items: Array<{id, scenario, source_ip, source_scope, source_value, events_count, started_at, stopped_at, machine_id, decisions_count}>}`
- `GET /admin/security/crowdsec/alerts/:id` → `{alert: {... full detail including events: [...]}}`

Agent commands:
- `security.crowdsec.alerts.list` → `cscli alerts list -o json --since 24h --limit 100` (hard limits so a 10k-event host doesn't OOM the panel).
- `security.crowdsec.alerts.inspect` → `cscli alerts inspect <id> -o json --details` (details = include events).

UI: new section `AlertsCard` BELOW the Decisions table (so the flow is Status → Metrics → Allowlist → Decisions → Alerts → Hub → AppSec). Table with row click → Drawer showing full alert detail + events list.

### Tasks

1. Agent handlers:
   - `csAlertsListHandler` — `cscli alerts list -o json --since 24h --limit 100`. No params from client (kept simple; server caps are server-chosen). Parse + return `{items: [...]}`.
   - `csAlertsInspectHandler` — params `{id int}`. `cscli alerts inspect <id> -o json --details`. Return `{alert: {...}}`.
   - Register in `init()`.

2. Panel-api:
   - `GET /admin/security/crowdsec/alerts` → agent `security.crowdsec.alerts.list`, passthrough.
   - `GET /admin/security/crowdsec/alerts/:id` → parse int, agent `security.crowdsec.alerts.inspect`, passthrough.

3. UI hook:
   - `CrowdsecAlert` + `CrowdsecAlertDetail` types.
   - `useCrowdsecAlerts()` with 60s refetchInterval.
   - `useCrowdsecAlert(id)` — conditional, only enabled when a row is selected.

4. UI card:
   - `AlertsCard` — Table with columns: Scenario, Source (IP + scope tag), Events, Started, Machine.
   - Row click opens Drawer with Descriptions for metadata + nested Table for events.
   - Drawer `destroyOnClose`.
   - No Add/Remove — read-only. Skip SearchableTable since the list is small + capped at 100.

5. Commit: `feat(security-crowdsec): alerts view (ADR-0061 follow-up)`.

### Verify

```bash
cd panel-api && TMPDIR=/var/tmp go test -race ./internal/api/...
cd panel-agent && TMPDIR=/var/tmp go test -race ./internal/commands/...
cd panel-ui && npm run typecheck

# on VM (trigger a scenario fire first by hammering a protected endpoint, then):
curl -sS -b /tmp/jabali-admin.cookies https://panel.local:8443/api/v1/admin/security/crowdsec/alerts | jq '.items | length'
# → >0 if scenarios fired, 0 otherwise (both valid)
```

### Exit criteria

- Alert list renders, row click opens Drawer with events
- 60s refetch works
- Race-clean tests
- Convention compliance

### Rollback

Revert commit. Pure read-only; no state to roll back.

### Dependencies

- Depends on Step 2 (gates Wave B)
- Parallel-safe with Step 4 (different files, different RPCs, different panel-api routes)

---

## Step 4 — Console enrollment (WAVE B — parallel with Step 3)

### Context brief

CrowdSec Console (https://app.crowdsec.net) is an optional hosted dashboard that pulls scenario fires + decisions from your machine for cross-instance correlation and gives you CTI community blocklists back. Free tier exists. Enrollment is machine-scoped — a single enrollment token ties one CrowdSec instance to one Console account.

Three states:
- Not enrolled → show text input for token + Enroll button
- Pending (operator clicked the "Accept instance" link in Console but token only binds after they confirm in the web UI) → show "Pending" badge + Refresh button
- Enrolled → show account name + Disenroll button

Wire:
- `GET /admin/security/crowdsec/console` → `{enrolled: bool, pending: bool, account_name?: string}`
- `POST /admin/security/crowdsec/console/enroll` body `{token}` → `{pending: true}` on success
- `POST /admin/security/crowdsec/console/disenroll` → `{}` on success

Agent:
- `cscli console status -o json` — returns enrollment state (parse `enrolled`, `pending_enrollment`).
- `cscli console enroll <token>` — token validation server-side, errors surface via stderr.
- `cscli console disenroll` — no-op if not enrolled.

UI: `ConsoleCard` — small card, no Table. Descriptions for state + Form for token (only visible in the not-enrolled state). Enroll / Disenroll buttons with Popconfirm. Link to `https://app.crowdsec.net` in the card header.

### Tasks

1. Agent handlers:
   - `csConsoleStatusHandler` — `cscli console status -o json`. Parse. Return `{enrolled: bool, pending: bool, account_name?: string}`.
   - `csConsoleEnrollHandler` — params `{token string}`. Validate token is non-empty + matches basic shape (alnum + dashes + length > 10; upstream tokens are long). Run `cscli console enroll <token>`. Return `{}`. On nonzero exit, return stderr-derived error.
   - `csConsoleDisenrollHandler` — `cscli console disenroll`. Return `{}`.
   - Register in `init()`.

2. Panel-api: three routes under `/admin/security/crowdsec/console/*`.

3. UI hook:
   - `CrowdsecConsoleStatus` type.
   - `useCrowdsecConsoleStatus()` with 60s refetch (catches pending→enrolled transitions).
   - `useEnrollCrowdsecConsole()` + `useDisenrollCrowdsecConsole()` mutations.

4. UI card:
   - `ConsoleCard` — renders one of three sub-components by state.
   - Not enrolled: input + Enroll button (Form with Popconfirm warning "This sends your scenario fires + decisions to CrowdSec Console").
   - Pending: badge + info Alert + Refresh (triggers refetch) + Disenroll fallback.
   - Enrolled: Descriptions with account name + Disenroll button (Popconfirm).
   - `destroyOnClose` is N/A (no Drawer).

5. Commit: `feat(security-crowdsec): console enrollment (ADR-0062)`.

### Verify

```bash
cd panel-api && TMPDIR=/var/tmp go test -race ./internal/api/...
cd panel-agent && TMPDIR=/var/tmp go test -race ./internal/commands/...
cd panel-ui && npm run typecheck

# on VM:
# 1. Visit https://app.crowdsec.net, create account, get enrollment token
# 2. Paste into UI, click Enroll
# 3. Accept the pending instance in the web UI
# 4. UI should transition pending → enrolled within 60s
# 5. Verify:
cscli console status
# → enrolled=true
```

### Exit criteria

- All three states render correctly
- Enroll + disenroll both round-trip
- 60s refetch picks up pending→enrolled transition

### Rollback

Revert commit. Operator can `cscli console disenroll` from the host if the revert leaves a stale enrolled state in LAPI. Recommend a disenroll before revert in the PR description.

### Dependencies

- Depends on Step 2
- Parallel-safe with Step 3

---

## Step 5 — Captcha remediation (WAVE C — first of pair)

### Context brief

Instead of 403-banning suspect IPs, send them to a captcha challenge. Legit users solve it and continue; bots fail and get banned. Lower false-positive pain from aggressive scenarios.

The upstream `crowdsec-nginx-bouncer` supports captcha out of the box — it ships hCaptcha / reCAPTCHA integration + a templated challenge page. Enablement is a bouncer-conf edit:
- `CAPTCHA_PROVIDER=hcaptcha` (or `recaptcha`, `turnstile`)
- `SITE_KEY=<public-site-key>`
- `SECRET_KEY=<secret-key-from-provider>`
- `FALLBACK_REMEDIATION=captcha` — optional; if the scenario doesn't specify a remediation, default to captcha instead of ban

Admin workflow:
1. Create an account at hCaptcha / reCAPTCHA / Cloudflare Turnstile
2. Get site_key + secret_key
3. Paste into UI, toggle ENABLED
4. Step 6 (per-scenario) lets admin say which scenarios use captcha

This step is ONLY #1 — the toggle + creds + bouncer conf rewrite. Per-scenario hooks up in Step 6.

Wire:
- `GET /admin/security/crowdsec/captcha` → `{enabled: bool, provider: "" | "hcaptcha" | "recaptcha" | "turnstile", site_key: string}` (secret_key NEVER returned; write-only).
- `PUT /admin/security/crowdsec/captcha` body `{enabled, provider, site_key, secret_key}` — secret_key empty means "don't change" (so admin can enable/disable without re-pasting).

DB: new migration `000068_add_server_settings_captcha.up.sql`:
```sql
ALTER TABLE server_settings
    ADD COLUMN crowdsec_captcha_enabled   BOOLEAN      NOT NULL DEFAULT FALSE,
    ADD COLUMN crowdsec_captcha_provider  VARCHAR(32)  NOT NULL DEFAULT '',
    ADD COLUMN crowdsec_captcha_site_key  VARCHAR(512) NOT NULL DEFAULT '',
    ADD COLUMN crowdsec_captcha_secret_key VARCHAR(512) NOT NULL DEFAULT '';
```

Agent:
- `security.crowdsec.captcha.get` → returns `{enabled, provider, site_key}` from server_settings (via panel-api → DB, NOT from bouncer conf — DB is truth).
- `security.crowdsec.captcha.set` — params `{enabled, provider, site_key, secret_key}`. Writes server_settings. Rewrites the **relevant** keys in `/etc/crowdsec/bouncers/crowdsec-nginx-bouncer.conf` using `sed -i` with per-key anchors (DO NOT rewrite the whole file — M26's `install_crowdsec_nginx_bouncer` wrote AppSec settings into the same file). `systemctl reload nginx`.

Critical: the sed rewrite must idempotently update exactly four lines: `CAPTCHA_PROVIDER=`, `SITE_KEY=`, `SECRET_KEY=`, `FALLBACK_REMEDIATION=`. If `enabled=false`, set `CAPTCHA_PROVIDER=` empty and `FALLBACK_REMEDIATION=ban` (default).

UI: `CaptchaRemediationCard` — Switch + Select (provider) + two Inputs. Secret input is `Input.Password`. Save button with Popconfirm warning.

### Tasks

1. Migration `000068_add_server_settings_captcha.{up,down}.sql`.
2. Model update: `panel-api/internal/models/server_settings.go` — add the four fields with JSON tags.
3. Server-settings repository: no change (generic `Get`/`Save` already handles new fields).
4. Panel-api:
   - `GET /admin/security/crowdsec/captcha` — load server_settings, return `{enabled, provider, site_key}` (NEVER secret_key).
   - `PUT /admin/security/crowdsec/captcha` — bind body; if `secret_key` is empty string, load existing from DB (don't overwrite); save DB; call agent `security.crowdsec.captcha.set` with the merged values.
5. Agent handler `csCaptchaSetHandler`:
   - Validate `provider` in `{"", "hcaptcha", "recaptcha", "turnstile"}`.
   - Read conf, sed-replace each of the four keys, write back via `install -m 0600`.
   - `nginx -t` → on pass, `systemctl reload nginx`; on fail, restore from `.bak` + return error.
6. Registration in agent `init()`.
7. UI hook + `CaptchaRemediationCard` component.
8. Unit tests: agent sed-rewrite on a fixture conf file.
9. Commit: `feat(security-crowdsec): captcha remediation (ADR-0061 follow-up)`.

### Verify

```bash
# Migrations up + down work
cd panel-api && go test -race ./internal/db/...

# Full race-clean
cd panel-api && TMPDIR=/var/tmp go test -race ./...
cd panel-agent && TMPDIR=/var/tmp go test -race ./...

# On VM:
# 1. Enable in UI with hCaptcha test site key (10000000-ffff-ffff-ffff-000000000001)
# 2. Verify conf rewrite:
grep -E '^(CAPTCHA_PROVIDER|SITE_KEY|SECRET_KEY|FALLBACK_REMEDIATION)=' /etc/crowdsec/bouncers/crowdsec-nginx-bouncer.conf
# 3. Reload nginx should succeed
nginx -t
# 4. M26 AppSec conf still intact:
grep -E '^(API_URL|APPSEC_URL|ALWAYS_SEND_TO_APPSEC)=' /etc/crowdsec/bouncers/crowdsec-nginx-bouncer.conf
# → all still present, same values as before
```

### Exit criteria

- Migration up + down clean
- Secret never returned via GET
- Toggle disable → `CAPTCHA_PROVIDER=` empty + `FALLBACK_REMEDIATION=ban`
- M26 AppSec conf untouched by this step's rewrites
- `nginx -t` passes after every Save

### Rollback

Revert commit + run `migrate down 000068`. Bouncer conf: operator runs `install.sh install_crowdsec_nginx_bouncer` to restore (it rewrites the full conf).

### Dependencies

- Depends on Step 2 (Wave A gate)
- Gates Step 6 (Step 6 uses the `captcha_enabled` flag to decide whether captcha is a selectable remediation)

---

## Step 6 — Per-scenario remediation override (WAVE C — second of pair)

### Context brief

`/etc/crowdsec/profiles.yaml` is the LAPI config that maps scenario fires → remediation actions. The default profile catches everything and issues a `ban` decision. We want admin to override specific scenarios to `captcha` (softer, user-friendly) or `off` (logged-only).

Profiles.yaml structure (upstream):
```yaml
name: default_ban
filters:
  - Alert.Remediation == true && Alert.GetScope() == "Ip"
decisions:
  - type: ban
    duration: 4h
on_success: break
---
name: default_captcha
filters:
  - Alert.Remediation == true && Alert.GetScenario() matches "http-bruteforce|http-bad-user-agent"
decisions:
  - type: captcha
    duration: 4h
on_success: break
```

Profiles are YAML multi-doc files, evaluated top-to-bottom with `on_success: break`. Rewriting the whole file means:
- jabali-generated profiles go BEFORE the upstream `default_ban` profile
- Each overridden scenario = one jabali profile
- Unoverridden scenarios fall through to `default_ban`

Wire:
- `GET /admin/security/crowdsec/profiles` → `{scenarios: Array<{name, description, default_action: "ban", override?: "captcha" | "off"}>, captcha_enabled: bool}`
- `PUT /admin/security/crowdsec/profiles` body `{overrides: Array<{scenario, action: "captcha" | "off"}>}`

`captcha_enabled` in GET response is sourced from server_settings (Step 5) — the UI uses it to grey out the captcha option if captcha isn't configured.

Agent:
- `security.crowdsec.scenarios.list` → `cscli scenarios list -o json`. Parse, return `{items: [{name, description}]}`.
- `security.crowdsec.profiles.get` → read `/etc/crowdsec/profiles.yaml`, parse all docs, extract jabali-generated overrides (bounded by `# jabali-begin-overrides` / `# jabali-end-overrides` comment markers), return `{overrides: [...]}`.
- `security.crowdsec.profiles.set` — params `{overrides: [...]}`. Rewrite profiles.yaml: write jabali overrides block between markers, preserve everything outside the markers. Back up `.bak` before write. Validate via `cscli hub list` (no syntax check CLI for profiles — use restart-and-check pattern: `systemctl reload crowdsec`; if it fails-to-reload, restore `.bak` + return error).

UI: `ProfilesCard` — Table listing installed scenarios, each row has a Select (Default / Captcha / Off). Captcha option is disabled if `captcha_enabled=false`. Save button Popconfirms before reload.

### Tasks

1. Agent handlers `csScenariosListHandler`, `csProfilesGetHandler`, `csProfilesSetHandler`.
2. Marker-bounded rewrite: parse existing profiles.yaml, find the `# jabali-begin-overrides` / `# jabali-end-overrides` block (or insert one at file top if absent). Replace contents. Preserve the rest verbatim. **First line of the rewritten block MUST be `# DO NOT HAND-EDIT — rewritten by jabali on Save. Edits inside these markers are lost.`** — operators are going to `vim profiles.yaml` at some point and we need them to see the warning.
3. YAML emission: use `gopkg.in/yaml.v3` (already in go.mod per M26 appsec yaml write) for multi-doc encoding.
4. **Pre-flight validate** — if Step 1 task 2 probe confirmed `crowdsec -t` exists, run it after writing the new file but BEFORE `systemctl reload crowdsec`. On failure: restore from `.bak`, return error with stderr. If `-t` isn't supported, fall back to write → reload → if reload fails restore `.bak`. Either way `.bak` is the safety net; `-t` is the cheaper preflight.
5. Panel-api:
   - `GET /admin/security/crowdsec/profiles` → merge agent `scenarios.list` + `profiles.get` + server_settings.captcha_enabled into one response.
   - `PUT /admin/security/crowdsec/profiles` → bind, validate (if any override is "captcha" but `!captcha_enabled`, reject 400), agent `profiles.set`.
5. UI:
   - `ProfilesCard` — Table; columns: Scenario, Description, Override (Select).
   - SearchableTable wrapper (scenario list can be 50+).
   - Apply button (no Drawer — table row edits inline).
6. Unit tests: profiles.yaml rewrite with + without existing overrides; test that content OUTSIDE the marker block is preserved byte-for-byte.
7. Commit: `feat(security-crowdsec): per-scenario remediation override (ADR-0063)`.

### Verify

```bash
cd panel-api && TMPDIR=/var/tmp go test -race ./...
cd panel-agent && TMPDIR=/var/tmp go test -race ./...

# On VM:
# 1. Override http-bad-user-agent → captcha
# 2. Verify profiles.yaml:
cat /etc/crowdsec/profiles.yaml
# → jabali-begin-overrides / jabali-end-overrides block contains a profile for http-bad-user-agent with type: captcha
# 3. crowdsec still loads:
systemctl status crowdsec | grep Active
# → active
# 4. Hit a bad-UA endpoint to fire the scenario; observe captcha challenge in curl:
curl -H "User-Agent: sqlmap" https://test-vhost.local/
# → 200 with captcha HTML (instead of the normal endpoint body)
```

### Exit criteria

- Marker-bounded rewrite preserves everything outside jabali block
- `.bak` restore works on reload failure
- Override → captcha shows captcha challenge on VM
- Captcha option greyed out in UI if Step 5's `captcha_enabled=false`

### Rollback

Revert commit. Restore profiles.yaml from `.bak` on the host, or re-run `install.sh install_crowdsec_profiles` (it won't touch an existing file; restore manually).

### Dependencies

- Depends on Step 5 (needs `captcha_enabled` flag; UI greys out captcha option)

---

## Step 7 — Runbook + E2E + ADR polish + VM smoke (FINALE)

### Context brief

Close out M27: document the five new features in the runbook, add E2E specs, polish the three ADRs with any lessons learned during implementation, and validate on the VM testbed end-to-end.

Runbook: extend `plans/m26-security-tab-runbook.md` (not a new file — M27 is a M26 extension). New sections:
- Allowlists — how to add / remove, relation to decisions
- Alerts view — reading alerts vs decisions
- CrowdSec Console — enrolment walkthrough, what gets sent, privacy notes
- Captcha remediation — provider setup, test site keys, fallback behaviour
- Per-scenario overrides — how profiles.yaml evaluates, order matters

E2E (Playwright): extend `panel-ui/e2e/admin-security.spec.ts` (M26's spec):
- Add to allowlist + remove
- Alerts table renders + row-click opens Drawer
- Console card shows one of three states
- Captcha toggle + provider select
- Per-scenario override table saves

VM smoke: full five-feature walkthrough on `ssh -p 2222 root@192.168.100.150`. Screenshot or transcript appended to the PR.

### Tasks

1. Extend `plans/m26-security-tab-runbook.md` with five new sections.
2. Extend `panel-ui/e2e/admin-security.spec.ts` with five new test blocks.
3. Re-check the three ADRs (0061, 0062, 0063) against the shipped code — amend Context/Consequences if implementation drifted.
4. VM smoke walkthrough, transcript saved to `plans/m27-vm-smoke.txt`.
5. `git fetch origin main && git rebase origin/main`.
6. Run full test suite post-rebase (build BEFORE test — TS/Go type change on main can pass tests but fail build):
   - `cd panel-api && TMPDIR=/var/tmp go vet ./... && go build ./... && go test -race ./...`
   - `cd panel-agent && TMPDIR=/var/tmp go vet ./... && go build ./... && go test -race ./...`
   - `cd panel-ui && npm run typecheck && npm run build && npm test`
7. Open PR from `m27/crowdsec-extensions` → `main`. Commit: `docs(m27): runbook + E2E + VM smoke`.
8. Update `~/.claude/projects/-home-shuki-projects-jabali2/memory/MEMORY.md` with the M27 SHIPPED entry.

### Verify

```bash
# rebase clean
git fetch origin main
git rebase origin/main  # fast-forward OR minor conflicts in MEMORY.md only
git log --oneline main..HEAD

# all tests green post-rebase
cd panel-api && TMPDIR=/var/tmp go test -race ./...
cd panel-agent && TMPDIR=/var/tmp go test -race ./...
cd panel-ui && npm run typecheck && npm run build

# E2E green (needs running stack)
cd panel-ui && npx playwright test admin-security
```

### Exit criteria

- PR open, rebased onto latest `origin/main`, all CI checks green
- VM smoke transcript attached
- Runbook covers all five features
- Memory entry added

### Rollback

Revert the entire branch at PR-level (single-revert PR). Migration `000068` rollback via `migrate down 000068`. Bouncer conf: operator re-runs `install.sh install_crowdsec_nginx_bouncer`. Profiles.yaml: operator restores `.bak`.

### Dependencies

- Depends on Steps 1-6 complete

---

## Anti-patterns (closed regressions — do not reopen)

- **Rewriting the whole bouncer conf in Step 5** — will clobber M26's AppSec settings. Use per-key sed, not `cat > conf`. Covered by: Step 5 exit criterion "M26 AppSec conf untouched."
- **Storing the CrowdSec Console enrollment token in jabali DB** — token is machine-scoped; jabali re-querying via `cscli console status` is cheap + keeps jabali out of the loop on token rotation. Covered by: ADR-0062.
- **Returning secret_key from `GET /admin/security/crowdsec/captcha`** — admin UI only needs enabled state + provider + site_key (site_key is public). Secret is write-only. Covered by: Step 5 exit criterion.
- **Rewriting the full profiles.yaml without markers** — profiles.yaml ships with upstream defaults that jabali must NOT touch. Marker-bounded block keeps upstream config intact. Covered by: Step 6 task 2.
- **Skipping the .bak restore on reload failure** — if `systemctl reload crowdsec` fails after a profiles write, jabali MUST restore from `.bak` and return the error. Otherwise LAPI is left with a broken config. Covered by: Step 6 agent handler.
- **Creating a new panel-api file per feature** — extend `RegisterSecurityCrowdsecRoutes` in the existing `security_crowdsec.go`. Same for panel-agent.
- **Using Modal instead of Drawer for create** — docs/CONVENTIONS.md rule. All five UI cards use Drawer where a form is needed (or no form for read-only alerts).
- **Forgetting `destroyOnClose` on Drawer** — state leaks between opens. Every Drawer in this plan must set it.
- **Skipping `--race` on tests** — Go rule. Every test run command in this plan uses `-race`.
- **Acting on scenario "overrides" without validating captcha_enabled first** — Step 6 panel-api validates: override with action=captcha when captcha_enabled=false → 400. Prevents a broken profiles.yaml from shipping.
- **Merging Step 6 before Step 5** — Step 6's UI checks captcha_enabled from Step 5's endpoint. Reverse order breaks typecheck.
- **Not rebasing before final PR** — repo rule from CLAUDE.md. Step 7 task 5 is mandatory.
- **Using `:value` path param for CIDR DELETE** — Gin's `:value` matches one segment, `/192.0.2.0/24` won't route. Use `?value=` query param (Step 2).
- **Assuming `cscli allowlists` subcommand names** — probe the VM first (Step 1 task 2). CrowdSec minor releases rename subcommands without deprecation.
- **Hand-edit inside `jabali-*-overrides` markers gets silently clobbered** — Step 6 writes a prominent warning comment inside the block. If an operator needs stable custom profiles, they put them OUTSIDE the markers.
- **Running tests without building first** — post-rebase on main, TS/Go type changes can pass tests while failing build. Step 7 task 6 runs `go vet && go build && go test` + `npm run typecheck && npm run build && npm test` in that order.

## Step summary

| Step | Wave | Feature | Depends on | Files touched |
|------|------|---------|------------|---------------|
| 1    | Sprint 0 | ADRs + install.sh + VM probe | — | 3 ADRs + install.sh + plans/m27-probe.txt |
| 2    | A (gate) | Allowlists | 1 | security_crowdsec.go (both) + hook + card |
| 3    | B | Alerts view | 2 | same four files |
| 4    | B | Console enrollment | 3 | same four files |
| 5    | C (gate for 6) | Captcha remediation | 4 | mig 000068 + model + same four files |
| 6    | C | Per-scenario override | 5 | same four files |
| 7    | Finale | Runbook + E2E + smoke | 1-6 | m26 runbook + e2e spec |

Total: 7 steps. All serial (no sub-agent dispatch per memory rule). Dependency chain is linear: 1 → 2 → 3 → 4 → 5 → 6 → 7.

## Plan mutation protocol

If during execution a step reveals a constraint not in this plan:
- **Split** — add Step 3a / 3b; renumber downstream; note in this plan's audit trail below.
- **Insert** — add e.g. Step 2.5 for an unplanned prereq; note in audit trail.
- **Skip** — if a step turns out unnecessary, mark `SKIPPED` + reason; don't delete.
- **Abandon** — if infeasible, re-enter Blueprint with the new objective; close M27 branch without merge.

### Audit trail

(empty — no mutations yet)
