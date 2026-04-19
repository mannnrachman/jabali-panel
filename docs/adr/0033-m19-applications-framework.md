# ADR-0033: M19 Applications Framework

**Date:** 2026-04-19
**Status:** Accepted (steps 1–5 + 8 shipped; steps 6–7 deferred behind a host-deploy gate)
**Deciders:** Shuki

## Context

M10 (ADR-0026) shipped WordPress as a first-class hosting target with a
dedicated `wordpress_installs` table, a `WordPressInstallRepository`,
`/wordpress-installs` REST routes, three WordPress-specific agent
commands, and a WordPress-only install/clone modal. The shape worked
for one app. By 2026-04-19 the operator wanted a Softaculous-style CMS
catalog (DokuWiki, MediaWiki, Joomla, Drupal, phpBB, PrestaShop,
Moodle, Nextcloud, Matomo, …) — all CMS-class apps that share most of
the M10 plumbing (domain binding, optional MariaDB provisioning, async
install via the agent, status reconciliation) but vary on per-app
install parameters and whether they need a database at all.

Forking M10's WordPress-specific stack per app would mean: one model +
repo + handler + routes + UI page per CMS, with the same status
machine duplicated five ways. M19 generalises the plumbing into a
single Applications framework and makes adding a new app a question
of "register a descriptor + write the agent installer".

## Decision

We are renaming the WordPress surface to **Applications** and
introducing a process-wide **app registry** that the API + UI both
consume. The on-disk install path, the (domain, subdirectory) docroot
contract, the status machine, and the reconciler all stay; only the
discriminator (`app_type`) and the per-app parameter shape become
data, not code.

---

## Design Decisions

### 1. One table, one repository, `app_type` discriminator

**Decision:** Rename `wordpress_installs` → `application_installs`
(migration 000046) and add an `app_type VARCHAR(32) NOT NULL DEFAULT
'wordpress'` column. The DB-level composite UNIQUE widens from
`(domain_id, subdirectory)` to `(domain_id, subdirectory, app_type)`,
but the **API enforces the stricter rule**: at most one application
per `(domain_id, subdirectory)` slot regardless of `app_type`.

**Update 2026-04-19 (per operator):** the API check uses
`FindByDomainAndSubdirectory` (any app_type) and returns 409
`install_exists` if the slot is taken. The reason: in practice you
can't run two apps at the same docroot path — they'd serve overlapping
URLs and fight over `index.php`/`index.html` ordering. The wider DB
UNIQUE is kept for forward compatibility (e.g. a future migration
that prefixes app_type into a sub-path, or a true multi-app slot if
nginx routing learns to discriminate by URL prefix), but the product
behaviour today is one-app-per-slot.

**Rationale:**
- One status machine, one reconciler, one set of cross-cutting
  concerns (admin email validation, ownership checks, ListOptions).
- The lifecycle is identical across CMSes: provision DB (optional),
  dispatch agent, await status flip, allow delete/clone. Splitting
  per-app would duplicate the reconciler and the API delete chain.
- Migrations stay tractable: one table, one foreign key set.

**Consequences:**
- `models.WordPressInstall` becomes a type alias for
  `ApplicationInstall` so the M19 release window doesn't break every
  WordPress-specific call site at once. Alias deleted in M19.1.
- The composite unique key materialises only when the GORM tags
  declare contiguous priorities (1, 2, 3) — captured in the model
  comment so a future field reorder doesn't silently break it.
- The down migration uses MariaDB `SIGNAL SQLSTATE '45000'` to refuse
  rollback when any non-WordPress row exists. Once the first DokuWiki
  install lands, the rollback is one-way and intentionally so.

---

### 2. App descriptor registered at startup, not in a database

**Decision:** Each installable app is a Go struct
(`internal/apps.App`) registered into a process-wide `Registry` at
startup. The API reads the registry to validate `app_type`, render
`GET /applications/registry`, and decide which agent command to
dispatch. There is no `app_descriptors` database table.

**Rationale:**
- App descriptors are code: they bind a name to an agent installer
  function. Splitting that into a row in MariaDB would mean the API
  could "register" an app whose installer doesn't exist on the agent
  — the failure mode is silent.
- Adding an app requires a code change anyway (the agent installer).
  Putting the descriptor next to the installer keeps the contract
  in one place.
- `Register()` panics on duplicate names. Programmer errors surface
  at startup, not first request.

**Consequences:**
- The registry must be wired into `app.NewWithDeps`. The `Deps.Apps`
  field is nil-safe — when nil, the constructor builds a default
  registry via `apps.RegisterDefaults` so the existing
  `New()`/`Deps{}` test wiring keeps working unchanged.
- `RegisterDefaults` is the single place where every first-party app
  opts in. Adding an app means: write the descriptor file, write the
  agent installer, append one line to `RegisterDefaults`.
- Future external/plugin apps would need a different registration
  mechanism. Not in scope for M19; defer until a real need arises.

---

### 3. Typed per-app parameter schema with a closed set of types

**Decision:** Each descriptor declares an `InstallParamSchema
map[string]ParamSpec` where `ParamSpec.Type` is one of
`"string"|"email"|"password"|"enum"|"bool"`. The API validates the
incoming `params` blob against the schema (rejects unknown fields,
missing required, type/regex/enum mismatches) before dispatching the
agent. The UI install modal renders the same schema as AntD form
controls.

**Rationale:**
- A WordPress install needs `site_title` + admin credentials; a
  DokuWiki install needs `site_title` + admin + a `license` enum;
  a hypothetical phpBB install needs board name + admin. Bolting all
  of those onto the request struct would either bloat one giant
  request type or fork three.
- A closed type set keeps both validators (server) and renderers
  (client) finite. Adding a sixth type requires updating both — that
  friction is the point.
- `ParamSpec` validation runs at registry registration time, not
  first request. A descriptor with `Type:"enum"` and no `Values`
  fails to register, panicking startup.

**Consequences:**
- The UI today renders WordPress-specific fields by name; switching
  the picker to a `RequiresDB=false` app does NOT yet hide the
  WordPress fields. Folding the renderer over the descriptor's
  schema is a Step 5 follow-up that Step 6 (DokuWiki) blocks on.
- `Pattern *string` is supported on `string` types but not enforced
  on `email` (the email rule is built-in). If a descriptor needs
  per-character constraints on an email's local-part, treat it as a
  `string` and supply a regex.

---

### 4. `RequiresDB` is a per-descriptor flag, not a runtime check

**Decision:** Each descriptor declares whether it needs a MariaDB
database (`RequiresDB bool`). When false, the API skips the entire
panel-row + agent `db.create / db_user.create / db_user.grant` chain
and writes the install row with `db_id=""`.

**Rationale:**
- Flat-file CMSes (DokuWiki) shouldn't pay for a database that never
  gets touched. Skipping the chain saves three round-trips to the
  agent + three rows in MariaDB per install.
- Putting the decision on the descriptor (not e.g. "if
  `params.db_required`") keeps the per-app authors in charge of
  their own database story.

**Consequences:**
- The agent's app installer for a `RequiresDB=false` app must accept
  an empty `db_id` (or simply not read it). Documented in the
  runbook so a new app author doesn't assume DB credentials are
  always passed in.
- The clone path is gated to apps that have a non-empty
  `AgentCloneCmd`; a `RequiresDB=false` app without a clone
  installer simply hides the Clone button. Today only WordPress
  declares `AgentCloneCmd`.

---

### 5. Agent dispatch via three generic commands + a per-app routing table

**Decision:** The agent registers three new commands —
`app.install`, `app.delete`, `app.clone` — that read `app_type` off
the request body and forward the unchanged body to the matching
per-app handler. Each per-app handler file (`wordpress_install.go`,
future `dokuwiki_install.go`, …) opts into the routing table from
its package-level `init()` via `RegisterAppInstaller(name, handler)`.

The legacy `wordpress.install / wordpress.delete / wordpress.clone`
commands stay registered through the M19 release window so a panel
rolled back to a pre-M19 build keeps working. M19.1 deletes them.

**Rationale:**
- Adding an app means adding two `init()` lines on the agent and
  three on the panel descriptor. No global dispatch table to edit.
- Forwarding the body unchanged keeps each app's request/response
  shape under one file (its own `*_install.go`). Cross-boundary
  drift only matters within that one file pair.
- Three commands instead of one (`app.install` + `app.delete` +
  `app.clone`) keeps the agentwire log readable — operators see
  *what* the panel asked for, not just "app op".

**Consequences:**
- The dispatch table is process-global (per-package mutex) and
  populated by `init()`. Test isolation requires snapshot/restore
  helpers (in `app_dispatch_test.go`) — direct reset corrupts other
  suites in the same binary.
- The cross-boundary contract test (six fixtures under
  `panel-api/internal/agent/testdata/app_*.json`) catches JSON-tag
  drift between the panel's typed structs and the wire shape. Per
  the M10 lesson recorded in `feedback_cross_boundary_contracts`,
  silent panel↔agent JSON drift is invisible to mock-based tests
  and only surfaces in production; the round-trip tests close that
  gap.

---

### 6. Both API surfaces stay live through M19; UI cuts over to `/applications`

**Decision:** The legacy `/wordpress-installs` REST routes stay
mounted alongside the new `/applications` routes for one release.
The UI is updated to use `/applications` (and the matching
`applications` Refine resource) immediately. The API list/get/delete/
clone handlers under `/applications` delegate to the same
`wordPressHandler` methods because the row shape is identical;
only the create handler is new generic code.

**Rationale:**
- A user with a stale UI bundle (CDN cache, in-flight tab) keeps
  working against `/wordpress-installs` while the new modal POSTs
  to `/applications`. Removing the legacy path would 404 those
  stragglers.
- The two surfaces share the same database table and the same
  install-kicker/delete-chain functions. There's no second code
  path to maintain — the parallel routes are just two front doors
  to the same hallway.

**Consequences:**
- M19.1 deletes `/wordpress-installs` registration in `app.go` and
  the `WordPressHandlerConfig` type alias. Until then, both
  surfaces are documented as supported.
- Anyone looking at the route list will see `/wordpress-installs`
  next to `/applications` and might assume they're competing — the
  M19.1 cleanup is the single way to remove that confusion.

---

### 7. PHP-only for v1; Node and Python deferred

**Decision:** Every app shipped in M19 (and in the M19 catalog plan)
is PHP. The descriptor has no `Runtime` field. When the first
non-PHP app proposal lands, add `Runtime` to `App` and require every
existing descriptor to set it (panel-side validation forces the
update; no migration needed because the field lives in code).

**Rationale:**
- Adding `Runtime` now would be design-by-speculation. Without a
  real Node app proposal, the field's enum and the agent runtime
  story (where does Node live? How are versions selected? Per-user
  slices?) are unanswerable.
- The framework is open to extension at the descriptor + agent
  installer pair; the Runtime question can be answered when it
  actually has a customer.

**Consequences:**
- M20 (Node apps) will be a follow-on milestone with its own
  agent-side runtime story (PM2/systemd/Bun etc.) and its own ADR.
- The current code does NOT reject a hypothetical Node descriptor —
  it would install fine for the panel; the agent installer would
  be the one to decide what to actually run.

---

## Status

Steps 1–5 + 8 shipped on the `m19/*` branch stack (commits
`733d6b8` → `eb94b31`). Steps 6 (DokuWiki) and 7 (MediaWiki) are
gated on the stack being merged + deployed plus a UI follow-up
that renders per-app fields from the descriptor schema.

## Cross-references

- **ADR-0026** — M10 WordPress; superseded by M19 for the
  `wordpress_installs` table and the WP-specific routes.
- **plans/m19-applications-framework.md** — the eight-step
  construction plan + the adversarial review changelog.
- **panel-api/internal/agent/testdata/app_\*.json** — the six
  cross-boundary contract fixtures + the round-trip tests in
  `panel-api/internal/agent/app_contract_test.go`.
- **feedback_cross_boundary_contracts** — the M10 lesson that drove
  the contract-test layout.
