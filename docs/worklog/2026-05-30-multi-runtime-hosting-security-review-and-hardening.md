# 2026-05-30 — Multi-Runtime Hosting: Security Review & Hardening

**Scope:** Code review of all uncommitted git changes implementing ADR-0113
(multi-runtime domain hosting: PHP / Node.js / Python / Go / Docker / static),
followed by remediation of every issue found. No commits or pushes were made;
all changes remain in the working tree on `main`.

**Modules touched:** `panel-api` (Go), `panel-agent` (Go), `panel-ui` (TS/React).

**Verification gate used:** `go build` + `go vet` + `go test` for both Go
modules; `tsc -b` for the UI (the project's real gate — see Notes).

---

## 1. What was reviewed

The changeset under review (ADR-0113 Phase 1 foundation) added:

- Migrations `000148` (`runtime_type` column on `domains`) and `000149`
  (`runtime_services` table).
- `models.RuntimeService` + `EnvVars` JSON type; `RuntimeType` on `Domain`.
- `repository.RuntimeServiceRepository`.
- `services.PortAllocator`.
- API handlers `GET/PATCH/POST /domains/:id/runtime*` + `runtime_type` on PATCH.
- Reconciler convergence (`reconcileRuntimeService`).
- Agent commands: `runtime.apply`, `runtime.deploy`, `runtime.logs`,
  `runtime.remove`, `runtime.status`, plus `runtime_util.go`.
- Agent nginx vhost `proxy_pass` branch in `domain_create.go`.
- UI: `DomainRuntimeSettingsModal.tsx`, runtime column + picker in domain lists
  and `DomainEdit`.

Build status at review time: both Go modules compiled, vet clean, tests green.
The problems were security/correctness, not compilation.

---

## 2. Findings (as reported during review)

### CRITICAL
- **C1 — Docker command injection via `entry_point`.** The image name was
  rendered as a bare token in the systemd `ExecStart` line. A value like
  `alpine --privileged -v /:/host` would inject extra `docker run` flags →
  host mount / privileged container → escape. `entry_point` was fully
  user-controlled and unvalidated.
- **C2 — Docker runtime = root-equivalent, exposed to every tenant.** Running
  `docker` as a user invocation requires the `docker` group (~root) or rootless
  (not implemented). The API accepted `runtime_type: docker` from any user.

### HIGH
- **H1 — Env var injection into systemd unit.** `Environment="{{k}}={{v}}"`
  rendered via `text/template` (no unit-syntax escaping). A value with a newline
  or quote could inject new unit directives. Key validation was client-side only.
- **H2 — Linear port scan (tens of thousands of DB queries per request).**
  `getRuntimeService` scanned 10000→60000 with one `COUNT` per iteration; no
  reservation (race), and fell back to port 10000 when full.
- **H3 — Synchronous 5-minute deploy blocked the reconciler loop.**
  `runtime.deploy` (npm/pip/go/docker build) ran inline inside `ReconcileAll`,
  blocking SSL renewals, DNS, and all other domains.

### MEDIUM
- **M1 — API↔agent param mismatch.** API called `runtime.status`/`runtime.logs`
  with `systemd_unit`, but the agent handlers require `{username, domain}` →
  modal status/logs always empty.
- **M2 — Inconsistent status enum** across migration comment, reconciler
  (`running`/`failed`/`deploying_done`), and UI.
- **M3 — Source-leak window.** A proxy runtime with no allocated port fell back
  to `try_files`, serving raw source (server.js, .env) as static files.
- **M4 — PortAllocator rebuilt per call**, so the in-flight reservation set was
  always empty (no real anti-collision).

### LOW
- **L1** entry_point `..` traversal; **L2** React prop mutation in the modal;
  **L3** `agent_status?: any`; **L4** missing `r.agent` nil-guard; **L5**
  `bodyStyle` deprecated; **L6** down migration without guard.

---

## 3. Remediation — first batch (C1–H3, M1–M4, L1–L3)

**C1 — Docker injection fixed.** Added strict validators in
`panel-agent/internal/commands/runtime_util.go` (`validateImageName`,
`validateRuntimeType`, `validateSafeToken`, `validateEntryPoint`,
`validateEnvVars`). Wired into `runtime_apply.go` and `runtime_deploy.go`.
Image refs must match a strict regex (no leading `-`, no spaces/metacharacters).
Tests: `TestValidateImageName`, `TestRuntimeApply_RejectsDockerInjection`.

**C2 — Docker admin-gated.** `panel-api/internal/api/domains.go` PATCH now
returns 403 `docker_runtime_admin_only` for non-admins. Tests:
`domain_runtime_gate_test.go`.

**H1 — Env injection fixed (two layers).** (1) Server-side validation in
`updateRuntimeService` + agent `validateEnvVars` (reject non-POSIX keys and
values containing newline/quote/backslash/NUL). (2) Structural: user env vars
now written to a separate `EnvironmentFile=` (mode 0600, chown user) via
`writeUserFile`, instead of inline `Environment=`. Change-detection compares the
env file body separately so an env-only edit still triggers a rewrite + restart.
Tests: `TestValidateEnvVars`, `TestRuntimeApply_RejectsEnvInjection`.

**H2 — Port scan replaced.** `getRuntimeService` now uses
`services.PortAllocator.Allocate()` (random probe + reservation); returns 503
`no_free_port` when exhausted.

**H3 — Deploy made async.** `reconcileRuntimeService` creates the port row
synchronously, then runs deploy+apply in a background goroutine guarded by
`runtimeInflight` (sync.Map) so a slow tenant never blocks the tick and no
double-deploy occurs. The goroutine re-fetches the row before the final update
to avoid clobbering concurrent edits.

**M1 — Param mismatch fixed.** API now calls `runtime.status`/`runtime.logs`
with `{username, domain}` (username resolved from `domain.UserID`).

**M2 — Status enum unified.** Added `models.RuntimeStatus*` constants
(`pending/deploying/running/stopped/failed`); removed `deploying_done`; migration
000149 comment synced; UI renderer aligned.

**M3 — Source-leak closed.** Agent vhost: a proxy runtime with no port now
renders `return 503` instead of `try_files`. New `ProxyPending` field.

**M4 — Shared allocator.** A single `PortAllocator` instance is stored on the
`Reconciler` (created in `WithRuntimeServices`) so reservations work.

**L1** `validateEntryPoint` rejects `..` and absolute paths.
**L2** removed `domain.runtime_type = value` prop mutation in the modal.
**L3** `agent_status?: any` → `Record<string, unknown>`.

---

## 4. Dependency audit (`panel-ui/package-lock.json`, 1186 lines changed)

- `package.json` itself **unchanged** — lock was re-resolved only.
- **All `resolved` URLs come from `registry.npmjs.org`** — no git/http/file/ssh
  sources, no third-party registries.
- "New" packages are all legitimate: `@esbuild/*` and `@rollup/*` platform
  binaries, `fsevents`, and `@rc-component/*` (antd v6 internals). The
  `@rc-component/*` entries appear in both added/removed because they were
  relocated/de-duped in the tree, not newly introduced.
- `npm ls` reports no invalid/missing/unmet deps; lockfileVersion 3.
- **`npm audit`:** 2 moderate XSS advisories in `dompurify` (pulled via
  `monaco-editor`, `dompurify@3.2.7` / `monaco@0.55.1`). **Pre-existing** — the
  diff does not touch those lines. Reported but intentionally NOT changed
  (out of scope for this changeset). Candidate for a separate task.

---

## 5. Remediation — second batch (N1–N9)

**N1 — Docroot validation in runtime handlers.** `runtime_deploy.go` and
`runtime_apply.go` now call `validateDocrootPath(username, docroot)` (the same
helper used by 17 CMS install handlers), enforcing `/home/<user>/domains/`.
Test: `TestRuntimeDeploy_RejectsDocrootTraversal`. Three existing tests updated
to use compliant paths.

**N2 — DELETE domain no longer orphans the runtime.** `ReconcileDeleted` signature
is now `(ctx, domainName, ownerUsername, runtimeType)`; it calls `runtime.remove`
(stop+disable unit, remove EnvironmentFile, `docker rm -f`) for proxy runtimes
before tearing down the vhost. Both call sites (`domains.go` delete,
`users.go` cascade delete) capture username + runtime_type before the DB row
(and its cascade-deleted `runtime_services` row) is gone.

**N3 — Port allocator comment + real host probe.** Migration 000149 comment
corrected (no longer claims `/proc/net/tcp` probing). Added `portInUseOnHost`
in the agent (loopback dial); called in `runtime.apply` only on a fresh install
of an enabled, non-docker service → returns `CodeFailedPrecondition` on conflict
instead of a crash loop. Test: `TestPortInUseOnHost`.

**N4 — UI Docker gate.** `DomainRuntimeSettingsModal.tsx` reads
`useAuth().isAdmin` and hides the Docker option for non-admins (unless the
domain already is docker), matching the server gate.

**N5 — runtime_type at domain creation.** `createDomainRequest` accepts
`runtime_type`, validated against the closed set, docker admin-gated, default
`php`. Tests: `TestCreateRuntimeType_InvalidRejected`,
`TestCreateRuntimeType_NonAdminDockerForbidden`.

**N6 — App.Runtime/DefaultPort no longer dead.** In `applications_service.go`,
a descriptor with a non-PHP `Runtime` now persists it onto `domain.runtime_type`
at install (best-effort, logged on failure). No-op for all current PHP
descriptors (`Runtime==""`).

**N7 — Guarded down migration 000148.** `DROP COLUMN` wrapped in an
`information_schema` existence check + `PREPARE/EXECUTE` (idempotent). Matches
the existing pattern in migration 000136; `multiStatements=true` is already
forced in `db/raw.go`.

**N8 — Reconciler self-heal.** Before a heavy redeploy of a `failed && enabled`
service, the reconciler probes the unit via `runtimeStatusActive`; if systemd
(`Restart=always`) already recovered it, the DB flips back to `running` without
redeploying.

**N9 — antd v6 cleanup.** `bodyStyle` → `styles.body` on the Modal and Card in
the runtime modal.

---

## 6. Files changed by the remediation

**panel-agent**
- `internal/commands/runtime_util.go` — validators, `portInUseOnHost`, `writeUserFile`.
- `internal/commands/runtime_apply.go` — validation wiring, EnvironmentFile, host port check.
- `internal/commands/runtime_deploy.go` — validation wiring (runtime/entry/docroot/image).
- `internal/commands/runtime_remove.go` — env file cleanup.
- `internal/commands/runtime_test.go` — new validation + port tests; existing tests updated to compliant docroots.

**panel-api**
- `internal/models/runtime_service.go` — `RuntimeStatus*` constants.
- `internal/api/runtime_service.go` — PortAllocator, `{username,domain}` agent calls, server-side env validation, status constants.
- `internal/api/domains.go` — runtime_type on create + PATCH, docker admin gate, capture username/runtime on delete.
- `internal/api/users.go` — pass username/runtime to `ReconcileDeleted` on cascade delete.
- `internal/api/applications_service.go` — persist descriptor.Runtime onto domain at install (N6).
- `internal/api/domain_runtime_gate_test.go` — new (create/PATCH gate tests).
- `internal/reconciler/reconciler.go` — shared allocator, async converge, self-heal, `ReconcileDeleted` runtime teardown.
- `internal/reconciler/runtime_reconcile_test.go` — updated to poll async convergence.
- `internal/db/migrations/000148_*.down.sql` — guarded drop.
- `internal/db/migrations/000149_*.up.sql` — status comment.

**panel-ui**
- `src/shells/DomainRuntimeSettingsModal.tsx` — admin Docker gate, no prop mutation, typed agent_status, `styles.body`.

---

## 7. Tests added

- Agent: `TestValidateImageName`, `TestValidateEnvVars`, `TestValidateEntryPoint`,
  `TestValidateSafeToken`, `TestPortInUseOnHost`,
  `TestRuntimeApply_RejectsDockerInjection`, `TestRuntimeApply_RejectsEnvInjection`,
  `TestRuntimeDeploy_RejectsDocrootTraversal`.
- API: `TestUpdateRuntimeType_*` (4), `TestCreateRuntimeType_*` (2).
- Reconciler: existing `TestReconcile_RuntimeService_AutoProvisionAndDeploy`
  reworked to poll the async convergence.

## 8. Verification (final)

- `panel-api`: build OK, vet clean, tests green (api/reconciler/models/services).
- `panel-agent`: build OK, vet clean, tests green (commands).
- `panel-ui`: `tsc -b` OK.

## 9. Notes / decisions

- **Biome:** reports import-ordering/tab complaints on the UI files. The repo
  uses 2-space + ESLint and ships **no Biome config**; auto-fixing would reformat
  whole files and break the merge-clean convention. Project gate `tsc -b` passes,
  so Biome auto-fix was deliberately not applied.
- **dompurify/monaco XSS:** pre-existing, not introduced by this changeset;
  reported, left untouched (out of scope).
- **No commits or pushes** were made — all changes are in the working tree on
  `main`, per repo policy (agents never commit to main / never push).

## 10. Open items (not done — by decision, not oversight)

- L4: `reconcileRuntimeService` does not nil-guard `r.agent` (safe in production
  wiring; not defensive).
- Separate task candidate: upgrade `dompurify`/`monaco-editor` to clear the two
  moderate `npm audit` advisories.
