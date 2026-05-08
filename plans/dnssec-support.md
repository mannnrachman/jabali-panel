# DNSSEC — Per-domain signing via PowerDNS

**Objective**: Add a DNSSEC page to the panel where an operator (admin or user) can enable or disable DNSSEC per domain and copy the DS record set for the domain's registrar. Table columns follow the mockup: **Domain, Owner, DNSSEC status, Keys, Enable/Disable**.

**Scope**: Single new page mounted twice (admin sees all domains, user sees only their own). Signing state is produced by PowerDNS via `pdnsutil`; the panel stores the operator's intent (`dnssec_enabled` bool) and a small cache of the last-observed key set. Reconciler converges `pdnsutil` state to match DB intent. DS records are read-through on demand for the "copy to registrar" modal.

**Not in scope** (enumerated to head off scope creep):

- Automatic DS publication to a registrar. The registrar step is manual — we show the DS record set for the operator to paste.
- Custom key algorithms beyond PowerDNS's default for `secure-zone` (ECDSAP256SHA256 / algorithm 13). Algorithm choice is a config-time decision, not per-domain UI.
- Key rollover UX. One KSK + one ZSK per zone; rollover requires removing and re-enabling, which the UI allows but does not automate.
- NSEC3 opt-out, "narrow" mode, or custom salt management. We let PowerDNS pick default NSEC3 parameters at enable time and don't expose them.
- Signing of zones that are not PowerDNS-backed. The panel only manages zones in `jabali_pdns`.
- Signing metrics / dashboards. A future M-observability feature.

**Branch**: `dnssec/support` (integration branch; sub-steps land via `dnssec/support/<step-slug>` feature branches; umbrella merges to `main` only after Step 6 green on the test VM).

**Anchors** (verified 2026-04-24 against `origin/main` @ `87d0ad7`):

- **PowerDNS backend**: `panel-agent/internal/pdns/client.go` — the agent talks to PowerDNS by writing the `jabali_pdns` database directly inside a transaction. DNSSEC will NOT use that path — we shell out to `pdnsutil` (PowerDNS's sanctioned DNSSEC tool; see https://doc.powerdns.com/authoritative/dnssec/index.html). Writing directly to `cryptokeys` / `domainmetadata` is fragile and bypasses the algorithm-choice logic PowerDNS ships with.
- **Existing DNS agent commands**: `panel-agent/internal/commands/dns_zone_upsert.go`, `dns_zone_delete.go`. The new commands (`dns.dnssec_enable`, `dns.dnssec_disable`, `dns.dnssec_keys_list`, `dns.dnssec_ds_export`) match this `dns_*.go` naming convention.
- **Panel-API DNS surface**: `panel-api/internal/api/` — no existing DNS REST handlers; zone records are handled via the agent from the UI's DNS records page. DNSSEC gets its own handler at `panel-api/internal/api/domain_dnssec.go`.
- **Domain model**: `panel-api/internal/models/domain.go` — `DNSSECEnabled bool` + `DNSSECEnabledAt *time.Time` land as two new columns on `domains` (one migration). Key cache goes to a new `domain_dnssec_keys` table.
- **Reconciler phase registry**: `panel-api/internal/reconciler/phases/registry.go` — M6.5 pattern. New file `m_dnssec.go` implements the `Phase` interface; init() registers at package load.
- **UI nav**: `panel-ui/src/nav.ts` — add one entry "DNSSEC" under both `adminNav` and `userNav` at the end of the DNS-related group (after DNS entry).
- **UI route family**: admin page at `/jabali-admin/dnssec`, user page at `/jabali-panel/dnssec`. Both use the same `DNSSECTable` component from `panel-ui/src/components/dnssec/DNSSECTable.tsx` — the admin page passes `scope="all"`, the user page passes `scope="mine"`. Mirrors `SSLManagerTable.tsx` layout.
- **Empty state**: per `CLAUDE.md` / CONVENTIONS, every `<Empty>` uses `Empty.PRESENTED_IMAGE_SIMPLE`.
- **Icon**: `ShieldCheckOutlined` on nav + enabled-row badge; `ShieldExclamationOutlined` (or `SafetyOutlined` fallback from `@icons`) on disabled rows. Match the mockup shield-exclamation glyph.
- **Latest migration on main**: `000066_add_modsec_columns`. Next = **000067**.
- **AntD patterns**: use `Card.tabList` NOT `Tabs` for grouping if we ever split "Signed" / "Unsigned" views — skip splitting in v1, single flat table is enough. AntD lookups use the `antd` skill + `mcp__antd__antd_doc` MCP (see `docs/CONVENTIONS.md`).

**Version pins** (none new; DNSSEC uses the PowerDNS that install.sh already provisions):

- PowerDNS authoritative 4.9 (ADR-0011)
- `pdnsutil` from the same package — the binary at `/usr/bin/pdnsutil`.

**Target ADR**:

- **ADR-0076** (draft in Step 1, accept in Step 6): "Per-domain DNSSEC via pdnsutil shell-out". Records four decisions: (a) shell out to `pdnsutil` instead of writing `cryptokeys` directly, (b) algorithm 13 (ECDSAP256SHA256) is the only supported algorithm in v1 — leave `pdnsutil secure-zone` defaults, (c) the panel stores `dnssec_enabled` intent + a key cache, never the private key material, (d) DS retrieval is read-through via `pdnsutil export-zone-ds` on user demand, not cached (registrars need the current active KSK's DS; caching invites staleness during rollover).

**Dependencies** (all shipped on main):

- M3 DNS (PowerDNS backend + zones + record CRUD)
- M6.3 pdns-recursor (split-port + recursor on :53) — unaffected; DNSSEC is an authoritative-side concern.
- M24 IP Manager (per-domain listen IP + apex DNS) — DNSSEC does not change IP binding.
- Panel unix socket lockdown (M25) — agent still runs as root; `pdnsutil` runs locally; no loopback TCP reopened.

---

## Waves & Parallelism

| Wave | Steps | Mode | Gate |
|------|-------|------|------|
| **A** Foundation | 1 | serial | Must complete before Wave B |
| **B** Core | 2, 3 | **parallel** | Both green → Wave C |
| **C** Reconciler + UI | 4, 5 | **parallel** | Both green → Wave D |
| **D** Ship | 6 | serial | Live smoke on 192.168.100.150 before merge |

Wave B parallelism is safe: Step 2 (agent commands) and Step 3 (panel-API routes) touch disjoint packages. Step 3 stubs the agent calls against the already-registered command names — Step 1 locks the contract.

Wave C parallelism is safe: Step 4 (reconciler phase) lives in `panel-api/internal/reconciler/phases/m_dnssec.go`; Step 5 (UI) in `panel-ui/src/**`. No shared files.

---

## Step 1 — Foundation: ADR, migration, command names, UI scaffold

**Model tier**: strongest (locks contracts for all later steps).
**Branch**: `dnssec/support/01-foundation`.
**Blocks**: Steps 2, 3, 4, 5.

### Context brief

Lock every cross-cutting contract so Waves B/C land without collisions.

1. ADR-0076 draft in `docs/adr/0057-dnssec-per-domain-pdnsutil.md` — four decisions listed above.
2. Migration `000067_add_dnssec_to_domains.up.sql` + `.down.sql`:
   - `ALTER TABLE domains ADD COLUMN dnssec_enabled BOOLEAN NOT NULL DEFAULT 0, ADD COLUMN dnssec_enabled_at DATETIME(6) NULL;`
   - `CREATE TABLE domain_dnssec_keys (domain_id BIGINT NOT NULL, key_tag INT NOT NULL, key_type ENUM('KSK','ZSK','CSK') NOT NULL, algorithm TINYINT UNSIGNED NOT NULL, public_key TEXT NOT NULL, active BOOLEAN NOT NULL DEFAULT 1, observed_at DATETIME(6) NOT NULL, PRIMARY KEY (domain_id, key_tag), FOREIGN KEY (domain_id) REFERENCES domains(id) ON DELETE CASCADE) ENGINE=InnoDB;` — cache only, never the private half.
   - `.down.sql`: drop the table, drop the columns.
3. Agent command name registration — add empty handler stubs in `panel-agent/internal/commands/` that return `agentwire.CodeUnimplemented`:
   - `dns_dnssec_enable.go`, `dns_dnssec_disable.go`, `dns_dnssec_keys_list.go`, `dns_dnssec_ds_export.go`. Each registers its name via `Default.Register(...)` in `init()`. Step 2 fills the bodies.
4. Panel-API route stubs — new file `panel-api/internal/api/domain_dnssec.go` with a `RegisterDomainDNSSECRoutes(g *gin.RouterGroup, cfg DomainDNSSECHandlerConfig)` that wires an empty handler returning `501 not_implemented`. Main router calls it once. Step 3 fills the handlers.
5. Reconciler phase stub — new file `panel-api/internal/reconciler/phases/m_dnssec.go` with an empty `DNSSECPhase` registered via `init()` + `registry.RegisterPhase(...)`. `Reconcile` just logs "dnssec phase: not yet implemented" and returns `nil`. Step 4 fills it.
6. UI scaffold:
   - Nav entries in `panel-ui/src/nav.ts` — `adminNav` adds `{ key: "dnssec", label: "DNSSEC", icon: navIcon(ShieldCheckOutlined), path: "/jabali-admin/dnssec" }` right after DNS; `userNav` adds the mirror at `/jabali-panel/dnssec`.
   - Routes in `panel-ui/src/App.tsx` — `<Route path="dnssec" element={<AdminDNSSECPage />} />` under `/jabali-admin` and `<Route path="dnssec" element={<UserDNSSECPage />} />` under `/jabali-panel`.
   - New files `panel-ui/src/shells/admin/dnssec/AdminDNSSECPage.tsx`, `panel-ui/src/shells/user/dnssec/UserDNSSECPage.tsx`, `panel-ui/src/components/dnssec/DNSSECTable.tsx`. Scaffold only — header + `<DNSSECTable scope=...>` that renders an `Empty` placeholder. Step 5 fills `DNSSECTable`.
   - Skeleton hook `panel-ui/src/hooks/useDNSSEC.ts` — typed `useListQuery` + `useEnable` / `useDisable` / `useDSExport` mutations that call the stub routes. Step 3 backs them with real responses.

### Files touched in Step 1 only (NEVER again in Wave B/C)

- `panel-api/internal/api/router.go` — one `RegisterDomainDNSSECRoutes(apiV1, ...)` call.
- `panel-api/internal/reconciler/reconciler.go` — no edit; phase auto-registers via `init()`.
- `panel-ui/src/App.tsx` — two new route lines.
- `panel-ui/src/nav.ts` — two new nav entries.

### Tasks

- [ ] Write ADR-0076 with the four decisions.
- [ ] Write migration 000067 up/down.
- [ ] Scaffold four agent command stub files that return `CodeUnimplemented`.
- [ ] Scaffold panel-API stubs + register in router.
- [ ] Scaffold reconciler phase stub.
- [ ] Scaffold UI pages + table component + hook.
- [ ] `go build ./panel-agent/... ./panel-api/...` must pass; `tsc -b` must pass.
- [ ] Run `mcp__codebase-memory-mcp__detect_changes` before committing per CLAUDE.md.

### Verification

- `go vet ./...` clean.
- `go test ./panel-api/internal/db/...` — migration up/down round-trip clean.
- `tsc -b` clean.
- Navigate to `/jabali-admin/dnssec` and `/jabali-panel/dnssec` in a dev browser — each renders the scaffold header + empty placeholder with no 404.

### Exit criteria

- ADR in place, migrations round-trip, all stubs compile, UI pages route correctly. Merge to `main` before Wave B dispatch.

---

## Step 2 — Agent: four `dns.dnssec_*` commands via `pdnsutil`

**Model tier**: default.
**Branch**: `dnssec/support/02-agent-commands`.
**Parallel with**: Step 3.
**Depends on**: Step 1.

### Context brief

Implement the four command bodies. `pdnsutil` is a CLI tool; we run it with `exec.CommandContext` from the agent (which runs as root). Parse its stdout for the structured bits we care about.

Command contracts:

- `dns.dnssec_enable` — params `{domain_name string}`. Runs `pdnsutil secure-zone <domain>` then `pdnsutil set-nsec3 <domain> '1 0 10 ab'` (RFC 5155 default: SHA-1, opt-in, 10 iterations, random salt per zone). After each call, run `pdnsutil rectify-zone <domain>`. Returns `{ok: true, keys: [...]}` parsing `pdnsutil show-zone <domain>` for id / tag / algorithm / type / active / public-key lines. Quotes the exact `pdnsutil` stderr text in `AgentError.Details` on failure.
- `dns.dnssec_disable` — params `{domain_name string}`. Runs `pdnsutil disable-dnssec <domain>` then `pdnsutil rectify-zone <domain>`. Returns `{ok: true}`. Idempotent (calling on an unsigned zone still returns ok with a note).
- `dns.dnssec_keys_list` — params `{domain_name string}`. Runs `pdnsutil show-zone <domain>` and returns a structured `[{key_tag, key_type, algorithm, public_key, active}]`. Empty array if no keys.
- `dns.dnssec_ds_export` — params `{domain_name string}`. Runs `pdnsutil export-zone-ds <domain>` and returns `{ds_records: [{key_tag, algorithm, digest_type, digest}, ...]}` parsing each line.

### Implementation notes

- Validate `domain_name` matches the existing `internal/mailaddr` domain regex — no shell escaping shortcuts. `exec.Command` with `Args=[name, arg1, arg2]` is safe because it never invokes a shell.
- Timeout each `pdnsutil` invocation at 30s via the ctx passed in.
- Parse `pdnsutil show-zone` output line-by-line — the format is stable but free-text; write a small parser in `panel-agent/internal/pdns/dnssec_parse.go` with table-driven tests covering:
  - signed zone with one KSK + one ZSK
  - signed zone after rollover (two KSKs, one pending)
  - unsigned zone (no keys)
- `pdnsutil export-zone-ds` emits each DS as `example.com. IN DS 12345 13 2 abcdef...`. Parse into struct. Tests in `dnssec_parse_test.go`.

### Tasks

- [ ] Write `dns_dnssec_enable.go` + test.
- [ ] Write `dns_dnssec_disable.go` + test.
- [ ] Write `dns_dnssec_keys_list.go` + test.
- [ ] Write `dns_dnssec_ds_export.go` + test.
- [ ] Write `internal/pdns/dnssec_parse.go` + tests.
- [ ] Provide a `pdnsutil` fake (PATH override or test seam) so tests don't need a live PDNS.

### Verification

- `go test ./panel-agent/... -race -count=1` green.
- On test VM: invoke via agent socket for a known test zone (`123123.com`). Confirm `secure-zone` produces keys, `export-zone-ds` returns DS lines, `disable-dnssec` clears. Quote exact agent JSON response in the commit message.

### Exit criteria

- All four commands behave idempotently and return structured data matching the wire contract. `pdnsutil` stderr is surfaced verbatim when it fails.

### Rollback

- `git revert <step2-commit>` — step stands alone; Step 3 routes return 501 from Step 1 until this step lands.

---

## Step 3 — Panel-API: `/domains/:id/dnssec` routes + repository

**Model tier**: default.
**Branch**: `dnssec/support/03-api-routes`.
**Parallel with**: Step 2.
**Depends on**: Step 1.

### Context brief

Three routes under `/api/v1`:

- `GET /domains/:id/dnssec` — returns `{domain_id, domain_name, dnssec_enabled, enabled_at, keys: [{key_tag, key_type, algorithm, public_key, active}]}`. Reads domain row + `domain_dnssec_keys` rows.
- `PUT /domains/:id/dnssec` — body `{enabled: bool}`. On change: update the domain row, then fire-and-log the reconciler (non-blocking — reconciler runs in 60s anyway; this just shrinks the convergence window). Return the new shape.
- `GET /domains/:id/dnssec/ds` — read-through: calls agent `dns.dnssec_ds_export` inline. No caching. Returns `{domain_id, domain_name, ds_records: [...]}`. If the domain isn't DNSSEC-enabled, return `409 not_enabled`.

Repository: `panel-api/internal/repository/dnssec_keys.go` with `UpsertKeys(domainID, []Key)`, `ListByDomainID(domainID)`, `DeleteAllForDomain(domainID)`. `domains.dnssec_enabled` moves through `DomainRepository.UpdateDNSSECEnabled(id, enabled, at)` — extend the existing repo.

Authorization: admin sees all domains; user sees their own only. Copy the pattern used by `domain_catchall.go` — `claims.IsAdmin || dom.UserID == claims.UserID`.

### Tasks

- [ ] Extend `DomainRepository` with `UpdateDNSSECEnabled(ctx, id, enabled, at)`.
- [ ] Write `DNSSECKeyRepository` with `UpsertKeys`, `ListByDomainID`, `DeleteAllForDomain`.
- [ ] Fill `panel-api/internal/api/domain_dnssec.go` with all three handlers.
- [ ] Update mock repositories so existing tests still compile (three mocks: `mockDomainRepo`, `MockDomainRepository`, `fakeDomainRepo`).
- [ ] Handler tests using the existing gin test helper — cover admin-sees-all, user-sees-own, user-forbidden, not-enabled → 409, agent timeout → 502.

### Verification

- `go test ./panel-api/internal/api/...` green.
- `curl -X PUT /api/v1/domains/<id>/dnssec -d '{"enabled": true}'` changes the DB row; reconciler picks it up within 60s.

### Exit criteria

- Full CRUD works through the REST surface; 409 / 502 paths covered; tests at 80% coverage for the handler file.

### Rollback

- `git revert` — stubs from Step 1 return to returning 501.

---

## Step 4 — Reconciler phase: converge `dnssec_enabled` → PowerDNS

**Model tier**: default.
**Branch**: `dnssec/support/04-reconciler-phase`.
**Parallel with**: Step 5.
**Depends on**: Steps 2, 3.

### Context brief

The phase is domain-scoped. For each domain:

1. Read `dnssec_enabled` from DB.
2. Call `dns.dnssec_keys_list` — if `enabled==true && len(keys) == 0` → call `dns.dnssec_enable`. If `enabled==false && len(keys) > 0` → call `dns.dnssec_disable`.
3. After the state-change call (or every tick when already converged), call `dns.dnssec_keys_list` again and upsert into `domain_dnssec_keys`. Old rows that are no longer in the live set get deleted.
4. On any error, log + continue to the next domain — a single failed zone must not block the rest.

Phase file `panel-api/internal/reconciler/phases/m_dnssec.go`. Follows the existing `m65_*.go` phase pattern.

### Tasks

- [ ] Implement `DNSSECPhase.Reconcile(ctx, domain)`.
- [ ] Register via `init()` + `registry.RegisterPhase(&DNSSECPhase{})`.
- [ ] Table-driven phase test with a fake agent + in-memory repo: covers enable, disable, key drift, agent error.

### Verification

- `go test ./panel-api/internal/reconciler/...` green.
- Manual: set `dnssec_enabled=1` via panel for one zone, wait ≤ 60s, `dig @127.0.0.1 <zone> DNSKEY` returns keys; flip off, wait, `DNSKEY` returns empty.

### Exit criteria

- Reconciler converges in both directions without touching zones it shouldn't; key cache tracks PowerDNS's truth.

### Rollback

- `git revert`. Operator-set `dnssec_enabled=1` rows survive in DB until the next redeploy; they're harmless without the phase.

---

## Step 5 — UI: DNSSEC table + DS modal (admin + user)

**Model tier**: default.
**Branch**: `dnssec/support/05-ui`.
**Parallel with**: Step 4.
**Depends on**: Step 3.

### Context brief

One shared component, two scoped pages. Follows `SSLManagerTable` layout.

`panel-ui/src/components/dnssec/DNSSECTable.tsx`:

- Props: `{scope: "all" | "mine"}`.
- Query: `useListQuery<Domain>({resource: "domains", params: {...}})` — admin gets all, user gets own per existing domains endpoint scoping.
- For each domain row: use `useQueries` fanning out to `GET /domains/:id/dnssec` so the status + key count per row is live.
- Columns matching the mockup: **Domain** (sortable), **Owner** (admin-only — hide when `scope=mine`), **DNSSEC** (shield-check green when enabled with `Tag color="green">active</Tag>`; shield-exclamation amber when disabled), **Keys** (count or `—`), **Actions** (`Enable` or `Disable` button; `View DS` button when enabled).
- Search box on top — plain text filter, matches `domain_name` and `owner_email` (admin only).
- Pagination: 10 per page default (matches mockup "Per page 10" dropdown); `pageSizeOptions = [10, 25, 50]`.
- Empty state: `<Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description="No domains to sign yet" />`.

DS modal: triggered by the `View DS` button. Fetches `GET /domains/:id/dnssec/ds`. Renders the DS records as a monospace code block with a "Copy to clipboard" button. Includes a one-liner: "Paste these DS records at your registrar. Changes can take up to 24 hours to propagate."

Confirm-before-disable: `Popconfirm` with "Disabling DNSSEC removes the signing keys. Your domain will stop validating. Continue?" — `okButtonProps={{danger: true}}`.

Icons: `ShieldCheckOutlined` (active), `ShieldExclamationOutlined` or `SafetyOutlined` (inactive) from `@icons` shim — verify the shim has both; add if missing.

### Tasks

- [ ] Add `ShieldExclamationOutlined` to the `@icons` shim if absent (map to a lucide glyph like `ShieldAlert`).
- [ ] Fill `DNSSECTable.tsx`.
- [ ] Fill `AdminDNSSECPage.tsx` + `UserDNSSECPage.tsx` thin wrappers.
- [ ] Fill `useDNSSEC.ts` hook — `useDomainDNSSEC(id)`, `useUpdateDNSSEC()`, `useDSRecords(id)`.
- [ ] Component tests with `@testing-library/react` — mock api client; cover enabled state, disabled state, disable confirmation, DS modal fetch + copy.
- [ ] Playwright E2E spec `e2e/dnssec.spec.ts` — enable on a test zone, wait for status flip, open DS modal, verify DS rows appear.

### Verification

- `tsc -b` + `vitest` green.
- Playwright spec passes on CI.
- Visual smoke on VM: table matches the mockup — domain / owner / shield / keys / action; 10-per-page pager; search box filters.

### Exit criteria

- Admin + user pages ship; DS copy flow works; tests cover both states; empty state uses simple image.

### Rollback

- `git revert`. Nav entries and routes come out with the same commit.

---

## Step 6 — Runbook + ADR-0076 accept + memory + live smoke + merge

**Model tier**: strongest (final ship gate).
**Branch**: `dnssec/support/06-ship`.
**Depends on**: Steps 4, 5.

### Context brief

Final wrap-up, same shape as M6.5 Step 8–9.

### Tasks

- [ ] Write `plans/dnssec-support-runbook.md`: how to enable, what to hand the registrar, how to disable, what to do when `pdnsutil` fails (e.g. zone not in PowerDNS).
- [ ] Move ADR-0076 to **ACCEPTED**.
- [ ] Update `docs/BLUEPRINT.md` with a DNSSEC section.
- [ ] Add memory entries: `project_dnssec_shipped.md` + MEMORY.md index line.
- [ ] Live smoke on test VM: enable DNSSEC on one zone, verify `dig @127.0.0.1 <zone> DNSKEY +short` returns keys, `dig DS <zone>` (via recursor resolving parent) intentionally won't validate until registrar publishes — document this expectation in the runbook.
- [ ] `git merge --no-ff dnssec/support` into `main` after CI green.

### Verification

- Runbook covers the three operator paths (enable / view DS / disable).
- ADR ACCEPTED header present.
- CI green on the umbrella branch.
- Live smoke screenshot in the merge commit body.

### Exit criteria

- `main` at new tip; VM picks up the DNSSEC page on `jabali update -f`; runbook in place; memory updated.

### Rollback

- Revert merge commit; DB columns stay (no destructive migration); operator state (`dnssec_enabled=1` rows) survives but goes inert.

---

## Invariants to re-check after every step

1. `go build ./panel-agent/... ./panel-api/...` green.
2. `tsc -b` green.
3. `mcp__codebase-memory-mcp__detect_changes` on the branch — no unexpected symbols.
4. Test VM: the domain list pages still load; DNSSEC page does not crash on an unsigned domain.
5. `pdnsutil` stderr is surfaced on error — never swallowed into a generic "internal" message.

## Anti-patterns to avoid (learnt from M6/M6.5)

- **No direct writes to `cryptokeys` / `domainmetadata`**. `pdnsutil` is the supported surface.
- **No DS cache**. ADR-0076 item (d) — always read-through.
- **No "secure-zone with custom args"**. v1 uses PDNS defaults. Advanced ops live in the admin DNS records page or a shell.
- **No migration that seeds from app-populated tables** (per `feedback_migration_data_seed_ordering.md`). `domain_dnssec_keys` is populated by the reconciler, never the migration.
- **No `domain_id` as Stalwart id or any cross-service id**. This is purely jabali's internal `domains.id`. (M6.5 got this wrong for catchall — lesson carried forward.)
- **No agent/Task dispatch** per `feedback_never_agents.md`. Every step above is meant to be executed inline by the dispatcher.

## Self-review (in lieu of adversarial sub-agent)

- **Completeness**: all four operator actions (list, enable, disable, get DS) have an agent command, a REST route, a reconciler branch, and a UI affordance. ✓
- **Dependency correctness**: Step 1 scaffolds the contract; Steps 2+3 are genuinely parallel (disjoint files); Step 4 waits on both (agent calls + repo); Step 5 only needs Step 3. Step 6 is the ship gate. ✓
- **Rollback**: every step has a commit-revert strategy. The one destructive action (migration 000067) is in Step 1; its `.down.sql` is written and tested before Wave B dispatches. ✓
- **Security**: `domain_name` validated against regex before reaching `exec.Command`; private key material never leaves `/var/lib/powerdns`; DS endpoint scoped by owner. ✓
- **Failure modes surfaced**: `pdnsutil` not installed (fail loud in Step 2), zone not in PowerDNS (409), agent timeout (502), parse failure on `show-zone` output (explicit tested path in Step 2). ✓
- **One gap to watch**: NSEC3 defaults — `pdnsutil set-nsec3 '1 0 10 ab'` uses SHA-1 + opt-in. This is the modern RFC-appropriate choice, but some registrars still reject NSEC3 in opt-in mode. If a registrar rejects, Step 6 runbook must document the "turn off NSEC3 for this zone" escape hatch: `pdnsutil unset-nsec3 <domain>` + rectify.
