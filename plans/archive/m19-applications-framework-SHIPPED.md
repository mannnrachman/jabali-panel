# Plan: M19 — Applications Framework (rename WordPress, register more apps)

**Status:** Draft.
**Owner:** shuki
**Scope:** Rename the "WordPress" section to "Applications" and turn the
existing WP install/delete/clone path into a generic 1-click installer
framework that other CMS apps (DokuWiki, MediaWiki, Joomla, Drupal,
phpBB, PrestaShop, Moodle, Nextcloud, Matomo, …) plug into. WordPress becomes the first registered app, not a hard-coded
special case.
**Depends on:** M2 ✅ (domains), M7 ✅ (databases), M9 ✅ (per-user PHP-FPM),
M10 ✅ (WordPress, the surface we're generalising). The
`(domain_id, subdirectory)` unique index from `feat-wp-multi-install`
(local commit, may not be on origin/main yet — check before Step 1).
**Next migration after main HEAD:** `000046` (current head is `000045`).
**Next ADR:** `0033-m19-applications-framework`.
**Working directory:** `/home/shuki/projects/jabali2-a` — branch off
`origin/main` per step.

---

## 0. Operating assumptions (read before you start any step)

### Conventions inherited from this repo

- **Branch + commit, do not push.** Every step gets its own feature
  branch off `origin/main`; commit there, build, deploy to
  `root@192.168.100.150` via scp/rsync, never `git push`. The user merges to
  `main` themselves. Per memory `feedback_fetch_rebase_before_deploy`:
  before every build, run `git fetch origin main && git rebase
  origin/main` so the deploy reflects upstream and the next
  `jabali update` doesn't silently regress your work.
- **Conventional commits**: `feat(apps): …`, `feat(api): …`, etc.
- **Go style:** `gofmt` + `go vet`; table-driven tests; `make test`
  must stay green per step. Handlers follow the existing
  `api.*HandlerConfig` injection pattern.
- **Migrations:** golang-migrate, both `.up.sql` and `.down.sql`. No
  destructive defaults; back-fill `app_type='wordpress'` for existing
  rows. Confirm the next free number with `ls
  panel-api/internal/db/migrations/` before adding — main may have
  advanced.
- **FK-referenced index drops** require `ADD UNIQUE … ; DROP INDEX old`
  ordering (errno 1553 — see migration `000045` for the canonical
  example).
- **Agent wire:** NDJSON over UDS. JSON tag drift is invisible to
  mock-based tests — every new agent command needs golden testdata
  consumed by both sides per `feedback_cross_boundary_contracts`.
- **Sub-agent dispatch is forbidden** for cross-boundary work
  (panel↔agent JSON contracts). Hand-roll or sequential only — see
  `feedback_subagent_contract_drift`.
- **Shipping cadence per step:** commit on a feature branch, build
  binaries, scp + rsync to `root@192.168.100.150`, restart, smoke-test. The
  user runs `jabali update` whenever they want; for changes to outlive
  that, the commit must reach `origin/main` (user-driven merge).
- **`install.sh` is truth:** if a step requires a new system package
  (e.g. `unzip` for MediaWiki tarballs), add it to `install.sh`; never
  assume it's pre-installed (`feedback_deps_in_installer`).

### Hard sequencing rules

- **Step 2 is a wave gate.** The registry interface is load-bearing
  for Steps 3 through 7. Do NOT start Step 3 until Step 2 is reviewed
  and merged. If Step 6 surfaces a missing field on `App` /
  `ParamSpec`, amend Step 2 in place and rebase the downstream
  branches — do NOT add the field as a Step 6-local hack.
- **Step 5 (UI) is a hard prerequisite for Steps 6 and 7.** The
  install-modal dynamic field rendering and the `RequiresDB=false`
  conditional must be on `origin/main` before DokuWiki can be
  smoke-tested.

### What we are NOT doing in M19

- **Node-runtime apps (n8n).** The framework here ships with a single
  runtime: PHP-FPM + per-user slice. Adding Node means a new systemd
  template, a process supervisor, a port allocator, and reverse-proxy
  rules — out of scope here. Punt to a follow-up M20.
- **Auto-update inside an app.** Apps update via the user's normal
  update path (e.g. wp-cli for WP); the panel doesn't bump versions
  on its own.
- **Two installs of the same app at the same `(domain, subdir)`.**
  The unique constraint expands to
  `(domain_id, subdirectory, app_type)` — different app types can
  share a slot, but two WordPresses can't.
- **Backwards-compat HTTP routes after this milestone.** Both
  `/wordpress-installs` and `/applications` ship together for one
  release window (M19); a follow-up M19.1 deletes the old paths and
  the rename completes.

---

## 1. Dependency graph

```
Step 1 (schema + model rename)
    │
    ▼
Step 2 (apps registry + WordPress descriptor)
    │
    ├──────────────┬──────────────────────┐
    ▼              ▼                      ▼
Step 3      Step 4 (agent dispatcher)   Step 5 (UI rename + app picker)
(generic API)       │                      │
    │               │                      │
    └───────┬───────┴──────────────────────┘
            │
            ▼
   ┌────────┴────────┐
   ▼                 ▼
Step 6              Step 7
(DokuWiki app)     (MediaWiki app)     ← parallel; each adds one Descriptor
   │                 │
   └────────┬────────┘
            ▼
       Step 8 (ADR-0033, BLUEPRINT, runbook)
```

Steps 3, 4, 5 run sequentially because Step 4 (UI) reads the new API
shape Step 3 ships, and Step 5 (agent dispatcher) is what Step 4's
`Install Application` button calls into. Steps 6 and 7 are independent
(different agent files, different registry rows) and can be split
across two sub-sessions if useful — but per
`feedback_subagent_contract_drift`, a single operator hand-rolling them
sequentially is safer than parallel dispatch.

## 2. Model tier per step

| Step | Tier | Why |
|------|------|-----|
| 1 | default | Migration + repo rename. Mechanical. |
| 2 | **strongest** | Registry interface design — gets reused for every future app. |
| 3 | default | New API routes mirroring existing /wordpress-installs shape. |
| 4 | default | Agent dispatcher — registers new commands, delegates to existing impls. |
| 5 | default | UI rename + dropdown. Mechanical once the API is in place. |
| 6 | **strongest** | First non-WP app — exercises the framework, surfaces what the registry got wrong. Expect to amend Step 2's interface. |
| 7 | default | Second non-WP app — should be a drop-in if Step 6 was done well. |
| 8 | default | Docs + ADR. |

## 3. Invariants verified after every step

After each step, before commit:

1. `make test` green (race detector on).
2. `cd panel-ui && npm test && npx tsc --noEmit && npm run lint` clean.
3. Existing WordPress installs still work end-to-end on
   `https://192.168.100.150` (smoke: list view loads, install modal opens,
   "open" link goes to `https://<domain>/<subdir>/`, delete works).
4. `git fetch origin main && git rebase origin/main` succeeds without
   conflict before building binaries.
5. Build + deploy succeeded; `systemctl is-active jabali-panel
   jabali-agent` both report `active`.

If any invariant fails, the step is not complete. Don't move on.

## 4. Steps

---

### Step 1 — Rename `wordpress_installs` → `application_installs`, add `app_type`

**Branch:** `m19/01-schema-rename`.

**Context brief (cold-start safe):**

The DB has a single `wordpress_installs` table with a composite unique
on `(domain_id, subdirectory)` (added by migration 000045 in the
`feat-wp-multi-install` branch — verify it's on `origin/main` before
starting). Every install is implicitly WordPress. We need to widen the
table to host any app type, keyed by a new `app_type` column.

The tricky bits:

- The composite unique must become `(domain_id, subdirectory, app_type)`
  so e.g. domain `x.com/blog` can host both a WordPress AND a
  DokuWiki install.
- `fk_wpinstalls_db` references `databases(id)` and is RESTRICT — be
  careful with index drops (errno 1553). Pattern: `ADD UNIQUE
  uniq_app_installs_domain_subdir_apptype …; DROP INDEX
  uniq_wpinstalls_domain_subdir`.
- Existing rows back-fill cleanly: `app_type = 'wordpress'`.
- The Go model + repo + handler config still reference `WordPress*`
  names. Rename the type to `ApplicationInstall` and the repo to
  `ApplicationInstallRepository`; keep the existing JSON tags
  (`json:"domain_id"` etc.) so the API response shape stays identical
  for now — Step 3 introduces the new shape with `app_type`.

**Tasks:**

1. Add migration `000046_rename_wordpress_installs_to_applications`:
   - `RENAME TABLE wordpress_installs TO application_installs;`
   - `ALTER TABLE application_installs ADD COLUMN app_type VARCHAR(32)
     NOT NULL DEFAULT 'wordpress' AFTER subdirectory;`
   - Add new composite unique with `app_type`, then drop the old one
     (FK-safe ordering).
   - Down migration reverses (rename back, drop column, restore old
     unique). Refuse if non-`wordpress` rows exist.
2. Rename `panel-api/internal/models/wordpress_install.go` →
   `application_install.go`. Type `WordPressInstall` →
   `ApplicationInstall`. Add `AppType string \`gorm:"type:varchar(32);
   not null;default:'wordpress'" json:"app_type"\``. Update the
   `uniqueIndex` GORM tag to span all three columns with explicit
   priority ordering — GORM requires contiguous priorities or the
   composite index won't materialise:
   - `DomainID`     → `uniqueIndex:uniq_app_installs_domain_subdir_apptype,priority:1`
   - `Subdirectory` → `uniqueIndex:uniq_app_installs_domain_subdir_apptype,priority:2`
   - `AppType`      → `uniqueIndex:uniq_app_installs_domain_subdir_apptype,priority:3`
3. Rename `repository/wordpress_install_repository.go` →
   `application_install_repository.go`. Interface
   `WordPressInstallRepository` → `ApplicationInstallRepository`.
   Add a new method:
   `FindByDomainAndSubdirectoryAndAppType(ctx, domainID, subdirectory,
   appType) (*ApplicationInstall, error)` — the API will call this
   in Step 3 to enforce the per-`(domain, subdir, app_type)` 409
   rule. Keep `FindByDomainAndSubdirectory` for callers that don't
   yet care about app_type.
4. Wire-up: `app.go` constructs the renamed repo. The handler config
   field stays `WordPressInstalls` in this step — Step 3 renames it
   to `ApplicationInstalls` once the new API ships, and adds
   backwards-compat aliasing for the WordPress-only routes that
   still reference the old name.
5. Update mocks in `wordpress_test.go` (and any test file that
   embeds the repo interface) to match the new method set.
6. Verify: `make test` green; on the live server the migration
   applies cleanly (`schema_migrations` advances to 046, no `dirty`
   flag); the existing WP page still functions.

**Verification commands:**

```bash
go build ./... && make test
# After deploy:
ssh -p 2222 root@192.168.100.150 "mariadb jabali_panel -e 'SHOW CREATE TABLE \
  application_installs\\G' | grep -E 'app_type|UNIQUE KEY'"
ssh -p 2222 root@192.168.100.150 "mariadb jabali_panel -e 'SELECT * FROM \
  schema_migrations'"
# Both: app_type column present, unique on (domain_id,subdirectory,app_type),
# schema_migrations row {version=46, dirty=0}.
```

**Exit criteria:** migration applied; existing WP installs still
listed and functional; `make test` green; binary deployed; commit on
`m19/01-schema-rename`.

**Rollback:** `git checkout -- .` on the branch + `mariadb -e
'UPDATE schema_migrations SET version=45, dirty=0;' &&
'<down-migration SQL>'`. The old binary on the server is the previous
`/usr/local/bin/jabali-panel` (which we backed up to `.prev` per the
deploy convention).

**Rollback caveat (read before Step 6 ships):** the down migration
refuses if any row has `app_type != 'wordpress'`. Once Step 6
(DokuWiki) is deployed and a single non-WP install exists, this
schema change is one-way in production. M19 is not a reversible
milestone past Step 6 — bug-fix forward, do not roll back.

---

### Step 2 — Apps registry (`internal/apps`), WordPress descriptor

**Branch:** `m19/02-apps-registry`.

**Model tier:** strongest — this is the load-bearing interface.

**Context brief:**

The framework hinges on a single registry of "app descriptors". Each
descriptor declares: display name, icon (lucide/antd name), default
subdirectory hint, requires_db (bool), supported PHP versions,
install/delete/clone agent command names, and a typed parameter
schema for the install modal.

This step ships:

- `panel-api/internal/apps/registry.go` — `App` struct + `Registry`
  with `Register(name string, descriptor App)` and `Get(name string)
  (App, bool)`.
- `panel-api/internal/apps/wordpress.go` — registers `"wordpress"`
  with the existing agent command names (`wordpress.install`,
  `wordpress.delete`, `wordpress.clone`). Does NOT change the agent
  side yet; just records what the API knows.
- `panel-api/internal/apps/registry_test.go` — registry round-trip,
  duplicate-name rejection, unknown-name lookup behaviour.
- `app.go` constructs the registry at startup and passes it into
  handlers that will need it (Step 3).

**Tasks:**

1. Define the `App` struct. Fields:
   - `Name string` (machine, e.g. `"wordpress"`)
   - `DisplayName string` (UI, e.g. `"WordPress"`)
   - `Icon string` (antd icon name)
   - `Description string` (short tagline for the install picker)
   - `DefaultSubdirectory string` (UI hint, e.g. `"blog"` for WP, `""`
     for docroot-default apps)
   - `RequiresDB bool`
   - `SupportedPHPVersions []string` (empty = "any")
   - `AgentInstallCmd, AgentDeleteCmd, AgentCloneCmd string`
   - `InstallParamSchema map[string]ParamSpec` (per-app form fields
     beyond the generic ones — e.g. WP wants admin_email/username/
     password/site_title/locale; DokuWiki wants admin + license enum)
2. `ParamSpec`:
   ```go
   type ParamSpec struct {
       Type        string   // "string"|"email"|"password"|"enum"|"bool"
       Required    bool
       Pattern     *string  // optional regex (string types)
       Values      []string // populated when Type=="enum"
       Default     any
       Description string   // shown as the AntD Form.Item extra
   }
   ```
   The UI in Step 5 reads `Type` to pick the AntD control
   (`Input` / `Input.Password` / `Select` / `Switch`); the API in
   Step 3 validates against `Pattern` and `Values`. Add unit tests
   for each type.
3. Registry: `New() *Registry`; `Register(d App) error` (rejects
   duplicates); `Get(name string) (App, bool)`; `List() []App` (for
   the install picker).
4. WordPress descriptor at `apps/wordpress.go` initialised in
   `app.go`'s wiring code; assert in tests it's registered after
   startup.

**Verification commands:**

```bash
go test ./panel-api/internal/apps/... -v -count=1 -race
```

**Exit criteria:** registry exposes WordPress; tests cover happy + sad
paths; `make test` green; nothing user-visible changed yet.

---

### Step 3 — Generic `/applications` API surface

**Branch:** `m19/03-applications-api`.

**Context brief:**

Today: `POST /wordpress-installs`, `GET /wordpress-installs`, `DELETE
/wordpress-installs/:id`, `POST /wordpress-installs/:id/clone`. Body
is WP-specific.

Goal: introduce the generic surface alongside (not instead of) the
old:

- `POST /applications` (body: `{ app_type, domain_id, subdirectory,
  use_www, params: { …per-app… } }`)
- `GET /applications`
- `DELETE /applications/:id`
- `POST /applications/:id/clone`

Validation flow: read `app_type`, look it up in the registry, validate
`params` against `App.InstallParamSchema`, dispatch to the existing
WordPress code path (which Step 4 will generalise on the agent side).

The old `/wordpress-installs` routes stay registered and translate to
the new code path with `app_type='wordpress'` hard-coded — gives the
UI one release to migrate.

**Tasks:**

1. Add `panel-api/internal/api/applications.go` with the four
   CRUD handlers PLUS a fifth: `GET /applications/registry` returning
   `registry.List()` as JSON (DisplayName, Icon, Description,
   RequiresDB, supported PHP versions, InstallParamSchema). The UI
   in Step 5 calls this to populate the app picker; without it,
   Step 5 has no way to render the dropdown.
2. Reuse `wordpress.go` helpers (the DB-create rollback chain, the
   install-kicker goroutine); refactor those into shared functions
   where they're exact dupes.
3. Rename `WordPressHandlerConfig` → `ApplicationHandlerConfig` and
   the `WordPressInstalls` field → `ApplicationInstalls`. Add a
   compile-time alias (`type WordPressHandlerConfig =
   ApplicationHandlerConfig`) so the legacy WordPress route
   registration still compiles unchanged through the M19 release
   window.
4. Validate `params` against `descriptor.InstallParamSchema`. Reject
   unknown fields, missing required, type / pattern / enum
   mismatches.
5. **`RequiresDB=false` short-circuit:** when the descriptor declares
   no DB requirement, skip the entire DB-create / DB-user / grant
   chain (and the matching agent calls `db.create`, `db_user.create`,
   `db_user.grant`). Pass `db_id=""` through to the install kicker;
   the agent must accept an empty `db_id` for non-WP apps.
6. The `(domain, subdir, app_type)` uniqueness check calls
   `repo.FindByDomainAndSubdirectoryAndAppType`; on hit, return 409
   `install_exists`. Different `app_type`s in the same `(domain,
   subdir)` slot are allowed by design.
7. `wordpress.go` handlers stay registered on `/wordpress-installs`;
   each one becomes a 4-line wrapper that synthesises an
   `application_create_request{app_type:"wordpress", …}` and calls
   the new handler. The API surface remains binary-compatible for
   the M19 window.
8. **Agent calls in Step 3 still use the legacy `wordpress.install`
   command name.** Step 4 introduces the agent-side `app.install`
   dispatcher and re-points the API at it. Don't try to skip ahead
   here — the agent hasn't registered `app.install` yet.
9. Tests in `applications_test.go`:
   - Happy path: WP install via `/applications` produces a row
     identical to the legacy `/wordpress-installs` row shape.
   - 400 `invalid_app_type` for an unregistered name.
   - 400 `invalid_params` for each ParamSpec failure mode (missing
     required, bad pattern, value not in enum).
   - 409 `install_exists` for a duplicate `(domain, subdir,
     app_type)`.
   - 409 `install_exists` does NOT fire when only `app_type` differs
     in the same `(domain, subdir)`.
   - 500 propagation when the DB-create fails (RequiresDB=true path).
   - `RequiresDB=false` install skips the DB-create chain entirely
     (assert no `db.create` agent call was issued).
   - `GET /applications/registry` returns the registered apps
     including WordPress.

**Verification commands:**

```bash
go test ./panel-api/internal/api/... -count=1 -race -run \
  'TestApplications|TestWordPress' -v
# Manual smoke after deploy:
curl -sS -k -X POST https://localhost:8443/applications \
  -H 'Content-Type: application/json' -b "auth=…" \
  -d '{"app_type":"wordpress","domain_id":"…","subdirectory":"test", \
       "params":{"admin_email":"…","admin_username":"admin", \
       "site_title":"t","locale":"en_US"}}'
```

**Exit criteria:** both API surfaces work; new + old WP installs land
the same row shape; `make test` green; deployed.

---

### Step 4 — Agent dispatcher: `app.install`/`app.delete`/`app.clone`

**Branch:** `m19/04-agent-dispatcher`.

**Context brief:**

The agent today has `wordpress.install`, `wordpress.delete`,
`wordpress.clone`. Each is a self-contained handler. We need a
generic shim:

- `app.install` takes `{app_type, ...}` and dispatches by `app_type`
  to the existing `wordpress.install` handler (or future
  `dokuwiki.install`, etc.).
- Same for `app.delete` and `app.clone`.
- The old WP-specific commands STAY registered — the panel still
  calls them in this step (Step 6/7 introduce non-WP apps that go
  through `app.install`).

The dispatcher lives in `panel-agent/internal/commands/app_dispatch.go`
and uses an internal map populated by `init()` blocks in each
app-specific handler file. Pattern:

```go
// in wordpress_install.go init():
RegisterAppInstaller("wordpress", wordpressInstallHandler)
```

This keeps each app's code in one file and makes the agent's app
surface trivially extensible.

**Tasks:**

1. Add `app_dispatch.go`. Pseudocode:
   ```go
   var (
       appInstallers = map[string]CommandHandler{}
       appDeleters   = map[string]CommandHandler{}
       appCloners    = map[string]CommandHandler{}
   )

   func RegisterAppInstaller(appType string, h CommandHandler) {
       if _, dup := appInstallers[appType]; dup { panic(...) }
       appInstallers[appType] = h
   }
   // …same shape for deleters + cloners.

   func appInstallHandler(ctx context.Context, raw json.RawMessage) (any, error) {
       var head struct{ AppType string `json:"app_type"` }
       if err := json.Unmarshal(raw, &head); err != nil { … }
       h, ok := appInstallers[head.AppType]
       if !ok { return nil, &agentwire.AgentError{Code: agentwire.CodeInvalidArgument, Message: "unknown app_type: "+head.AppType} }
       return h(ctx, raw) // forward the full body unchanged
   }

   func init() {
       Default.Register("app.install", appInstallHandler)
       Default.Register("app.delete",  appDeleteHandler)
       Default.Register("app.clone",   appCloneHandler)
   }
   ```
2. In `wordpress_install.go` add an `init()` that calls
   `RegisterAppInstaller("wordpress", wordpressInstallHandler)`;
   same for delete + clone. The existing `Default.Register("wordpress.install", …)` lines STAY — old command keeps working until M19.1
   removes them.
3. Cross-boundary contract test — golden fixtures live in TWO
   directories per the M10 pattern (one per side, no shared file):
   - `panel-api/internal/agent/testdata/app_install_wordpress_request.json`
     — read by the panel-side marshal test that asserts the request
     body the panel sends matches what the agent expects.
   - `panel-agent/internal/commands/testdata/app_install_wordpress_response.json`
     — read by the agent-side marshal test that asserts the response
     shape the panel will unmarshal.
   - Repeat both for `app_delete` and `app_clone`. Six fixtures
     total. Avoids the JSON-tag drift bug class per
     `feedback_cross_boundary_contracts`.
4. **API → agent rewire:** point Step 3's handlers at `app.install`
   (with `app_type` in the param) instead of the legacy
   `wordpress.install`. Smoke-test that an existing WP install +
   delete + clone all succeed via the new command name. The legacy
   `wordpress.*` commands stay registered on the agent (delete in
   M19.1) so any straggler caller doesn't break.

**Verification commands:**

```bash
go test ./panel-agent/internal/commands/... -count=1 -race
go test ./panel-api/internal/agent/... -count=1 -race
# Smoke after deploy: install + delete a WP via the panel; tail
# /var/log/jabali-agent.log to confirm "method=app.install" not
# "method=wordpress.install".
```

**Exit criteria:** WP install + delete + clone all work via the new
`app.*` agent commands; legacy commands still respond (but unused);
contract tests cover the wire format; deployed.

---

### Step 5 — UI: rename "WordPress" → "Applications", add app picker

**Branch:** `m19/05-ui-rename`.

**Context brief:**

Two user-visible files:

- `panel-ui/src/shells/user/wordpress/UserWordPressList.tsx`
- `panel-ui/src/shells/admin/wordpress/AdminWordPressList.tsx`

Plus the Refine resource entries in `App.tsx` (lines 147-150,
208-210), the install + clone modals, and the sidebar label.

This step:

1. Rename the directories: `shells/user/wordpress/` →
   `shells/user/applications/` and same for admin. Move + rename
   files: `UserWordPressList.tsx` → `UserApplicationList.tsx`,
   `InstallWordPressModal.tsx` → `InstallApplicationModal.tsx`, etc.
2. Refine resource: `wordpress-installs` → `applications`. Routes
   `/jabali-panel/wordpress` → `/jabali-panel/applications` and
   `/jabali-admin/wordpress` → `/jabali-admin/applications`.
3. Sidebar label: `"WordPress"` → `"Applications"`. Pick a more
   generic icon (`AppstoreOutlined` or `ApiOutlined`).
4. Install modal: at the top, an "App" `Select` populated from
   `GET /apps` (new endpoint that returns the registry list). The
   per-app fields are rendered dynamically from the descriptor's
   `InstallParamSchema`. WordPress is the default selection so the
   modal looks identical to today for that case.
5. List page: new "App" column at column 1 showing the descriptor's
   icon + display name; the existing Folder + Version + Status
   columns follow.
6. `package.json` / vite imports: ripgrep for `wordpress` and
   `WordPress` after the rename to catch stragglers; expected misses
   are agent-binary command names (those rename in M19.1).
7. Update Playwright + vitest tests for the new file paths and
   selectors.

**Verification commands:**

```bash
cd panel-ui && npx tsc --noEmit && npm test && npm run lint
cd panel-ui && npm run build
# After deploy, hard-refresh + check:
# - sidebar reads "Applications"
# - URL is /jabali-panel/applications
# - Install modal shows "App" dropdown with WordPress selected
# - Existing 123123.com/ddd install still appears in the table
```

**Exit criteria:** sidebar + URL + modal renamed; existing WP installs
visible and functional; `npm test` green; deployed.

---

### Step 6 — Add second app: DokuWiki (validates `RequiresDB=false`)

**Branch:** `m19/06-app-dokuwiki`.

**Model tier:** strongest — first non-WP app validates the framework.

**Hard prerequisites (block on these before starting):**

- Step 5 fully merged to `origin/main` and deployed; the install
  modal renders `RequiresDB=false` apps without admin/email/password
  fields. Verify by inspecting the live install modal — open it,
  switch the app picker to a temporary `RequiresDB=false` test
  descriptor, confirm those fields disappear.
- Step 4 fully merged; `app.install` works for WordPress through
  the new dispatcher.

**Context brief:**

DokuWiki is a flat-file PHP wiki — no database, no PHP extensions
beyond the LAMP defaults, configuration is plain text on disk. Picked
as the second app specifically because it exercises the framework's
`RequiresDB=false` short-circuit (skip db.create / db_user.create /
db_user.grant) without dragging in the more involved CLI installers
the M19 catalog will need later.

Differences from WordPress:

- **No DB.** `RequiresDB=false`. The API skips the entire db-create
  chain (Step 3 task #5).
- **No web wizard during install.** DokuWiki ships with an installer
  page (`install.php`) but it can be skipped by writing
  `conf/users.auth.php`, `conf/local.php`, and `conf/acl.auth.php`
  pre-populated with the admin user. The agent installer does this
  inline so the install completes without a manual web step.
- **Pinned release.** Download the latest stable tarball from
  `https://download.dokuwiki.org/src/dokuwiki/dokuwiki-stable.tgz`,
  verify checksum, extract.

**Tasks:**

1. `panel-api/internal/apps/dokuwiki.go` — descriptor with
   `RequiresDB=false`. Per-app params:
   - `site_title` (string, required)
   - `admin_username` (string, required, default "admin")
   - `admin_email` (email, required)
   - `admin_password` (password, optional — generate when blank)
   - `license` (enum: cc-by-sa, cc-by-nc-sa, public-domain, gpl,
     none. Default cc-by-sa.)
2. `panel-agent/internal/commands/dokuwiki_install.go`:
   - Download the stable tarball under
     `systemd-run --uid=<user> --slice=jabali-user-<user>.slice`,
     verify the SHA-256 against a hard-coded checksum in the
     installer (bump per release).
   - Extract to install path. Write `conf/local.php` with the site
     title + license. Write `conf/users.auth.php` with a
     `password_hash($admin_pass, PASSWORD_DEFAULT)`-style line for
     the admin user. Write `conf/acl.auth.php` with sensible
     defaults (admin gets `@ALL`, anonymous read).
   - Drop `data/install.lock` so DokuWiki doesn't re-prompt for
     setup.
   - Reuse `removePlaceholderIndex` and `normalizePermsToWwwData`
     from the WP installer.
3. `dokuwiki_delete.go` — mirror of `wordpress_delete.go` but
   without the DB drop. Restore the placeholder `index.html` if the
   subdir is the docroot.
4. Register installers via `RegisterApp*("dokuwiki", …)`.
5. UI: DokuWiki appears in the app picker. The install modal renders
   only the per-app params from the descriptor — the
   admin/email/password fields are NOT WordPress-specific anymore;
   they come from `dokuwiki`'s own `InstallParamSchema`.
6. `install.sh`: `tar` is already present on the install host; no
   change needed. Verify on a fresh test box that `command -v tar`
   exists before declaring no-op.
7. E2E test: install DokuWiki to `123123.com/wiki`, confirm
   `https://123123.com/wiki/` returns the DokuWiki "Welcome" page
   (NOT the install wizard) and that login with the admin
   credentials returned by the API works.

**Exit criteria:** DokuWiki installable + deletable via the
Applications page; framework gaps surfaced and fed back into Step 2's
interface (e.g. if `enum` ParamSpec rendering needs work for the
license dropdown); deployed.

---

### Step 7 — Add third app: MediaWiki (validates "edit a config file post-install" hook)

**Branch:** `m19/07-app-mediawiki`.

**Context brief:**

MediaWiki's installer is a web wizard that writes
`LocalSettings.php`. We have two options:

(a) Ship MediaWiki at `<docroot>/<subdir>/` and let the user run the
    web wizard manually. Simplest, but the panel-side install button
    is just "download + extract".
(b) Run MediaWiki's CLI installer (`maintenance/install.php`) with
    pre-supplied admin credentials, like wp-cli does for WordPress.
    Higher value but pushes the registry to support
    "post-install hook" since MW's CLI installer is more involved.

Pick (b). It surfaces the missing-from-Step-2 capability if there is
one, and keeps the user experience symmetric with WP.

**Note on hooks:** the MW CLI install runs INSIDE the agent's
`mediawiki.install` handler — it's an agent-internal detail, NOT a
framework-level "post-install hook" callback on the `App` struct.
The registry stays oblivious to per-app install internals; only the
agent installer knows what `php maintenance/install.php` needs.

**Tasks:**

1. `apps/mediawiki.go` descriptor — params: site_name, admin_user,
   admin_password, language. `RequiresDB=true`.
2. `panel-agent/internal/commands/mediawiki_install.go` — download
   release tarball from `releases.wikimedia.org`, extract, run
   `php maintenance/install.php …` under `systemd-run --uid=<user>
   --slice=jabali-user-<user>.slice`, normalise perms.
3. `mediawiki_delete.go` — `rm -rf` the extracted tree + the
   placeholder restore.
4. Register; add UI fields via descriptor; test install + delete +
   page render.

**Exit criteria:** MediaWiki installable; the registry's
`InstallParamSchema` proves it can carry app-specific fields cleanly.
If Step 2's schema needs widening (e.g. a `RuntimeRequirements` field
for MW's `php-intl` extension), do it here as a follow-up commit on
the same branch and update Step 2's commit message.

---

### Step 8 — ADR-0033, BLUEPRINT entry, runbook

**Branch:** `m19/08-docs`.

**Tasks:**

1. `docs/adr/0033-m19-applications-framework.md` — record:
   - The renaming + framework decision.
   - Why a registry over per-app tables (single composite uniqueness,
     simpler reconciler, shared DB lifecycle code).
   - Why PHP-only for v1 (Node deferred to M20).
   - Backwards-compat: legacy `/wordpress-installs` routes deprecated
     for one release; M19.1 deletes them.
2. `docs/BLUEPRINT.md`: add `### M19: Applications Framework
   (SHIPPED)` under Section 6, mark M10 as superseded by M19, update
   the "What's shipped" inventory.
3. `docs/runbooks/applications.md` — operator runbook:
   - How to register a new app type (descriptor + agent installer
     pair).
   - Common failure modes (download fail, DB grant fail, FPM pool
     not bound).
   - Where each app stores its files, what to back up.
4. Update `docs/CONTRIBUTING.md` — mention the registry pattern in
   the architectural-guardrails list.

**Exit criteria:** ADR merged; BLUEPRINT lists M19 as shipped; runbook
present; deployed (docs are static so no service restart).

---

## 5. Open questions (surface during review)

1. **n8n / non-PHP runtimes:** does the user want them ever? If yes,
   the `App` struct needs a `Runtime` field now (even with only `php`
   as a value) to avoid an awkward second migration later.
2. **Per-app database engine:** WordPress assumes MariaDB. Does
   MediaWiki want SQLite as an option? Probably defer to "MariaDB
   only until someone asks".
3. **Versioning:** lock app versions in the descriptor, or always
   pull "latest"? Current WP code pulls latest; recommend keeping
   that consistent across apps and adding `version_pin` to the
   descriptor only when a real need surfaces.
4. **Marketplace icons:** the descriptor has an `Icon string` (antd
   name) — do we want SVG logos for each app instead? Defer until
   we have ≥5 apps.
5. **Permissions model:** can a non-admin user install ANY registered
   app, or do some apps need an admin gate (e.g. resource-heavy
   ones)? Add a `RequiresAdmin bool` field to `App` if yes.

## 6. Parallel dispatch plan

Strictly serial through Step 5 (each builds on the last and ships
user-visible changes). Steps 6 and 7 are independent in code and DB
but should still go sequentially per `feedback_subagent_contract_drift`
— a single operator hand-rolling both, in the same session, is the
safe path. Step 8 lands after both apps work end-to-end.

## 7. Review changelog

**Round 1 (Phase 4 adversarial review, planner sub-agent, 2026-04-19):**

- Step 1 GORM tag — added explicit `priority:1/2/3` for the composite
  uniqueIndex; without it the index doesn't materialise.
- Step 1 — added `FindByDomainAndSubdirectoryAndAppType` repo method
  to the task list (Step 3 needs it for the 409 path).
- Step 1 — added rollback caveat: down migration becomes one-way
  once Step 6 ships and a single non-WP install exists.
- Step 2 — `ParamSpec` fully specified (`Values []string` for enums,
  per-type rendering rules); was vague and would have blocked Step 5.
- Step 3 — added `GET /applications/registry` endpoint; Step 5
  couldn't render the app picker without it.
- Step 3 — explicit handler-config rename + back-compat alias;
  removed ambiguity about whether `cfg.WordPressInstalls` survives.
- Step 3 — explicit `RequiresDB=false` short-circuit (skip
  db.create/db_user.create/db_user.grant); silent before, would have
  broken Step 6.
- Step 3 — call out that the API still calls `wordpress.install`
  in this step; the agent-side `app.install` arrives in Step 4.
- Step 3 — error-case test list expanded (409 same-slot,
  RequiresDB=false skip-DB, registry endpoint).
- Step 4 — six-fixture testdata layout (request panel-side,
  response agent-side, ×3 commands); request/response confusion
  removed.
- Step 4 — pseudocode for `app_dispatch.go` so the registration
  pattern is unambiguous when Step 6 mirrors it.
- Step 6 — hard-prerequisite block on Step 5 being on `origin/main`
  before this step is dispatchable.
- Step 7 — clarified that MW's CLI installer is an agent-internal
  detail, not a framework-level post-install hook (avoids a Step 2
  amendment cascade).
- Operating assumptions — added a "hard sequencing rules" subsection
  flagging Step 2 as the wave gate.

**LOW findings deferred:** open questions left in section 5; runbook
freshness disclaimer left as is for Step 8 author to write.

**Round 2 (user direction, 2026-04-19):**

- Removed phpMyAdmin from Step 6 — out of catalog scope (it's a dev
  tool, not a CMS). Replaced with **DokuWiki** which preserves the
  same `RequiresDB=false` framework-validation goal AND fits the
  Softaculous-style CMS catalog the operator is building toward.
- Step 6 now exercises the `enum` ParamSpec rendering (DokuWiki
  license dropdown) — additional coverage Step 2's interface needs.
- Catalog signal-words in section 0 + intro updated to list CMS
  apps (Joomla, Drupal, phpBB, PrestaShop, Moodle, Nextcloud,
  Matomo, …) instead of dev/admin tools.
