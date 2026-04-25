# M29 — Admin Updates + Support

**Goal.** Two new admin-only pages:

1. `/jabali-admin/updates` — System Updates
   - **Card 1: Jabali Panel** — `Check for updates` button. If behind: `Update Jabali panel` button → runs `jabali update -f` end-to-end (npm ci + go build + migrations + restart).
   - **Card 2: System Packages** — `Check for updates` button → `apt-get update` + `apt list --upgradable`. If non-empty: a 3-column table (Package / Current / New) plus `Apply updates` button → `apt-get -y dist-upgrade`.

2. `/jabali-admin/support` — Support
   - 4 info cards: Documentation / Report a Bug / Paid Support / Emergency Support (per mockup, red primary CTAs except Emergency = yellow).
   - `Send Diagnostic Report` button top-right → backend collects `{hostname, panel sha, OS info, service status (jabali-panel/jabali-agent/pdns/mariadb/kratos/stalwart/redis/nginx/php-fpm-pools), last 200 journal lines per service}`, encrypts the bundle to a hard-coded `age` recipient (Jabali team's pubkey), returns base64. UI shows a copy-paste textarea + "Copy to clipboard" button. Operator pastes into a GitHub issue; only the team can decrypt.

Branch: `m29/admin-updates-support`. Default mode: branch + ff-merge into `main` after every step (shared codebase, multiple agents — same workflow as M14/M15/M26).

ADR target: **0064** for the `age`-encrypted diagnostic flow. ADR `0064` is free (last on main: `0063-profiles-yaml-for-remediation-override.md`).

---

## Constraints + invariants

- **Single-tenant feature**: admin-only, gated by `RequireAdmin()` middleware on every endpoint. User shell never sees these routes.
- **Long-running ops live in jabali-agent (root). Panel-api spawns nothing.** `panel-api` runs as the unprivileged `jabali` user; root-only operations (`jabali update`, `apt-get`, journalctl reads on system services) MUST go through agent commands. This is the existing pattern (php_ext_apply, etc.).
- **One run-job at a time per kind.** No concurrent `jabali update` / `apt run` on the same host. Backend stores a single in-memory job slot per kind; second call returns 409 with the running `job_id`.
- **Run jobs as transient systemd units, NOT as agent children.** `jabali update -f` restarts jabali-agent itself; an in-cgroup child gets SIGTERM mid-run and never reaches its own "→ restart services" step. Same for `apt-get dist-upgrade` when it pulls in libc / openssh / mariadb. **Every long-running root command runs as `systemd-run --unit=jabali-<kind>-oneshot.service /usr/local/bin/...`** so the agent restart can't kill it. Job status reads via `systemctl is-active jabali-<kind>-oneshot.service` + `journalctl -u jabali-<kind>-oneshot.service --since=<start>` — surviving an agent bounce. The in-process ring buffer is GONE.
- **`apt` lock contention.** Every `apt-get`/`apt list` invocation MUST include `-o DPkg::Lock::Timeout=60` because `unattended-upgrades.timer` runs nightly and holds `/var/lib/dpkg/lock-frontend`. Without the timeout the operator sees a cryptic crash.
- **Diagnostic encryption**: hard-coded `age` recipient pubkey lives in `internal/diagnostic/recipient.go` (constant). Private key NEVER on disk anywhere. Operator can paste the ciphertext anywhere safely.
- **No reconciler**: every flow is operator-initiated, fire-and-forget; no DB-as-truth state to converge.
- **Migrations: none.** Pure code.
- **Existing migration high-water-mark on main: 000070** (M15 DNSSEC). M29 reserves nothing.
- **Support links**: real URLs only. Placeholder `*.example` values must NOT ship to `main`. Hold Step 6 until the product team supplies the four URLs (Documentation / GitHub Issues / Paid Support / Emergency Support). If a URL is empty when Step 6 runs, the corresponding card hides the CTA button entirely and shows a "Coming soon" tag — better than a dead link.
- **Anti-pattern: don't shell out from panel-api.** Even when convenient. The `jabali update` self-rebuild path used to do this and was retired; M29 must not regress.

---

## Step 1 — agent commands: `system.update_check` + `system.update_run`

**Context brief.** `panel-agent` runs as root via `jabali-agent.service`. Already has a command registry (`Default.Register`) and unix socket at `/run/jabali/agent.sock`. We add two top-level "system" commands plus their parsers + tests. They drive `jabali update -f` and `git fetch --quiet origin main && git rev-list --count HEAD..origin/main` respectively. Stdout streams back over the same socket via the existing event-frame channel (search `agentwire.WriteEvent` for the helper).

**Files to add.**
- `panel-agent/internal/commands/system_update_check.go`
  - command name `system.update_check`
  - params: none
  - response `{current_sha, remote_sha, behind_count, branch}`
  - shells: `git -C /opt/jabali-panel rev-parse HEAD`, `git fetch --quiet origin main`, `git rev-parse origin/main`, `git rev-list --count HEAD..origin/main`
  - runs as `jabali` user (sudo -u) like `update.go` already does
- `panel-agent/internal/commands/system_update_run.go`
  - command name `system.update_run`
  - params: `{force?: bool}`
  - exec: `systemd-run --unit=jabali-update-oneshot.service --no-block /usr/local/bin/jabali update -f`
  - returns immediately: `{unit: "jabali-update-oneshot.service", started_at: <now>}` — the process is detached from the agent's cgroup so a panel/agent restart mid-update doesn't kill it
- `panel-agent/internal/commands/system_update_status.go`
  - command name `system.update_status`
  - params: `{since: <RFC3339>}`
  - reads `systemctl is-active jabali-update-oneshot.service` + `journalctl -u jabali-update-oneshot.service --since=<since> --no-pager -n 4096 -o cat`
  - response: `{status, log_tail, exit_code?}`
- `panel-agent/internal/commands/system_update_check_test.go` + `system_update_run_test.go`
  - parser tests (no real subprocess); golden fixtures for `git fetch` no-op output, "behind" output, and `jabali update` final-line capture

**Verification.**
- `go test ./panel-agent/internal/commands/...` green.
- Live: `printf '{"id":"t","command":"system.update_check","params":{}}' | socat -T10 UNIX-CONNECT:/run/jabali/agent.sock -`
- Live: trigger `system.update_run`, confirm at least one event frame arrives, then a final result frame.

**Rollback.** Delete the two .go files + tests. No migration.

**Exit criteria.** Both handlers registered; tests pass; live VM emits at least one streamed event from `system.update_run`.

---

## Step 2 — agent commands: `system.apt_check` + `system.apt_run`

**Context brief.** Mirrors Step 1 but for the OS package set.

**Files to add.**
- `panel-agent/internal/commands/system_apt_check.go`
  - all apt invocations include `-o DPkg::Lock::Timeout=60` to wait out `unattended-upgrades.timer`
  - env `LC_ALL=C` for stable column headers
  - `apt-get -o DPkg::Lock::Timeout=60 update -qq`
  - then `apt list --upgradable 2>/dev/null`. Parse to `[]{name, current, new, source}`.
  - response: `{packages: [...], total: N}`
- `panel-agent/internal/commands/system_apt_run.go`
  - **runs as a transient systemd unit** like Step 1's update path:
    `systemd-run --unit=jabali-apt-oneshot.service --no-block --setenv=DEBIAN_FRONTEND=noninteractive --setenv=LC_ALL=C apt-get -y -o Dpkg::Options::="--force-confdef" -o Dpkg::Options::="--force-confold" -o DPkg::Lock::Timeout=120 dist-upgrade`
  - returns immediately: `{unit: "jabali-apt-oneshot.service", started_at: <now>}`
- `panel-agent/internal/commands/system_apt_status.go`
  - mirrors `system.update_status` for `jabali-apt-oneshot.service`
- `panel-agent/internal/commands/system_apt_*_test.go`
  - golden fixtures for `apt list --upgradable` output (current Debian 13)
  - golden fixture for `apt-get dist-upgrade` summary tail line

**Anti-injection.** `apt list --upgradable` accepts no params; `apt-get dist-upgrade` only accepts the `force` flag in the request, never a package list. No user-controlled string ever reaches the apt CLI. Document this in the file header.

**Verification.**
- `go test ./panel-agent/internal/commands/...` green.
- Live VM: `apt_check` returns the same set as `apt list --upgradable` shows manually.

**Rollback.** Delete the four files.

**Exit criteria.** Both handlers registered; tests green; parser handles current Debian 13 output exactly.

---

## Step 3 — panel-api admin endpoints + in-memory job slots

**Context brief.** Mount four endpoints under the existing admin route group. Job-slot is a tiny per-kind singleton: `map[kind]*Job` guarded by a mutex; `kind ∈ {"jabali","apt"}`. Each Job has `{ID, Kind, Status, Tail (ring 4 KB), StartedAt, FinishedAt, Result}`. New invocation when one is `Running` for the same kind returns 409 with the existing `id`.

**Files.**
- `panel-api/internal/api/admin_updates.go` — `RegisterAdminUpdatesRoutes(g *gin.RouterGroup, agent agent.AgentInterface)`
  - `GET  /admin/updates/jabali/check` — wraps `system.update_check`
  - `POST /admin/updates/jabali/run` — wraps `system.update_run`, returns `{unit, started_at}`
  - `GET  /admin/updates/jabali/status?since=…` — wraps `system.update_status`
  - `GET  /admin/updates/apt/check` — wraps `system.apt_check`
  - `POST /admin/updates/apt/run` — wraps `system.apt_run`, returns `{unit, started_at}`
  - `GET  /admin/updates/apt/status?since=…` — wraps `system.apt_status`
  - `DELETE /admin/updates/jabali` / `DELETE /admin/updates/apt` — calls a new `system.unit_stop` agent command (`systemctl stop jabali-{update,apt}-oneshot.service`) for a kill-switch
- **No in-memory job manager.** State lives in systemd; the API is a thin proxy.
- `panel-api/internal/api/admin_updates_test.go` — table-driven: 401 (no claims), 403 (non-admin), 200 happy path with mock agent, 404 on unknown unit.

**Wiring.** Add `RegisterAdminUpdatesRoutes` to `panel-api/internal/app/app.go` next to the existing `RegisterDomainDNSSECRoutes`. Pass `deps.Agent` (already plumbed).

**Verification.**
- `go test ./panel-api/...` green.
- `curl -s --unix-socket /run/jabali/panel-api.sock 'http://localhost/api/v1/admin/updates/jabali/check'` (admin cookie) returns the agent payload.
- After triggering `run`, `systemctl is-active jabali-update-oneshot.service` returns `active`.

**Rollback.** Delete `admin_updates*.go`; remove the wiring line from `app.go`.

**Exit criteria.** Eight endpoints reachable; RBAC enforced; status survives an `systemctl restart jabali-panel jabali-agent` mid-run.

---

## Step 4 — UI: `SystemUpdatesPage` (admin)

**Context brief.** AntD page mirroring the rest of the admin shell. Two `<Card>`s stacked. Each card has a primary `Check for updates` button on the right. When `check` returns "behind"/"upgradable", a secondary CTA appears in the same card with the `Update jabali panel` / `Apply updates` label. When a run is in flight, the CTA flips to `<Button loading>` and a `<pre>`-style log tail block appears under the card body, fed by 2-second polling on `/admin/updates/jobs/:id`.

**Files.**
- `panel-ui/src/hooks/useSystemUpdates.ts` — TanStack Query wrappers for the four endpoints + `useJobPoll(id)`.
- `panel-ui/src/shells/admin/updates/SystemUpdatesPage.tsx`
  - 2 cards, AntD `Card` with `extra={<Button>}` for the right-aligned CTA matching CONVENTIONS.md.
  - Apt card uses `<Table>` with `scroll={{ x: "max-content" }}` (per ADR-0046).
  - Empty state: `<Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description="System is up to date" />`.
  - Log tail: `<Typography.Text code>` wrapping a `pre` with `whiteSpace: "pre-wrap"`; auto-scrolls to bottom.
- `panel-ui/src/components/JobLogTail.tsx` — reusable: takes `jobId`, polls, renders log lines + status badge.

**Verification.**
- `npx tsc --noEmit` clean.
- `npx vite build` clean.
- Live: trigger from the panel; observe streamed lines appearing within 2 s of starting.

**Rollback.** Delete the three new files; nav entry stays out until Step 7.

**Exit criteria.** Page renders, both cards work end-to-end against a real VM, log tail updates live.

---

## Step 5 — diagnostic report (agent + ADR-0064)

**Context brief.** `system.diagnostic_report` collects:
- `uname -a`, `cat /etc/os-release`, `uptime`, `free -h`, `df -h`
- `git -C /opt/jabali-panel rev-parse HEAD`, `git status --porcelain`
- For every service in `[jabali-panel, jabali-agent, pdns, pdns-recursor, mariadb, redis, nginx, jabali-stalwart, jabali-webmail, jabali-kratos]`: `systemctl is-active`, `systemctl status --no-pager -n 0`, `journalctl -u <svc> -n 200 --no-pager`
- `dpkg-query -W` summary
- output of `ss -tnlp` and `iptables -L INPUT -n`

**Redaction (mandatory before encrypting).** Logs and process listings routinely contain DSN passwords, session cookies, API tokens. Encrypted-to-team is not a license to be careless — ciphertext lives forever in GitHub issues, and a single private-key compromise turns every past report into a credential-stuffing buffet.

`internal/diagnostic/redact.go` runs every collected file through these passes BEFORE `age` encryption:
- `password=\S+`               → `password=REDACTED`
- `--password=\S+`             → `--password=REDACTED`
- `(mysql|postgres)://[^:/]+:[^@]+@` → `$1://USER:REDACTED@`
- `(?i)Cookie:\s*\S+`          → `Cookie: REDACTED`
- `(?i)(token|secret|api[_-]?key|authorization)["\s:=]+\S+` → `$1=REDACTED`
- `ory_kratos_session=\S+`     → `ory_kratos_session=REDACTED`
- `Bearer\s+\S+`               → `Bearer REDACTED`

Every redaction pattern has a unit test fed by a synthetic log line proving it matches AND that the surrounding context (timestamps, log-level, hostname) is preserved.

The bundle is a tar of redacted `.txt` files in memory. Encrypt with `age` (single recipient pubkey baked into the binary). Return `{ciphertext_b64, byte_count, generated_at, redaction_count}` so the operator can see how many secrets were stripped.

ADR-0064: choose `age` over PGP because:
- single dependency (`filippo.io/age`), no GnuPG keyring on host
- text-armored output is paste-friendly
- no key-server / web-of-trust trap; the recipient is the Jabali team's static pubkey

**Files.**
- `panel-agent/internal/diagnostic/diagnostic.go` — collection helpers + age encryption.
- `panel-agent/internal/diagnostic/redact.go` — regex-driven redactor; exported `Redact(b []byte) (out []byte, count int)`.
- `panel-agent/internal/diagnostic/redact_test.go` — synthetic-log corpus for every regex. Coverage 100% on this file.
- `panel-agent/internal/diagnostic/recipient.go` — `const RecipientPublicKey = "age1..."` (placeholder until the team generates the real keypair; documented in ADR).
- `panel-agent/internal/diagnostic/diagnostic_test.go` — collector returns expected file set, encryption round-trips with a test keypair, redaction runs before encryption.
- `panel-agent/internal/commands/system_diagnostic_report.go` — `system.diagnostic_report` handler.
- `docs/adr/0064-diagnostic-report-age-encryption.md` — full ADR. Decision section MUST include a "Why redact even when encrypted" subsection with the credential-stuffing-buffet rationale.

**go.mod.** `go get filippo.io/age` adds one direct dep.

**Verification.**
- `go test ./panel-agent/internal/diagnostic/... ./panel-agent/internal/commands/...` green.
- Live: invoke handler, decrypt locally with the test private key (kept off-host).

**Rollback.** Delete the four files; revert `go.mod`/`go.sum`.

**Exit criteria.** Handler returns base64 age ciphertext; round-trip decrypt with a test key works; ADR-0064 merged.

---

## Step 6 — UI: `SupportPage` (admin)

**Context brief.** AntD page matching the mockup [Image #25].

Layout:
- Title "Support" left; `Send Diagnostic Report` button top-right (header strip, not inside any card).
- 4-column responsive grid (`<Row gutter>` with `<Col xs=24 md=12 lg=6>`).
- Each card: header with icon + title; body paragraph; footer button.
- Documentation/Paid Support → red `type="primary"` AntD button; Report a Bug → outline; Emergency Support → yellow CTA (`style={{ background: "#FFC107", borderColor: "#FFC107", color: "#000" }}` or use AntD's `danger=false` + custom theme token).

Diagnostic flow:
- Click `Send Diagnostic Report` → `<Modal>` opens, calls `POST /admin/support/diagnostic`, shows `<Spin>` while collecting (~5–15 s).
- Result: `<Input.TextArea value={ciphertext} rows={12} readOnly />` + `<Button onClick={copy}>Copy to clipboard</Button>` + a callout: "Paste in your GitHub issue. Only the Jabali team can decrypt this."

**Files.**
- `panel-api/internal/api/admin_support.go` — `POST /admin/support/diagnostic`. Calls `system.diagnostic_report`. Returns the agent's response verbatim.
- `panel-api/internal/api/admin_support_test.go` — RBAC + happy path with mock agent.
- `panel-ui/src/hooks/useSupport.ts` — `useDiagnosticReport()` mutation (returns ciphertext).
- `panel-ui/src/config/support-links.ts` — exported consts.
- `panel-ui/src/shells/admin/support/SupportPage.tsx` — the page.
- `panel-ui/src/shells/admin/support/DiagnosticReportModal.tsx` — modal.

**Verification.**
- `npx tsc --noEmit`.
- `npx vite build`.
- Visual diff against [Image #25] (caller verifies in browser).

**Exit criteria.** Page renders pixel-close to mockup; diagnostic round-trips end-to-end.

---

## Step 7 — wiring + nav + routes + runbook + memory

**Context brief.** Final glue: nav entries, routes, runbook, memory.

**Edits.**
- `panel-ui/src/nav.ts` — append two `adminNav` entries:
  - `{key: "updates", label: "Updates", icon: navIcon(CloudDownloadOutlined), path: "/jabali-admin/updates"}`
  - `{key: "support", label: "Support", icon: navIcon(LifeBuoyOutlined), path: "/jabali-admin/support"}`
  - Verify both icons exist in `panel-ui/src/icons/index.tsx`. If `LifeBuoyOutlined` is missing, add it (lucide-react has `LifeBuoy`).
- `panel-ui/src/App.tsx` — admin route group:
  - `<Route path="updates" element={<SystemUpdatesPage />} />`
  - `<Route path="support" element={<SupportPage />} />`
- `plans/m29-admin-updates-support-runbook.md` — operator notes for emergencies (cancel a stuck `jabali update`, recover from failed `apt dist-upgrade`, where ciphertext goes, how to rotate the age recipient).
- Memory: write `project_m29_admin_updates_support.md` + index line.

**Verification.**
- `go test ./...` + `npx tsc --noEmit` + `npx vite build` all green from a fresh checkout of the merged `main`.
- E2E sanity on VM 192.168.100.150: navigate to both pages, click `Check for updates` (jabali + apt), click `Send Diagnostic Report`, confirm copy-button works.

**Exit criteria.** Both pages reachable from the admin sidebar; runbook + memory present; live VM smoke clean.

---

## Step graph

```
1 ── 3 ── 4 ──┐
2 ──┘         │
              7
5 ── 6 ───────┘
```

- **Parallel after Step 0 (research, done):** Steps 1, 2, 5 can be implemented concurrently (no shared files).
- **Serial:** 3 depends on 1+2; 4 depends on 3; 6 depends on 5; 7 depends on 4+6.

In practice (per `feedback_never_agents`), execute serially inline: 1 → 2 → 3 → 4 → 5 → 6 → 7. Each step ends with `git commit` on `m29/admin-updates-support`; only Step 7 ff-merges into `main` and pushes.

---

## Risks + open questions (resolved during advisor review)

1. **Long-running `jabali update` killed by its own restart step.** RESOLVED — every long-running root command runs as a transient `systemd-run --unit=jabali-<kind>-oneshot.service` so the agent restart can't reach it. State + log tail come from `journalctl -u <unit>`. Survives a panel + agent bounce mid-run.
2. **Diagnostic bundle would leak credentials.** RESOLVED — Step 5 mandates the redaction pass before encryption. Encryption-to-team is not a substitute (ciphertext lives forever; private-key compromise = retroactive credential dump).
3. **`apt` lock contention from `unattended-upgrades`.** RESOLVED — every apt invocation includes `-o DPkg::Lock::Timeout=60` (120 s for the dist-upgrade run).
4. **`apt-get dist-upgrade` blast radius.** Operator-policy issue: runbook tells them to snapshot first. No simulation/preview step in this milestone — out of scope.
5. **age recipient pubkey rotation.** ADR-0064 §rotation: bump the constant + cut a release. Old ciphertexts remain decryptable as long as the team retains the old private key.
6. **Panel-api privilege.** Confirmed runs as `jabali`, not root. All root work routes through agent.
7. **apt locale.** `LC_ALL=C` set in the env on every apt invocation; already in Step 2 file headers.
8. **Placeholder support URLs blocked.** Step 6 will not ship `*.example` URLs; cards hide the CTA when the const is empty.

---

## Definition of done

- 7 commits on `m29/admin-updates-support`, ff-merged into `main`, pushed.
- `jabali update -f` on `192.168.100.150` succeeds post-merge.
- New admin sidebar entries `Updates` and `Support` render and link correctly.
- `Check for updates` (both kinds) returns within 5 s.
- A diagnostic report decrypts cleanly with the test private key.
- ADR-0064 in `docs/adr/`.
- Runbook in `plans/m29-admin-updates-support-runbook.md`.
- Memory entry in `project_m29_admin_updates_support.md`; index line in `MEMORY.md`.
