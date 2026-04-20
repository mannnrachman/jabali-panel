# M16 Rollback + M22 Magic-Link Admin Login — Plan

**Date**: 2026-04-21
**Author**: shuki + Claude
**Status**: dispatchable
**Related ADRs**: 0036 (M16 — to be marked SUPERSEDED), 0038 (M16 rollback rationale, new), 0039 (M22 magic-link design, new)

---

## Why both in one plan

The operator validated M16 end-to-end on a real VM and rejected the result: the auto-installed OIDC plugin (daggerhart-openid-connect-generic v3.11.3) doesn't send PKCE, our Hydra config enforces it, so the visible UX is a **broken** "Login with OpenID Connect" button on every WP install.

Diagnosis pointed at three shippable fixes (relax PKCE, swap plugin, or swap protocol). Operator chose option 4: **drop OIDC entirely for this use case** because the actual goal — "click a button in the panel, land in wp-admin signed in" — doesn't need a federation protocol. cPanel/Plesk solve the same problem with a magic-link: panel mints a signed short-lived token, custom WP plugin trades it for a `wp_set_auth_cookie()` call. No OAuth, no consent screens, no PKCE, no token-endpoint round-trip. M22 is that.

M16 must come out completely (Hydra service, all panel-side handlers, all UI, all DB columns) before M22 lands. Otherwise we ship two parallel SSO mechanisms and confuse every future contributor.

## Goals

1. **Full M16 rollback** — uninstall jabali-hydra service, drop migration 000050, delete every line of OIDC code on panel-api / panel-agent / panel-ui, archive the runbook + plan, supersede ADR-0036, mark BLUEPRINT M16 ROLLED BACK
2. **M22 magic-link admin login** — panel-side token mint + signature scheme, custom WP must-use plugin, panel UI button on Applications row, `Log in to admin` works in one click

## Non-goals

- **Wave F (Automation API / machine tokens)** — Hydra's other planned use case is also dropped here. If/when machine-tokens become urgent, a fresh ADR will pick the right tool (could be Hydra again, could be something simpler like signed JWT-only tokens).
- **Magic-link for Drupal / Joomla / phpBB / etc.** — M22 ships only the WordPress plugin. Other apps remain admin-via-credentials until a per-app must-use-plugin equivalent is added (one PR per app, follows the same template).
- **Cross-host magic-links** — token validation is a callback from the WP plugin to the panel over the local network. Does not work for offsite WP installs, and never will under this design.

## Branching + commit conventions

Per `CLAUDE.md`: agents commit to feature branches; the dispatcher merges to `main` and pushes. Use the per-step branch slug noted under each step (e.g. `m16-rollback-1-apps-mint`, `m22-magic-link-9-token-model`). Rebase onto latest `origin/main` before final report; re-run tests post-rebase.

`gh` is unavailable on this Gitea remote — there's no PR command. After commit, the report is just the branch name + commit SHAs + `git log main..<branch>` summary.

## Steps overview

```
M16 rollback (Steps 1–7)              M22 magic-link (Steps 8–11)
                                                  │
1 ─┐                                       8 (ADR + design)
2 ─┤        (parallel)                            │
3 ─┘                                       9 (token + migration + signer)
   │                                              │
   ├──> 4 (panel-side OIDC code)         10 (API + plugin + agent step)
   │                                              │
   ├──> 5 (proxy + config block)          11 (UI button + tests + runbook)
   │                                              │
   ├──> 6 (install.sh + service unit)            done
   │
   └──> 7 (docs + ADR-0038 + runbook)
              │
              all merged → operator runs VM teardown checklist (Step 7's runbook)
              │
              then Steps 8–11 dispatch
```

| # | Title                                                | Branch slug                          | Depends on    | Parallel with  | Model    |
|---|------------------------------------------------------|--------------------------------------|---------------|----------------|----------|
| 1 | Revert apps-framework + CLI OIDC client minting      | `m16-rollback-1-apps-mint`           | —             | 2              | default  |
| 2 | Revert WP OIDC plugin auto-install (agent)           | `m16-rollback-2-agent-plugin`        | —             | 1              | default  |
| 3 | Migration 000051 drops OIDC columns + model fields   | `m16-rollback-3-migration`           | 1             | —              | default  |
| 4 | Delete hydraclient + oauth2_flow + Consent SPA       | `m16-rollback-4-panel-oidc-code`     | 1             | 5, 6           | default  |
| 5 | Drop /oauth2/* proxy + HydraConfig + browser-auth    | `m16-rollback-5-proxy-config`        | 4             | 6              | default  |
| 6 | Remove install_hydra + templates + service unit      | `m16-rollback-6-install-sh`          | —             | 1, 2, 4, 5     | default  |
| 7 | ADR-0038 + plan archive + BLUEPRINT + VM-teardown    | `m16-rollback-7-docs`                | 1, 2, 4, 5, 6 | —              | strongest|
| 8 | ADR-0039 magic-link design + threat model            | `m22-magic-link-8-adr`               | 1–7 merged    | —              | strongest|
| 9 | Token model + migration 000052 + signer/verifier     | `m22-magic-link-9-token-model`       | 8             | —              | default  |
|10 | Panel API endpoints + WP must-use plugin + agent     | `m22-magic-link-10-api-plugin`       | 9             | —              | default  |
|11 | Panel UI button + tests + runbook                    | `m22-magic-link-11-ui-finalize`      | 10            | —              | default  |

11 steps. Steps 1+2+6 dispatchable in parallel as Wave A. Steps 4+6 (after 1 lands) parallel as Wave B. Step 5 gates on 4. Step 7 gates on everything in M16. Steps 8–11 strictly serial. M22 doesn't dispatch until Step 7's VM-teardown is done by the operator on the test VM.

## Pre-flight (Wave A) — run BEFORE any step dispatches

Two cheap greps that catch the cross-step deletion-ordering hazards the reviewer flagged. Run these once before Step 1 commits; if either returns hits outside the expected files, dispatch is paused and the plan is amended.

```bash
# (1) Confirm RequireKratosSessionOrRedirect has no consumer outside oauth2_flow.go and its tests.
grep -rn 'RequireKratosSessionOrRedirect' panel-api panel-ui  # expect: only oauth2_flow.go + auth_kratos_test.go + auth_kratos.go itself

# (2) Confirm OIDCClientID/Secret/Issuer (the kickContext fields) are only consumed by the WordPress kicker.
grep -rn 'OIDCClientID\|OIDCClientSecret\|OIDCIssuer' panel-api/internal/api panel-api/cmd panel-agent/internal/commands  # expect: only wordpress.go + applications_service.go + wordpress_install.go + their tests

# (3) Confirm wire.go does not exist or doesn't inject the deleted deps (project uses constructor injection in cmd/server, not google/wire).
ls panel-api/wire.go panel-api/internal/wire/ 2>&1 | grep -v 'No such'  # expect: empty (project uses cmd/server explicit DI, not wire codegen)
```

If grep (1) finds an extra consumer, add the matching deletion to Step 4. If grep (2) finds an extra dispatcher, expand Step 1 task list to that file. If grep (3) finds wire.go, expand Step 1 to update it.

## Invariants verified after every step

- `cd panel-api && go build ./...` clean
- `cd panel-agent && go build ./...` clean
- `cd panel-api && go test ./...` green (no skipped tests, no panics)
- `cd panel-agent && go test ./...` green
- `cd panel-ui && npm run build` succeeds
- `cd panel-ui && npx vitest run` green (skip Playwright; that's a separate matrix)
- `gitnexus_detect_changes` shows only the symbols this step is supposed to touch

For Steps 5–6 (config + install.sh removals) additionally:
- Fresh `install.sh` parse — `bash -n install.sh` passes
- No remaining references to `hydra` (case-insensitive grep) outside intentional comments and the archived plan

For Steps 9–11 (magic-link runtime):
- `golangci-lint run ./...` clean (or `go vet ./...` if linter not installed)
- New tests cover: signed-token round-trip, expired token rejection, replay rejection (used_at set), wrong install_id rejection, signature-mismatch rejection
- Plugin install on fresh WP: `wp plugin list --status=mustuse --path=…` shows `jabali-magic-link`

---

## Step 1 — Revert apps-framework + CLI OIDC client minting

**Branch**: `m16-rollback-1-apps-mint`
**Model**: default
**Depends on**: —
**Parallel with**: 2, 6

### Context brief (cold-start)

The apps framework currently mints an OAuth 2 client in Hydra during `POST /api/v1/applications` (HTTP path) and `jabali app install` (CLI path). On install:

1. `panel-api/internal/api/applications_service.go:InstallApplication` — after the install row is inserted, calls `deps.HydraClient.CreateClient`, `SetClientTrusted(true)`, seals the secret with `deps.SSOKey.Seal`, calls `deps.ApplicationInstalls.UpdateOIDCFields` to persist; passes `OIDCClientID/Secret/Issuer` through `kickContext` to the per-app kicker.
2. `panel-api/internal/api/wordpress.go` — `installKickArgs` carries `OIDCClientID/Secret/Issuer`; the WP kicker forwards them in the agent payload as `oidc_client_id/secret/issuer`. The HTTP `ApplicationHandlerConfig` carries `HydraClient`, `SSOKey`, `PanelBaseURL`. `createDeleteAndKickAgent` calls `cfg.HydraClient.DeleteClient(oidcClientID)` best-effort.
3. `panel-api/internal/apps/registry.go` — `App` struct has `OIDCCallbackPath string`.
4. `panel-api/internal/apps/wordpress.go` — sets `OIDCCallbackPath: "/wp-admin/admin-ajax.php?action=openid-connect-authorize"`.
5. `panel-api/cmd/server/cli_app_install.go:buildAppDeps` — wires `HydraClient` (from `cfg.Auth.Hydra.AdminURL`), `SSOKey` (from `cfg.SSO.KeyPath`), `PanelBaseURL` (from `cfg.Server.Hostname`).
6. `panel-api/cmd/server/cli_ops_app.go:deleteAppDirect` — calls `hydraclient.DeleteClient` before DB teardown.
7. `panel-api/internal/repository/application_install_repository.go` — has `UpdateOIDCFields(ctx, id, clientID, secretEnc) error` on the interface.
8. `panel-api/internal/models/application_install.go` — has `OIDCClientID *string` and `OIDCClientSecretEnc []byte` fields with `gorm` tags including a unique index `uniq_app_installs_oidc_client_id`.

This step removes ALL of that. It does NOT touch the DB columns themselves (Step 3 does — the migration). After this step, the model's two fields still exist (so the GORM SELECT shape matches the DB), but no code reads or writes them.

### Tasks

1. `panel-api/internal/api/applications_service.go`:
   - Delete the entire OIDC mint block (`if descriptor.OIDCCallbackPath != "" && deps.HydraClient != nil && deps.SSOKey != nil { ... }`)
   - Remove `OIDCClientID`, `OIDCClientSecret`, `OIDCIssuer` from `kickContext`
   - Remove the three matching fields from the `dispatchInstallKicker` "wordpress" case
   - Drop the `hydraclient` import
2. `panel-api/internal/api/wordpress.go`:
   - Remove `HydraClient`, `SSOKey`, `PanelBaseURL` from `ApplicationHandlerConfig`
   - Remove `OIDCClientID/Secret/Issuer` from `installKickArgs`
   - Remove the `if args.OIDCClientID != "" && args.OIDCClientSecret != ""` block in `createInstallAndKickAgent`
   - Drop the `oidcClientID` parameter from `createDeleteAndKickAgent` (back to the original positional arg list); remove the best-effort `DeleteClient` block
   - Update the caller site that passes `oidcClientID` from the handler
   - Drop the `hydraclient` and `ssokey` imports
3. `panel-api/internal/apps/registry.go`: remove the `OIDCCallbackPath string` field from `App`
4. `panel-api/internal/apps/wordpress.go`: remove the `OIDCCallbackPath: "..."` line from the WordPress descriptor
5. `panel-api/cmd/server/cli_app_install.go`:
   - Remove the `hydra := hydraclient.New(...)`, `ssoK := ssokey.Load(...)`, `panelBaseURL := ...` blocks
   - Remove `HydraClient`, `SSOKey`, `PanelBaseURL` from the returned `ApplicationHandlerConfig`
   - Drop the `hydraclient` and `ssokey` imports
6. `panel-api/cmd/server/cli_ops_app.go`:
   - Remove the `if install.OIDCClientID != nil && ...` block that calls `hydra.DeleteClient`
   - Drop the `hydraclient` import
7. `panel-api/internal/repository/application_install_repository.go`:
   - Remove `UpdateOIDCFields(...)` from the `ApplicationInstallRepository` interface
   - Remove the `func (r *applicationInstallRepo) UpdateOIDCFields(...)` implementation
8. `panel-api/internal/repository/application_install_repository_test.go`:
   - Delete `TestApplicationInstallUpdateOIDCFields_Success` and `TestApplicationInstallUpdateOIDCFields_NotFound`
   - The 17-column INSERT tests stay as-is for now — Step 3 reverts them when it drops the migration
9. `panel-api/internal/api/wordpress_test.go`:
   - Delete the `UpdateOIDCFields` mock method on `mockWordPressInstallRepo`
10. `panel-api/internal/reconciler/wordpress_reconcile_test.go`:
    - Delete the `UpdateOIDCFields` mock method on `mockWordPressInstallRepo`
11. `panel-agent/internal/commands/wordpress_install.go`:
    - Remove `OIDCClientID`, `OIDCClientSecret`, `OIDCIssuer` fields from `wordpressInstallReq` (the panel no longer sends them; tolerated as ignored if any straggler does)

> **Sequencing note for Wave A**: Steps 1 and 2 are dispatchable in parallel because the panel-agent JSON request struct tolerates extra fields by design (`json.Decoder` defaults to "ignore unknown"). If Step 2 lands first, the panel-api still sends `oidc_client_id` etc. for one deploy cycle and the agent silently drops them — no panic, no schema break. If Step 1 lands first, the agent receives an empty payload for those fields and the now-deleted `installAndConfigureOIDCPlugin` block is gone, so the previous-version agent (still running on hosts) silently no-ops. **However**: if you'd rather serialise to remove the in-flight ambiguity, dispatch Step 1, wait for it to land, then dispatch Step 2 — costs one extra round-trip, removes a (small) deployment-window edge case.

### Verify

```bash
cd panel-api && go build ./... && go test ./...
cd ../panel-agent && go build ./... && go test ./...
grep -rn 'OIDCClientID\|OIDCCallbackPath\|hydraclient\.\|HydraClient\b\|UpdateOIDCFields' panel-api/internal/api panel-api/internal/apps panel-api/internal/repository panel-api/cmd/server | grep -v '_test.go.*FAILS' | grep -v '\.git'
# Expected: zero hits in those four trees
```

### Exit criteria

- All four `grep` targets return zero hits in the named subtrees
- Every test in `panel-api/internal/{api,apps,repository,reconciler}/` and `panel-agent/internal/commands/` passes
- No build warnings about unused imports

### Rollback

`git revert <commit>` — pure code revert, no DB or service state touched.

---

## Step 2 — Revert WP OIDC plugin auto-install (agent)

**Branch**: `m16-rollback-2-agent-plugin`
**Model**: default
**Depends on**: —
**Parallel with**: 1, 6

### Context brief (cold-start)

`panel-agent/internal/commands/wordpress_install.go` has an `installAndConfigureOIDCPlugin(ctx, req, installPath)` function that runs after `wp core install`:

1. `wp plugin install daggerhart-openid-connect-generic --activate`
2. `wp option update openid_connect_generic_settings <json>` via stdin

This is what produces the broken "Login with OpenID Connect" button on every WP install. Remove the function, the call site, and the `buildOIDCPluginSettings` helper.

This step does NOT touch the panel-api side request fields (Step 1 already removed them from the agent payload). If Step 1 hasn't merged yet, the agent will still receive `oidc_client_id` etc. in the body — but `wordpressInstallReq` decodes them into now-unused fields and ignores them; no harm.

### Tasks

1. `panel-agent/internal/commands/wordpress_install.go`:
   - Delete `installAndConfigureOIDCPlugin` function
   - Delete `buildOIDCPluginSettings` helper
   - Delete the `if req.OIDCClientID != "" && ... { installAndConfigureOIDCPlugin(...) }` call site
2. `panel-agent/internal/commands/wordpress_install_test.go`:
   - Delete `TestBuildOIDCPluginSettings_Shape`
   - Delete `TestWordPressInstallHandler_SkipOIDCWhenEmpty`

### Verify

```bash
cd panel-agent && go build ./... && go test ./internal/commands/
grep -rn 'OIDC\|openid-connect\|openid_connect' panel-agent/internal/commands/
# Expected: zero hits
```

### Exit criteria

- `wordpress_install.go` shrinks back to its pre-M16 size (~430 lines vs current ~540)
- All commands tests pass
- No more grep hits for OIDC-related strings in `panel-agent/internal/commands/`

### Rollback

`git revert <commit>` — agent code only, no on-disk WP state touched.

---

## Step 3 — Migration 000051 drops OIDC columns + model fields

**Branch**: `m16-rollback-3-migration`
**Model**: default
**Depends on**: 1
**Parallel with**: —

### Context brief (cold-start)

Migration 000050 added `oidc_client_id CHAR(40)` and `oidc_client_secret_enc VARBINARY(512)` to `application_installs`, with a unique index `uniq_app_installs_oidc_client_id`. The model has matching fields. After Step 1, no code reads or writes them — they're dead weight in the schema and the struct.

This step:
- Adds migration 000051 that drops both columns + the unique index (forward-only — running 000051's `up.sql` is the column drop; the `down.sql` is a no-op or can recreate but with NULL data, since this is a destructive forward step)
- Removes the two fields + the GORM `uniqueIndex` tag from `models.ApplicationInstall`
- Updates the sqlmock test fixtures from 17-column INSERT back to 15-column

### Tasks

1. New `panel-api/internal/db/migrations/000051_application_installs_drop_oidc.up.sql`:
   ```sql
   -- Idempotent: handles environments that never applied 000050 (e.g., DB restored
   -- from a pre-M16 backup before this migration ships). DROP COLUMN IF EXISTS is
   -- supported on MariaDB 10.5+. The unique index is dropped via DROP INDEX IF
   -- EXISTS first; MariaDB drops the index implicitly with the column on most
   -- versions but explicit is safer and reversible.
   ALTER TABLE application_installs DROP INDEX IF EXISTS uniq_app_installs_oidc_client_id;
   ALTER TABLE application_installs DROP COLUMN IF EXISTS oidc_client_id;
   ALTER TABLE application_installs DROP COLUMN IF EXISTS oidc_client_secret_enc;
   ```
2. New `panel-api/internal/db/migrations/000051_application_installs_drop_oidc.down.sql`:
   - Header comment: "Re-adds the columns from 000050 with NULL data — original sealed secrets are gone. Restore from backup if you need them."
   - Recreates both columns + the unique index, NULLABLE (matches 000050's schema).
3. `panel-api/internal/models/application_install.go`:
   - Remove `OIDCClientID *string` and `OIDCClientSecretEnc []byte` fields
   - Remove the entire comment block introducing them
4. `panel-api/internal/repository/application_install_repository_test.go`:
   - In `TestWordPressInstallCreate_Success`: drop the two `install.OIDCClientID, install.OIDCClientSecretEnc` args from the INSERT `WithArgs` (back to 15 args)
   - In `TestWordPressInstallCreate_UniqueDomainIDConstraint`: drop two `sqlmock.AnyArg()` entries (back to 15)

### Verify

```bash
cd panel-api && go build ./... && go test ./internal/repository/ ./internal/models/
ls panel-api/internal/db/migrations/000051_*
grep -rn 'OIDCClientID\|oidc_client_id\|oidc_client_secret_enc' panel-api/internal/
# Expected: only hits inside the migration files

# Reversibility test on a throwaway schema (catches typos in down migration):
docker run --rm -d --name mig-test-mariadb -e MARIADB_ROOT_PASSWORD=test -p 13306:3306 mariadb:11.8
sleep 5
docker exec mig-test-mariadb mariadb -uroot -ptest -e "CREATE DATABASE jabali_panel"
JABALI_DSN="root:test@tcp(localhost:13306)/jabali_panel" jabali migrate up   # apply 000050 then 000051
JABALI_DSN="root:test@tcp(localhost:13306)/jabali_panel" jabali migrate down 1  # roll back 000051
docker exec mig-test-mariadb mariadb -uroot -ptest jabali_panel -e "DESCRIBE application_installs" | grep oidc
# Expected: down migration recreates the columns; output shows oidc_client_id + oidc_client_secret_enc
JABALI_DSN="root:test@tcp(localhost:13306)/jabali_panel" jabali migrate up  # re-apply forward
docker rm -f mig-test-mariadb
```

### Exit criteria

- Tests green (sqlmock now expects 15 columns)
- `grep` hits limited to the migration files themselves
- Migration 000051 is reversible-ish (down recreates columns but data is gone — comment makes that explicit)

### Operator action after merge + deploy

```bash
ssh root@<panel-host> 'jabali migrate up'
# verifies new column count: should report 0 oidc_* columns on application_installs
mariadb -D jabali_panel -e "DESCRIBE application_installs" | grep -i oidc
# expected: empty output
```

### Rollback

Risky — the original sealed secrets are unrecoverable. If a rollback is genuinely needed, restore from the pre-migration mysqldump AND `git revert` the code commit. The down migration recreates the column shape but not the data.

---

## Step 4 — Delete hydraclient + oauth2_flow + Consent SPA + browser-auth middleware

**Branch**: `m16-rollback-4-panel-oidc-code`
**Model**: default
**Depends on**: 1
**Parallel with**: 5, 6

### Context brief (cold-start)

After Step 1, no caller in the apps framework references `hydraclient` or the `RegisterOAuth2FlowRoutes` handler. This step removes the entire OIDC handler surface area:

- `panel-api/internal/hydraclient/{client,consent,tokens,errors,scope_labels}.go` + tests
- `panel-api/internal/api/oauth2_flow.go` + tests
- `panel-api/internal/middleware/auth_kratos.go`'s `RequireKratosSessionOrRedirect` (was added in Wave E specifically for the OIDC browser flow; no other consumer)
- `panel-api/internal/middleware/auth_kratos_test.go`'s three `OrRedirect` tests
- `panel-ui/src/pages/Consent.tsx` + `Consent.test.tsx`
- `panel-ui/src/App.tsx`'s `/consent` route + the `Authenticated` import if it becomes unused
- `panel-ui/tests/e2e/oauth2-wordpress.spec.ts`

`RequireKratosSession` (the strict 401 variant) stays — every `/api/v1/*` handler depends on it.
`ssokey` package stays — phpMyAdmin SSO uses it.

### Tasks

1. Delete `panel-api/internal/hydraclient/` (the whole directory)
2. Delete `panel-api/internal/api/oauth2_flow.go` and any `oauth2_flow_test.go` if present
3. `panel-api/internal/middleware/auth_kratos.go`: delete `RequireKratosSessionOrRedirect` and the `redirectToLogin` helper; drop the `net/url` import if it's now unused
4. `panel-api/internal/middleware/auth_kratos_test.go`: delete the three `TestRequireKratosSessionOrRedirect_*` tests + the `redirectProbe` helper
5. Delete `panel-ui/src/pages/Consent.tsx` and `panel-ui/src/pages/Consent.test.tsx`
6. `panel-ui/src/App.tsx`: remove the `<Route path="/consent">` block and the `import { ConsentPage } from "./pages/Consent"` line
7. Delete `panel-ui/tests/e2e/oauth2-wordpress.spec.ts`
8. **Full `hydraclient` cleanup in `panel-api/internal/app/app.go`** (must land in this step — Step 4 deletes the `hydraclient/` package, so every file that imports it must drop the import in the SAME commit, otherwise the build is red):
   - Delete the `if cfg.Auth.Hydra.AdminURL != "" && deps.HydraClient == nil { deps.HydraClient = hydraclient.New(...) }` construction block
   - Delete the `if deps.HydraClient != nil { api.RegisterOAuth2FlowRoutes(...) }` registration block (the `OAuth2FlowHandlerConfig{...}` literal goes with it, including the `BrowserAuth: middleware.RequireKratosSessionOrRedirect(...)` field)
   - Delete `HydraClient *hydraclient.Client` from `app.Deps`
   - Delete the `panelBaseURLFromConfig` helper if it's only used by the construction block (grep confirms; if a non-OIDC caller exists, leave it)
   - Drop the `git.linux-hosting.co.il/.../hydraclient` import line
   - **Do NOT touch** `RegisterHydraProxy`, `hydra_proxy.go`, `HydraConfig`, env vars — those are Step 5
   - Confirm via `cd panel-api && go build ./...` after the deletion — the compiler is the safety net for missed call sites

### Verify

```bash
cd panel-api && go build ./... && go test ./...
cd ../panel-ui && npm run build && npx vitest run
ls panel-api/internal/hydraclient/ 2>&1 | grep -v 'No such'
# Expected: "No such file or directory"
grep -rn 'hydraclient\|oauth2_flow\|ConsentPage\|RequireKratosSessionOrRedirect' panel-api panel-ui/src
# Expected: zero hits
```

### Exit criteria

- `hydraclient/` directory does not exist
- `oauth2_flow.go` does not exist
- `Consent.tsx` does not exist
- All Go + frontend tests green
- Bundle size delta: SPA build smaller by the removed files

### Rollback

`git revert <commit>` — pure code, no service or DB touched.

---

## Step 5 — Drop /oauth2/* proxy + HydraConfig + RegisterOAuth2FlowRoutes wiring

**Branch**: `m16-rollback-5-proxy-config`
**Model**: default
**Depends on**: 4
**Parallel with**: 6

### Context brief (cold-start)

After Step 4, every `hydraclient` import has been dropped (Step 4 task 8 absorbed the construction + handler registration + `app.Deps.HydraClient` field). What remains for Step 5 is the runtime proxy mount and the config schema — none of which import the deleted package, so they could not have lived in Step 4 without artificial coupling:
- `RegisterHydraProxy(r)` mount for `/oauth2/*` and the file `hydra_proxy.go` (in package `app`, uses only `httputil.ReverseProxy`)
- `HydraConfig` type + `Hydra HydraConfig` field on `AuthConfig` + env-var parsing in `panel-api/internal/config/config.go`
- `[auth.hydra]` block in `config.example.toml`

Plus `panel-api/internal/config/config.go` has the `HydraConfig` type and the `[auth.hydra]` env parsing. Plus `panel-api/internal/app/hydra_proxy.go` exists.

This step:
- Deletes `panel-api/internal/app/hydra_proxy.go`
- Removes the `RegisterHydraProxy` call from `app.go`
- Removes the `if cfg.Auth.Hydra.AdminURL != "" && deps.HydraClient == nil { deps.HydraClient = hydraclient.New(...) }` block
- Removes the `if deps.HydraClient != nil { api.RegisterOAuth2FlowRoutes(...) }` block
- Removes `HydraClient`, `SSOKey` from `app.Deps` only if no other path uses them (SSOKey IS used by phpMyAdmin SSO — keep it; HydraClient is M16-only — drop it)
- Removes the `panelBaseURLFromConfig` helper and the import of `hydraclient` from app.go
- `panel-api/internal/config/config.go`: removes `HydraConfig` type, the `Hydra HydraConfig` field on `AuthConfig`, the env-var parsing for `JABALI_HYDRA_*`

### Tasks

1. Delete `panel-api/internal/app/hydra_proxy.go`
2. `panel-api/internal/app/app.go`:
   - Remove the `RegisterHydraProxy(r)` call (the only remaining Hydra-shaped wiring after Step 4)
   - All `hydraclient`-touching code in app.go was already removed by Step 4 task 8 — this step is config + proxy only
3. `panel-api/internal/config/config.go`:
   - Remove `Hydra HydraConfig` field from `AuthConfig`
   - Remove `type HydraConfig struct { ... }` block
   - Remove env-var parsing for `JABALI_HYDRA_PUBLIC_URL` and `JABALI_HYDRA_ADMIN_URL`
4. `config.example.toml`: remove the `[auth.hydra]` block entirely
5. `panel-api/cmd/server/serve.go` or wherever `loadHydraClient` lives (if anywhere): remove

### Verify

```bash
cd panel-api && go build ./... && go test ./...
grep -rn 'HydraConfig\|RegisterHydraProxy\|JABALI_HYDRA' panel-api config.example.toml
# Expected: zero hits
```

### Exit criteria

- Panel-api builds clean
- All tests green
- No grep hits for HydraConfig anywhere in the tree

### Rollback

`git revert <commit>`. No runtime state touched (app.go just stops mounting the proxy on the next restart).

---

## Step 6 — Remove install_hydra + hydra templates + service unit + .sha256

**Branch**: `m16-rollback-6-install-sh`
**Model**: default
**Depends on**: —
**Parallel with**: 1, 2, 4, 5

### Context brief (cold-start)

`install.sh` has an `install_hydra()` function (~190 lines, 2510-2700) called from `main()` between `install_kratos` and `install_php_pool_template`. It downloads + verifies + extracts the Hydra binary, provisions SQLite state dir, renders `/etc/jabali-panel/hydra.yml` from `install/hydra.yml.tmpl`, runs `hydra migrate sql`, installs + enables `jabali-hydra.service`, and waits for `/health/ready`.

The template, sha256 file, and unit file:
- `install/hydra.yml.tmpl`
- `install/hydra.sha256`
- `install/systemd/jabali-hydra.service`

Strip all of it. After this step, a fresh install bootstraps without ever touching Hydra.

### Tasks

1. `install.sh`:
   - Delete the entire `install_hydra()` function definition
   - Remove the `install_hydra` call from `main()`
   - If any Hydra-specific helpers (`_hydra_ensure_secret`, etc.) live outside the function, delete them too
2. Delete `install/hydra.yml.tmpl`
3. Delete `install/hydra.sha256`
4. Delete `install/systemd/jabali-hydra.service`

### Verify

```bash
bash -n install.sh
grep -inE 'hydra|oauth2|oidc' install.sh install/ -r
# Expected: zero hits (or only matches in unrelated comments — review by hand)
```

### Exit criteria

- `install.sh` parses
- No hydra references anywhere under `install/`
- No service unit file remains

### Rollback

`git revert <commit>`. The next `install.sh` run on a host won't reinstall Hydra; operator runs the VM teardown checklist (Step 7) to remove the running service.

---

## Step 7 — ADR-0038 + plan archive + BLUEPRINT update + VM-teardown runbook

**Branch**: `m16-rollback-7-docs`
**Model**: strongest (Opus or equivalent — tone + structure matter)
**Depends on**: 1, 2, 4, 5, 6
**Parallel with**: —

### Context brief (cold-start)

ADR-0036 currently says `Status: accepted (Waves A–E shipped 2026-04-20)`. This step flips it to `superseded by ADR-0038` and writes ADR-0038 explaining the rollback rationale. Also archives the M16 plan + runbook (move to `plans/archive/`) so future searches don't match them as live guidance, and updates BLUEPRINT.md's M16 section to ROLLED BACK with a one-line pointer to ADR-0038 + M22.

Also writes a VM-teardown runbook at `plans/m16-rollback-vm-teardown.md` for the operator: stop service, drop DB, remove files, deactivate plugin on existing WP installs.

### Tasks

1. New `docs/adr/0038-m16-rollback.md`:
   - Status: accepted, 2026-04-21
   - Context: M16 was validated end-to-end on a real VM and the user-visible result was a broken "Login with OpenID Connect" button (PKCE-incompatible plugin v3.11.3 vs `pkce.enforced: true` Hydra config)
   - Decision: drop OIDC entirely for the panel-managed-WP use case; replace with magic-link in M22; revisit Hydra as a separate ADR if/when machine-token Automation API becomes urgent (Wave F was always optional)
   - Consequences: lose the standards-compliant OIDC IdP capability; one fewer service to operate; M22 picks up the only remaining consumer
   - Mark ADR-0036 superseded; cross-link
2. `docs/adr/0036-m16-hydra-identity.md`: change `Status:` line to `superseded by ADR-0038 (2026-04-21)`
3. Move `plans/m16-hydra-oauth.md` and `plans/m16-hydra-runbook.md` to `plans/archive/`
4. `docs/BLUEPRINT.md`:
   - Change M16 heading to `M16: Identity Federation + Automation API (ROLLED BACK 2026-04-21 — see ADR-0038)`
   - Add a 3-line summary explaining what shipped, why it was rolled back, and pointer to M22
5. New `plans/m16-rollback-vm-teardown.md` (operator runbook):
   - `systemctl stop jabali-hydra && systemctl disable jabali-hydra`
   - `rm /etc/systemd/system/jabali-hydra.service && systemctl daemon-reload`
   - `rm -f /usr/local/bin/hydra`
   - `rm -rf /var/lib/jabali-hydra`
   - `rm -f /etc/jabali-panel/hydra.yml`
   - `rm -rf /etc/jabali-panel/hydra-secrets`
   - For every WP install: `wp plugin deactivate daggerhart-openid-connect-generic --path=… --uninstall`
   - `jabali migrate up` to apply 000051 (drops the columns)
   - Verify: no `jabali-hydra` unit, no listening sockets on 127.0.0.1:4444 / :4445, no `hydra_*` tables in `mariadb`, no `oidc_client_id` column in `application_installs`

### Verify

```bash
test -f docs/adr/0038-m16-rollback.md
test -f plans/archive/m16-hydra-oauth.md
test -f plans/archive/m16-hydra-runbook.md
test -f plans/m16-rollback-vm-teardown.md
grep -A3 '^### M16' docs/BLUEPRINT.md | head -5  # should show ROLLED BACK
grep '^## Status' docs/adr/0036-m16-hydra-identity.md  # should show superseded
```

### Exit criteria

- All four files present at the listed paths
- BLUEPRINT M16 section starts with ROLLED BACK
- ADR-0036 status line updated
- Runbook is executable verbatim by the operator on the test VM (every command quoted, no placeholders without explanation)

### Rollback

`git revert <commit>`. ADRs and BLUEPRINT history are append-only-by-convention but a revert is acceptable mid-discussion.

---

## --- M16 ROLLBACK COMPLETE — STEPS 8–11 DISPATCH AFTER OPERATOR HAS RUN STEP 7'S VM-TEARDOWN CHECKLIST ON THE TEST VM ---

## Step 8 — ADR-0039 magic-link design + threat model

**Branch**: `m22-magic-link-8-adr`
**Model**: strongest
**Depends on**: 1–7 merged + VM teardown verified
**Parallel with**: —

### Context brief (cold-start)

The replacement for M16's OIDC-for-WP path. Operator wants: panel UI button on Applications row → click → new tab opens → lands in `/wp-admin` signed in as the install's admin user. No OAuth, no consent, no PKCE.

Design contract (preview — ADR formalises):
- Token shape: opaque base64url string, not a JWT (smaller, no signature-algorithm-confusion risk)
- Anatomy: `<token_id (16B random) >.<signature (32B HMAC-SHA256)>`
- Server-side row: `magic_link_tokens(id, application_install_id, panel_user_id, token_hash, expires_at, used_at, created_at)` — the panel stores the SHA-256 of the token_id (NOT the signature) so a DB read leak doesn't yield valid tokens
- Signing key: per-deployment 32-byte random in `/etc/jabali-panel/magic-link.key` (analog to `sso.key` for phpMyAdmin); rotation = generate new key, append to a `keys: []` slice in config, accept either for verification, sign with the newest, retire the old after 30 days
- TTL: 60 seconds from mint
- Single-use: `used_at` is set on first successful validate; subsequent validates with the same token return 410 Gone
- Validation contract: `POST /api/v1/applications/:install_id/magic-link/validate { "token": "..." }` returns `{ "admin_user": "<wp username>", "expires_in": <seconds remaining of original 60s> }` on success; the WP plugin uses the username to call `wp_set_auth_cookie`
- WP plugin endpoint: a `?jabali_admin_login=<token>` query param on any URL of the WP install; the must-use plugin's `init` hook detects + validates + cookies + redirects

Threat model (must be covered in the ADR, expanded by reviewer):
- **Token leak in URL** — the URL is single-use, 60s TTL, scoped to one install; an attacker who intercepts the URL gets exactly one log-in attempt within 60s. Mitigations: TLS everywhere (already enforced), `Referrer-Policy: no-referrer` set by the must-use plugin on its response.
- **Replay** — `used_at` blocks the second use; race window between two simultaneous WP nodes is acceptable (both get logged in once each, but operator-only path).
- **Token forgery** — HMAC over `(token_id || install_id || expires_at)` using a 32B server-side key; signature length-extension attack on HMAC is impossible by construction.
- **Signature key leak** — rotation procedure documented. Existing tokens issued under the old key keep working until expiry (60s). Mechanism: `magic-link.key` is a comma-separated list of base64'd 32B keys, newest first. `Sign` always uses the first key; `Verify` accepts any. Operator rotates by prepending a new key, restarting panel-api (graceful — picks up new key on next request), then ~5 minutes later (well past 60s TTL) editing the file again to drop the old key from the tail. ADR documents this as the only supported rotation path; emergency mid-flight rotations (key compromise during active use) accept the cost of revoking outstanding tokens by removing the leaked key immediately.
- **Cross-install replay** — token row binds to a specific `application_install_id`; the WP plugin's validate POST includes its own install id (read from the must-use plugin's PHP constant set at install time), and the panel rejects on mismatch.
- **CSRF on validate endpoint** — endpoint is unauthenticated (the WP server is the caller, no panel session cookie). Auth is the token itself. Endpoint is rate-limited per IP.
- **Phishing the operator** — the URL pattern `https://<domain>/?jabali_admin_login=…` is recognisable. UI button must show what domain it'll open before the click.
- **Token leak via server logs / APM** — gin's default logger writes the full request URL on every request, which would land tokens in `/var/log/jabali-panel.log`. The mu-plugin's POST body is logged by Apache/nginx access log too. **Mitigations** (mandatory, captured here so Steps 10–11 implement them):
  - Panel logger: register a `LogFormatter` that scrubs `?jabali_admin_login=` and the `Authorization`-shaped POST body from the access log line; replace with `<redacted>`.
  - Mu-plugin: do NOT log the token to PHP error_log on validation failure — log the response code and a short reason string only.
  - WP installs running an APM (NewRelic, Datadog) — operator runbook (Step 11) calls out adding `jabali_admin_login` to the APM's URL parameter denylist.
- **Collision with a legitimate query string** — `?jabali_admin_login=` is a namespaced enough key that collision risk is negligible, but the mu-plugin still **must** treat a malformed token as a no-op (just `unset($_GET['jabali_admin_login'])` and let WP serve the page normally) rather than calling `wp_die`. Otherwise an attacker could break a public page by appending `?jabali_admin_login=junk` to its URL.

### Tasks

1. New `docs/adr/0039-m22-magic-link.md`:
   - Status: proposed (blueprint phase)
   - Context: rollback of M16, operator's actual use case
   - Decision: token format, signing scheme, storage model, validation contract, plugin contract, key rotation
   - 7-section threat model (above) with mitigations per threat
   - Comparison vs alternatives (OIDC, signed JWT cookies, SSH-key derived tokens) — one paragraph each on why magic-link wins for THIS use case
2. `docs/BLUEPRINT.md`: add M22 section under planned milestones (status: in-flight, dispatchable when ADR accepted)
3. Memory entry pointer (in `MEMORY.md`): `- [M22 magic-link plan](project_plan_m22_magic_link.md) — 4 steps, OIDC replacement, signed token + must-use WP plugin`

### Verify

- ADR reviewed by an Opus-tier sub-agent against the strongest-model checklist (per Blueprint phase 4)
- Reviewer adversarially probes the threat model — every finding either fixed or explicitly accepted with rationale

### Exit criteria

- ADR-0039 accepted (Status flipped after review)
- BLUEPRINT M22 entry present
- Memory pointer registered

### Rollback

`git revert <commit>`. No code changes in this step.

---

## Step 9 — Token model + migration 000052 + signer/verifier

**Branch**: `m22-magic-link-9-token-model`
**Model**: default
**Depends on**: 8
**Parallel with**: —

### Context brief (cold-start)

Implementation of the contract from Step 8. Three pieces:

1. **DB schema** — migration 000052 adds `magic_link_tokens` table with the columns from the ADR plus `created_at`. FK on `application_install_id` cascade-deletes (when the install row goes, its outstanding tokens go with it).
2. **GORM model** — `panel-api/internal/models/magic_link_token.go` mirrors the schema. Add a tiny accessor `(t *MagicLinkToken) Used() bool` for readability at call sites.
3. **Signer/verifier** — `panel-api/internal/magiclink/{signer,verifier,key}.go`:
   - `Key` type analog to `ssokey.Key` — 32-byte HMAC key loaded from `/etc/jabali-panel/magic-link.key`
   - `Sign(tokenID, installID string, exp time.Time) (string, error)` → returns `<base64(tokenID)>.<base64(hmac)>`
   - `Verify(tokenString, installID string) (tokenID string, err error)` — re-derives the HMAC, constant-time compares; returns the tokenID on success so the caller can DB-lookup
   - `Generate() (tokenID, full string, err error)` — new tokenID via `crypto/rand`, plus the full signed string
4. **Repository** — `panel-api/internal/repository/magic_link_token_repository.go`:
   - `Create(ctx, *MagicLinkToken) error` (sets `expires_at`, leaves `used_at` nil)
   - `FindByTokenHash(ctx, hash string) (*MagicLinkToken, error)`
   - `MarkUsed(ctx, id string) error` (atomic: `UPDATE … SET used_at = NOW() WHERE id = ? AND used_at IS NULL`; returns ErrNotFound if rows affected is 0 — that's the replay path)
   - `DeleteExpired(ctx) (int, error)` — janitor for the reconciler

### Tasks

1. New `panel-api/internal/db/migrations/000052_magic_link_tokens.{up,down}.sql`:
   - up.sql: `CREATE TABLE magic_link_tokens (id CHAR(26) PK, application_install_id CHAR(26) NOT NULL, panel_user_id CHAR(26) NOT NULL, token_hash CHAR(64) NOT NULL UNIQUE, expires_at DATETIME(6) NOT NULL, used_at DATETIME(6) NULL, created_at DATETIME(6) NOT NULL, INDEX idx_magic_links_expires (expires_at), CONSTRAINT fk_magic_links_install FOREIGN KEY (application_install_id) REFERENCES application_installs(id) ON DELETE CASCADE)`
   - down.sql: `DROP TABLE magic_link_tokens`
2. New `panel-api/internal/models/magic_link_token.go`
3. New `panel-api/internal/magiclink/key.go` (Load + ErrKeyMissing + ErrKeyWrongSize, copy ssokey's pattern)
4. New `panel-api/internal/magiclink/signer.go` (Generate + Sign)
5. New `panel-api/internal/magiclink/verifier.go` (Verify + constant-time HMAC compare)
6. New `panel-api/internal/repository/magic_link_token_repository.go` + interface + GORM impl
7. Tests (table-driven, this is the crypto-critical surface — explicit cases listed because "round-trip" alone has been the source of every signed-token CVE in living memory):
   - `magiclink/signer_test.go`:
     - Sign+Verify round-trip succeeds (happy path)
     - Sign output changes when token_id changes (entropy in signature)
     - Sign output changes when install_id changes
     - Sign output changes when expires_at changes (timestamp included in HMAC input)
     - Sign output is deterministic for fixed inputs + key (no nonce in HMAC)
     - Sign with two different keys produces two different signatures
   - `magiclink/verifier_test.go`:
     - Verify rejects wrong key with `ErrSignatureMismatch`
     - Verify rejects wrong install_id (cross-install replay defence) with `ErrInstallMismatch`
     - Verify rejects malformed token (no `.` separator, non-base64, wrong byte length) with `ErrMalformed`
     - Verify rejects token signed by old key (rotation scenario — multi-key Verify accepts both, single-key rejects)
     - Verify uses constant-time HMAC compare (`crypto/subtle.ConstantTimeCompare`) — assert by reading the file, not by timing test
   - `magic_link_token_repository_test.go`: Create + FindByTokenHash + MarkUsed (replay path returns ErrNotFound) + DeleteExpired
   - `magiclink/key_test.go`: Load handles missing-file (ErrKeyMissing), wrong-size file (ErrKeyWrongSize), happy path
8. **Multi-key Load + Verify (key rotation support)** — `Key.Load` returns `Keys []byte` not `Key []byte`; reads `magic-link.key` as either a single 32B file (single-key mode) or a comma-separated list of base64-encoded 32B keys (rotation mode, newest first). `Sign` always uses `Keys[0]`. `Verify` tries every key in order and returns the first match. Operator runbook in Step 11 documents the rotation procedure.
9. `install.sh`: add `install_magic_link_key` step (32B random into `/etc/jabali-panel/magic-link.key`, mode 0600 root-owned, group-readable by jabali) — same pattern as `install_sso_key`. Call from `main()`.
10. `panel-api/internal/config/config.go`: add `MagicLinkKeyPath string` to `SSOConfig` (it lives next to sso.key, sharing its config sub-tree)

### Verify

```bash
cd panel-api && go build ./... && go test ./internal/magiclink/ ./internal/repository/ ./internal/models/
ls panel-api/internal/db/migrations/000052_*
ls panel-api/internal/magiclink/
```

### Exit criteria

- All new tests green
- No hits if you `grep -rn TODO ./internal/magiclink/`
- Migration applies + rolls back cleanly on a throwaway test schema

### Rollback

`git revert <commit>`. Migration rollback is destructive (drops the table); operator must restore from backup if production data exists. Step is pre-production (no UI yet) so this is acceptable.

---

## Step 10 — Panel API endpoints + WP must-use plugin + agent install step

**Branch**: `m22-magic-link-10-api-plugin`
**Model**: default
**Depends on**: 9
**Parallel with**: —

### Context brief (cold-start)

Wires the runtime. Three pieces:

1. **Panel API endpoints** (gated by RequireKratosSession EXCEPT validate which is unauthenticated, see threat model):
   - `POST /api/v1/applications/:id/magic-link` — caller must own the install (or be admin). Mints a token via `magiclink.Generate`, persists `magic_link_tokens` row with the `panel_user_id` from claims, returns `{ url: "https://<site>/?jabali_admin_login=<token>", expires_in: 60 }`
   - `POST /api/v1/applications/:id/magic-link/validate` — UNAUTHENTICATED. Body `{ "token": "..." }`. Calls `magiclink.Verify`, looks up by token_hash, atomically marks used (replay returns 410 Gone), returns `{ "admin_user": "<wp username>", "expires_in": <remaining seconds> }`. Rate-limited via existing rate-limit middleware, scoped to per-IP buckets.
2. **WP must-use plugin** — `install/wp-mu-plugins/jabali-magic-link.php` (vendored in repo; agent copies to install path during WP install):
   - On every `init` action, check `$_GET['jabali_admin_login']`. If the value is missing, empty, or not the expected length/shape (base64url, 86 chars — see Step 9's format), **no-op immediately** (let WP continue rendering the page). Do NOT call `wp_die` or emit any error — malformed is treated as "not for us" so a page URL accidentally appended with `?jabali_admin_login=anything` doesn't DoS the site.
   - Build `https://<panel-host>:8443/api/v1/applications/<INSTALL_ID>/magic-link/validate` (panel host + install id are PHP constants set at install time by the agent — see Step 10 task list for how they're injected)
   - POST the token via `wp_remote_post` (PHP cURL wrapper): `timeout => 5, sslverify => true, blocking => true, redirection => 0`. Do NOT retry — the token is single-use, and a retry on a 5xx from the panel would race the `used_at` set. One attempt; on network error, `wp_die` with a clear "panel unreachable" message and let the operator click the button again in the panel UI (which mints a fresh token).
   - On 200: `wp_set_auth_cookie( get_user_by( 'login', $admin_user )->ID, false, true ); wp_safe_redirect( admin_url() ); exit;`
   - On 410 (replay): `wp_die( 'Magic link has already been used — return to the panel and click "Log in to admin" again.', 'Login link expired', [ 'response' => 410 ] );`
   - On 401 (expired or invalid signature): `wp_die( 'Magic link is invalid or has expired. Return to the panel and mint a new one.', 'Login error', [ 'response' => 401 ] );`
   - On other non-200: `wp_die( 'Magic-link validation failed (panel returned HTTP ' . intval($code) . '). Contact the panel operator.', 'Login error', [ 'response' => $code ] );` — note we log the status code but NEVER the token or the response body, to prevent token leakage into WP debug logs
   - Sends `header('Referrer-Policy: no-referrer')` before any redirect/die so the URL doesn't leak to the next hop
3. **Agent install step** — replaces the deleted `installAndConfigureOIDCPlugin` function in `panel-agent/internal/commands/wordpress_install.go`:
   - `installMagicLinkMUPlugin(ctx, req, installPath)` — copies `install/wp-mu-plugins/jabali-magic-link.php` to `<installPath>/wp-content/mu-plugins/`, owner `<user>:www-data`, mode `0640`. Substitutes the two constants (`JABALI_PANEL_HOST` and `JABALI_INSTALL_ID`) in the file via `sed`.

### Tasks

1. `panel-api/internal/api/magic_link.go`:
   - `RegisterMagicLinkRoutes(g *gin.RouterGroup, root *gin.Engine, cfg MagicLinkHandlerConfig)`
   - `MagicLinkHandlerConfig{ Tokens, ApplicationInstalls, Domains, Users, Signer, ... }`
   - `mintHandler` (POST under /api/v1/applications/:id/magic-link, behind RequireKratosSession + ownership check)
   - `validateHandler` (POST /magic-link/validate, on root engine without auth, behind rate-limit)
2. `panel-api/internal/app/app.go`: wire `RegisterMagicLinkRoutes` when `MagicLinkSigner` is non-nil
3. `panel-api/internal/api/magic_link_test.go`: mint requires auth, mint validates ownership (404 for wrong user, 403 for cross-tenant admin), validate succeeds once and returns 410 on replay, validate rejects expired token, validate rejects mismatched install_id
4. `install/wp-mu-plugins/jabali-magic-link.php` — fully self-contained, no Composer deps
5. `panel-agent/internal/commands/wordpress_install.go`:
   - Add `installMagicLinkMUPlugin` function (no agent test required for this — it's a copy + sed; covered by integration)
   - Call it from `wordpressInstallHandler` after `wp core install` succeeds, BEFORE perms normalize so the mu-plugin gets the same `<user>:www-data` 0640 treatment
   - The copy source is `/usr/local/lib/jabali/wp-mu-plugins/jabali-magic-link.php` — install.sh ships this (Step 10 task 7)
6. `install.sh`: add `install_jabali_wp_mu_plugin` step that copies `install/wp-mu-plugins/jabali-magic-link.php` to `/usr/local/lib/jabali/wp-mu-plugins/`. Call from main().
7. `panel-api/cmd/server/cli_app_install.go`: wire the magic-link signer + token repo into the CLI's `ApplicationHandlerConfig` (the apps framework needs them for the agent payload — pass `panel_host` and `install_id` to the agent so the mu-plugin's constants can be injected)

### Verify

```bash
cd panel-api && go build ./... && go test ./...
cd ../panel-agent && go build ./... && go test ./...
# Integration: install a WP, curl the mint endpoint with a valid Kratos session, follow the URL, expect 200 + auth cookie
```

### Exit criteria

- `POST /api/v1/applications/:id/magic-link` returns a URL containing the install's domain + a token
- `?jabali_admin_login=<token>` on the WP install logs the user in
- Replay of the same token returns 410
- Token after 60s returns 410
- All new + existing tests green

### Rollback

`git revert <commit>`. Removes the routes; existing tokens become unverifiable (signer is gone) but no data corruption.

---

## Step 11 — Panel UI button + tests + runbook

**Branch**: `m22-magic-link-11-ui-finalize`
**Model**: default
**Depends on**: 10
**Parallel with**: —

### Context brief (cold-start)

Surface the magic-link mint as a per-row action on the My Applications page.

UI placement: in the Actions column of the WP install row, between "Clone" and "Delete". Button label `Log in to admin`. Click handler:
1. POST `/api/v1/applications/${install.id}/magic-link`
2. Open the returned `url` in a new tab via `window.open(url, "_blank", "noopener,noreferrer")`
3. Show an AntD success toast `"Opened login link in a new tab"` if window.open succeeds, error toast otherwise

The button shows only for app types where the WP plugin is installed — for now that's just `wordpress`. A `descriptor.SupportsMagicLink bool` field on the apps registry would generalize, but defer until the second consumer arrives.

### Tasks

1. `panel-ui/src/shells/user/applications/UserApplicationsList.tsx` (and the admin variant):
   - Add `Log in to admin` button to the Actions column, conditional on `app_type === "wordpress"`
   - `onClick` handler calls a new `useMagicLink(installId)` hook from `panel-ui/src/hooks/useMagicLink.ts`
2. New `panel-ui/src/hooks/useMagicLink.ts`:
   - Returns `{ mint, loading, error }` — wraps `apiClient.post("/applications/" + installId + "/magic-link")`
3. `panel-ui/src/hooks/useMagicLink.test.ts`:
   - Mocks the POST, asserts the URL is opened with the right window.open args
4. New `panel-ui/tests/e2e/magic-link.spec.ts`:
   - Sign in, visit Applications, click the button, assert window.open called with the expected URL pattern
5. `plans/m22-magic-link-runbook.md` (operator-facing):
   - "How to revoke an active session" — `mariadb -D jabali_panel -e "UPDATE magic_link_tokens SET used_at = NOW() WHERE used_at IS NULL"` (kills every outstanding token)
   - "How to rotate the signing key" — generate new key, append to `magic-link.key`'s comma-separated config, restart panel; old tokens still verify until expiry
   - "How to disable magic-link for a specific install" — set a `disabled_at` flag on the install row (TODO: Step 11 doesn't ship this; note as known gap)
   - "What to check if the button is missing" — descriptor field, frontend cache, browser console for 404s
6. `docs/BLUEPRINT.md`: flip M22 status from "in-flight" to "shipped"
7. Memory entry update: `- [M22 magic-link SHIPPED](project_m22_magic_link.md) — 4 steps done, OIDC replacement, button on Applications row`

### Verify

```bash
cd panel-ui && npm run build && npx vitest run && npx playwright test magic-link.spec.ts
# Manual on test VM: install WP via CLI, click button in panel UI, land on wp-admin signed in
```

### Exit criteria

- Button visible on every WordPress install row
- Click → new tab → wp-admin signed in
- Replay of the URL returns 410 (per Step 10's contract)
- Runbook complete
- BLUEPRINT shows M22 SHIPPED

### Rollback

`git revert <commit>`. Removes the button; the API endpoint stays live (Step 10) but is unreachable from the SPA.

---

## Operator handoffs

After Step 7 merges, before Step 8 dispatches:
- Operator runs `plans/m16-rollback-vm-teardown.md` on the test VM
- Operator confirms `systemctl status jabali-hydra` returns `Unit jabali-hydra.service could not be found`
- Operator confirms `mariadb -D jabali_panel -e "DESCRIBE application_installs" | grep oidc` returns empty
- Operator confirms `ss -ltnp | grep -E ':444[45]'` returns empty (no listener on Hydra's public/admin ports)
- Operator confirms `find /var/log -name 'jabali-hydra*' -mtime -1` returns empty (no recent Hydra log activity, indicating the service really stopped)
- Operator confirms (production-only, skipped on dev VM) that the panel-api access log shows zero requests to `GET /oauth2-login`, `GET /oauth2-consent`, `POST /oauth2-consent/accept|deny`, or `/oauth2/*` in the last hour. Stale browser sessions still hitting these paths means the rollback isn't yet quiescent and Step 8 should wait.
- Operator posts the verification output to the planning thread; **only then Step 8 dispatches**. Sub-agent for Step 8 reads this confirmation in the thread context before starting; if absent, the agent halts and reports "operator handoff missing".

After Step 11 merges, before declaring M22 done:
- Operator runs the magic-link end-to-end on the test VM (one fresh install + one click)
- Operator confirms the runbook procedures actually work (revoke + rotate)

## Memory entry to land with this plan

```
- [M16 ROLLED BACK + M22 magic-link plan](project_plan_m16_rollback_m22.md) — 11 steps at plans/m16-rollback-and-m22-magic-link.md, opus-reviewed; M16 fully out, M22 magic-link replaces panel→WP-admin one-click; key files: install/wp-mu-plugins/jabali-magic-link.php, panel-api/internal/magiclink/, migration 000052
```
