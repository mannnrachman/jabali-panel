> **PARKED 2026-04-17.** Decision: yak-shave risk too high (rewriting
> ~5,700 LOC of working UI to avoid ~400 LOC of new AntD code for M9
> step 8). Ship M9 step 8 + step 9 + PMA SSO in AntD first. Revisit
> migration as a background initiative after M6 email ships — or kill
> entirely if the existing AntD UI isn't blocking a shipped feature.
> No tasks are active for this plan; unpark by reinstating tasks
> 154–160 (plus new 161/162 for steps 8 and 9).

# Plan: panel-ui migration — Refine + AntD → shadcn/ui + TanStack

**Objective.** Replace Refine + Ant Design in `panel-ui/` with shadcn/ui,
TanStack Query (kept), TanStack Table, react-hook-form, zod, and sonner.
Shrink bundle size, remove a layer of framework indirection, gain design
control. No feature regressions, no backend changes.

**Mode.** Direct (Gitea remote, no `gh`). One commit per step on `main`.

**Sequencing.** Steps 1→2→3→4→5 strictly sequential. After step 5,
steps 6 and 7 are file-disjoint across `src/shells/user/` vs
`src/shells/admin/` and can parallelize (either sequential locally, or
via `isolation: "worktree"` if genuinely concurrent). Steps 8 and 9
run after both 6 and 7 land.

**Invariants.** After every step:
- `cd panel-ui && npx tsc -b && npx vite build` passes.
- `cd panel-ui && npm test` (vitest) passes.
- `cd panel-ui && npm run lint` stays green.
- Critical flows work manually in dev: login, admin creates a user, user
  lists their domains. (Formal Playwright update is step 9.)
- No user-facing regression — every page that worked before the step
  still works after it.
- Bundle size does not *grow* meaningfully between steps (the endgame
  is a shrink; interim steps can hold flat).

---

## Shape of the current codebase (facts you need before planning)

- **30 page components** — 20 under `src/shells/admin/`, 10 under
  `src/shells/user/`. 7 shared components (`src/components/`).
- **42 files import `antd`, 41 import `@refinedev/*`** — overlap is
  heavy but not total; AntD is the wider dependency.
- **Custom `dataProvider.ts` (118 LOC)** — already bypasses
  `@refinedev/simple-rest` because the API envelope doesn't match.
  Means the Refine data layer isn't doing much for us here.
- **Custom `authProvider.ts` (117 LOC)** — login, logout, check,
  identity. No access-control surface (the two-shell design handles
  admin vs user segregation via separate URL trees).
- **App.tsx (298 LOC)** — single `<Refine>` root wraps everything.
  Resources list declares admin + user resources with
  `meta: { shell }` filtering. `routerProvider` from
  `@refinedev/react-router` adapts react-router v7.
- **No Tailwind today.** shadcn requires Tailwind + PostCSS setup.
- **Vitest for unit, Playwright for E2E.** Playwright selectors
  currently target AntD class names; those need to change.

## Design decisions (committed in step 1)

ADR-0024 will record these. The plan assumes them.

1. **Drop Refine entirely.** Refine's value is thin here: the
   dataProvider is custom, the authProvider is custom, router is
   react-router v7, and the two-shell layout doesn't need Refine's
   resource abstraction. Keeping Refine + shadcn is a viable Plan B
   but duplicates mental models.
2. **Adopt full shadcn stack:** shadcn/ui components, Tailwind CSS
   with CSS variables for theming, sonner for notifications,
   react-hook-form + zod for forms, TanStack Table for sorted /
   paginated tables.
3. **Keep TanStack Query.** It's already the transport underneath
   Refine's hooks; we remove the wrapper, not the library. Hooks
   become `useDomains()` / `useCreateDomain()` etc., thin wrappers
   over `useQuery` / `useMutation` + the existing axios `apiClient`
   (which handles the refresh-token dance).
4. **Tailwind + shadcn coexist with AntD during migration.** Both
   render into the same DOM; shadcn is CSS-variables and Tailwind
   classes, AntD is its own runtime. No collision if we scope
   Tailwind's preflight. We keep both deps installed until step 8.
5. **One shell first: user.** User shell is smaller (10 pages) and
   lower-stakes. Land it in shadcn end-to-end; admin follows.
6. **No Refine router provider.** react-router v7 is already the
   actual router; Refine's `routerProvider` is an adapter we remove
   in step 4 when Refine goes.
7. **Forms: react-hook-form + zod.** Inline validation errors via
   shadcn's `<Form>` + `<FormField>` + `<FormMessage>`. Matches the
   AntD Form.Item UX users already have.
8. **Tables: TanStack Table + shadcn `<Table>`.** Pagination,
   sorting, column filtering baked in. Server-side pagination stays
   on the Go API; the table is the render layer.
9. **Notifications: sonner.** Single `<Toaster>` at the root. Replace
   `message.success`/`message.error` calls with `toast.success` /
   `toast.error`. This means dropping `@refinedev/antd`'s
   `useNotificationProvider` bridge.
10. **M9 step 8 (PHP pool UI) becomes the first purely-shadcn page.**
    Block M9 step 8 on this migration completing through step 5
    (pilot). Then the PHP pool admin pages are written in shadcn
    directly, skipping the doomed AntD version. **Explicit
    constraint** (review finding M7): M9 step 8 MUST use the new
    TanStack hooks from this plan's step 3 (e.g.
    `src/api/hooks/usePHPPools.ts`) — not Refine's `useList`, even
    if that's tempting for consistency with still-unmigrated admin
    pages. Using Refine for PHP pool UI would block step 8 of this
    plan (drop Refine) until the PHP pool page also migrates, which
    defeats the "first shadcn page" point.
11. **English only** — ADR-0007 stands; no i18n layer.
12. **Two shells stay.** `/jabali-admin/*` and `/jabali-panel/*` URL
    segregation is a security boundary (ADR-0013 / admin impersonation
    flow), not a styling choice. shadcn sidebars replace Refine's
    `ThemedSiderV2` but the URL shape is unchanged.

---

## Step 1 — ADR-0024 + migration principles

**Parallel:** no (blocks everything).
**Model tier:** strongest.
**Agent:** `adr-architect`.
**Est. complexity:** LOW.

### Context brief

The codebase ships one `Refine + AntD` shell with a custom dataProvider
and authProvider. Refine's abstractions have not paid for themselves
given how much glue we already hand-wrote. The migration target is the
shadcn/ui + Tailwind stack, with TanStack Query kept and TanStack Table
added. This ADR locks the 12 decisions in the plan's "Design decisions"
section so step-2+ implementers don't relitigate scope.

### Tasks

1. Read `docs/adr/0007-english-only-no-i18n.md`,
   `docs/adr/0012-refine-antd-tanstack.md` (if present — if not,
   ADR-0024 should reference whatever ADR captured the original
   Refine+AntD choice and supersede it).
2. Create `docs/adr/0024-panel-ui-shadcn-migration.md` with all 12
   decisions as subsections (Decision / Alternatives Considered /
   Consequences), MADR 3.0 format like ADR-0022 and ADR-0023.
3. Update `docs/adr/README.md` — add ADR-0024 row, mark any
   superseded ADR accordingly.
4. Update `docs/BLUEPRINT.md`:
   - Add "UI migration to shadcn/ui" as a new milestone row (or list
     it as in-flight infrastructure work, matching the existing style).
   - Note that M9 step 8 (PHP pool UI) is now gated on this migration
     reaching step 5 (pilot page proves the stack).
5. Also update `plans/m9-php-fpm-pool-manager.md` step 8 with a
   banner: "Depends on panel-ui shadcn migration step 5. See
   plans/panel-ui-migration.md."

### Verification

- `test -f docs/adr/0024-panel-ui-shadcn-migration.md`
- `grep -c "### " docs/adr/0024-panel-ui-shadcn-migration.md` ≥ 12
- `grep -q "0024" docs/adr/README.md`
- `grep -q "panel-ui-migration" plans/m9-php-fpm-pool-manager.md`

### Exit criteria

- ADR-0024 exists, all 12 decisions recorded with alternatives.
- Cross-doc links updated.
- Commit: `docs(adr): ADR-0024 panel-ui migration to shadcn + TanStack`.

---

## Step 2 — Tailwind + shadcn bootstrap + sonner

**Parallel:** no (blocks 3, 4).
**Model tier:** default.
**Agent:** `mobile-dev` (React/UI specialist).
**Est. complexity:** MEDIUM.

### Context brief

Add Tailwind CSS 4 (or 3.4 if v4 has stability concerns at plan time),
PostCSS, shadcn/ui CLI, and `components.json`. Initialize the shadcn
registry. Add sonner as the toast library. Do not migrate any page
yet — this is pure scaffolding that must ride alongside existing AntD
without visual conflict.

Key constraint: shadcn's default Tailwind config enables `preflight`,
which resets every `<h1>`, `<button>`, etc. AntD expects its own
baseline styles. Two options: (a) scope Tailwind preflight to shadcn
components only via CSS layer order, (b) disable preflight entirely
and live with slightly less consistent shadcn components during
transition. Go with (a): `@layer base` reset + explicit `.shadcn`
scope if needed.

### Tasks

1. Install deps: `tailwindcss@^4 postcss autoprefixer class-variance-authority clsx tailwind-merge lucide-react sonner`
   (exact versions via latest at implementation time — check the
   shadcn docs for current supported combo).
2. Run `npx shadcn@latest init` with:
   - Style: `new-york`
   - Base color: `slate` (neutral, easy to brand later)
   - CSS variables: yes
   - Path aliases: match existing tsconfig (`@/*` → `src/*`)
3. Commit `components.json`, `tailwind.config.ts` (or
   `tailwind.config.js`), `postcss.config.js`, `src/index.css` with
   the CSS variables block.
4. Scope Tailwind preflight so it doesn't break AntD: set
   `corePlugins.preflight = false` and add a minimal hand-rolled
   preflight scoped under a `.shadcn-root` class, OR keep preflight
   enabled and verify AntD still renders correctly (test every page
   visually, document findings in the commit).
5. Add `<Toaster>` from sonner in `App.tsx` (below the existing
   `<Refine>` root). Replace zero calls yet — just wire the
   container.
6. Add ONE shadcn component as a smoke test: `npx shadcn@latest add
   button`. Import `Button` from `@/components/ui/button` in a new
   file `src/shadcn-smoke.tsx` (don't wire it into any route yet).
   Confirm the build is clean.

### Verification

- `cd panel-ui && npx tsc -b && npx vite build` passes.
- `cd panel-ui && npm run lint` passes.
- `grep -q "tailwindcss" panel-ui/package.json`
- `test -f panel-ui/components.json`
- `test -f panel-ui/src/components/ui/button.tsx`
- Dev server (`npm run dev`) shows every existing AntD page rendering
  unchanged — no preflight breakage. Manual smoke.

### Exit criteria

- Tailwind builds; shadcn primitives available via `@/components/ui/*`;
  sonner toaster mounted; no visual regression on AntD pages.
- Commit: `feat(ui): bootstrap Tailwind 4 + shadcn/ui + sonner`.

---

## Step 3 — Data layer: typed TanStack Query hooks

**Parallel:** yes, with step 4 after step 2.
**Model tier:** default.
**Agent:** `mobile-dev`.
**Est. complexity:** MEDIUM.

### Context brief

Refine's `useList` / `useOne` / `useCreate` / `useUpdate` / `useDelete`
hooks wrap TanStack Query plus the dataProvider. To drop Refine, we
need equivalent thin hooks that call our axios `apiClient` directly
and expose `useQuery` / `useMutation` results. Keep the same React
Query cache semantics (staleTime, invalidation keys) so migrating a
page changes only the import, not the caching behavior.

### Tasks

1. Create `src/api/keys.ts` — query-key factory:
   ```ts
   export const qk = {
     domains:        ['domains'] as const,
     domain:         (id: string) => ['domains', id] as const,
     databases:      ['databases'] as const,
     database:       (id: string) => ['databases', id] as const,
     // ... one entry per resource
   };
   ```
2. Create `src/api/hooks/` with a hook per resource:
   `useDomains.ts`, `useDatabases.ts`, `useDatabaseUsers.ts`,
   `useDNSZones.ts`, `useDNSRecords.ts`, `useUsers.ts`,
   `usePackages.ts`, `useSSL.ts`, `useServerSettings.ts`,
   `usePHPPools.ts` (new for M9 step 8).
3. Each file exports:
   - `useXList(opts)` → `useQuery({ queryKey: qk.xs, queryFn: () =>
     apiClient.get('/xs', { params: opts }).then(unwrap) })`
   - `useX(id)` → `useQuery({ queryKey: qk.x(id), ... })`
   - `useCreateX()` → `useMutation({ mutationFn: (body) =>
     apiClient.post('/xs', body).then(unwrap), onSuccess: ()
     => queryClient.invalidateQueries({ queryKey: qk.xs }) })`
   - `useUpdateX(id)` and `useDeleteX()` similarly.
4. TypeScript types from existing models. If models are duplicated in
   the panel-ui codebase, consolidate into `src/api/types.ts`.
5. Write one pilot test in `src/api/hooks/useDomains.test.ts` with
   msw or a manual `vi.mock` of apiClient — prove the hook wiring
   works end-to-end in vitest.
6. **Dual-cache transition policy** (review finding C3): during
   steps 3–7, Refine's internal query cache and these new
   TanStack hooks live side-by-side. They do NOT share cache keys.
   A mutation in a shadcn-migrated page will NOT invalidate a
   still-Refine page's list view, and vice versa. This is acceptable
   because pages migrate wholesale (no mixed pages), but two
   constraints follow:
   - When migrating a page (steps 5–7), ALL components that render
     that resource's data must flip together. No leaving a
     `<DomainCountBadge>` that reads through Refine while the list
     renders through the new hooks — the badge will go stale after
     a shadcn-triggered mutation.
   - Resource hooks in this step use `qk.<name>` query keys only;
     never construct keys that match Refine's internal format
     (`['default', resource, ...]`). Keeping the namespaces
     disjoint prevents accidental cache collisions.

### Verification

- `cd panel-ui && npx tsc -b && npm test -- --run api/hooks`
- No page imports these hooks yet (grep
  `grep -r "from '@/api/hooks'" src/shells` must return 0 matches).
- Hook count: at least one per endpoint the backend exposes.

### Exit criteria

- All endpoints covered by typed hooks; pilot test green; zero
  runtime integration with existing pages (they still use Refine).
- Commit: `feat(ui): TanStack Query hooks per API resource`.

---

## Step 4 — Auth + router unbundle

**Parallel:** yes, with step 3 after step 2.
**Model tier:** default.
**Agent:** `mobile-dev`.
**Est. complexity:** MEDIUM.

### Context brief

Refine currently wraps react-router via `@refinedev/react-router`
and uses `<Authenticated>` + `authProvider` for gated routes. We
replace both with plain react-router v7 + a custom `useAuth` hook
and `<ProtectedRoute>` wrapper. Refine itself stays in App.tsx for
this step — just without the router/auth adapters. Drop them in
step 8 when every page is migrated.

### Tasks

1. Port `authProvider.ts` behavior to
   `src/auth/useAuth.ts` + `src/auth/AuthContext.tsx`:
   - `useAuth()` returns `{ user, isAdmin, login, logout,
     isLoading, error }`.
   - Access token lives in memory (as today); refresh cookie handled
     by apiClient (as today).
   - `login(email, password)` calls `apiClient.post('/auth/login')`
     and sets state.
   - `logout()` hits `/auth/logout` and clears state.
   - Initial `check()` on mount uses the existing
     `/auth/refresh` → `/users/me` sequence.
   - **Refresh reconciliation** (review finding C1): apiClient today
     silently refreshes on 401. After refresh, the auth CONTEXT must
     learn the session is still good. Wire a small event: apiClient
     emits `auth:refreshed` when it rotates a token; AuthContext
     subscribes and re-runs `/users/me` once per refresh to pick up
     any fresh claims (e.g., impersonation changes). Without this,
     a user who sits idle past `JWT_ACCESS_TTL` (15 min) will silently
     succeed on their next API call but their React state may be stale
     for claims-dependent UI (admin toggle, impersonation banner).
     Add one test that covers this path.
2. Create `src/auth/ProtectedRoute.tsx`:
   ```tsx
   export function ProtectedRoute({ requireAdmin = false, children }) {
     const { user, isLoading } = useAuth();
     if (isLoading) return <Spinner />;
     if (!user) return <Navigate to="/login" replace />;
     if (requireAdmin && !user.is_admin) return <Navigate to="/jabali-panel" replace />;
     return children;
   }
   ```
3. In `App.tsx`:
   - Remove `routerProvider={routerProvider}` from `<Refine>`.
   - Remove `@refinedev/react-router` imports
     (`CatchAllNavigate`, `DocumentTitleHandler`,
     `UnsavedChangesNotifier`).
   - Wrap the routes in plain `<BrowserRouter><Routes>…</Routes></BrowserRouter>`
     with `<ProtectedRoute>` instead of `<Authenticated>`.
   - Leave the `<Refine>` root in place with its dataProvider +
     resources list — pages still use Refine hooks at this step.
4. Migrate `LoginPage` off `@refinedev/*` hooks (it probably uses
   `useLogin`). Replace with the new `useAuth().login`.
5. Update `notificationProvider`: remove
   `useNotificationProvider` from `@refinedev/antd`. Pass a custom
   provider that calls `toast.success/toast.error` under the hood
   so Refine's internal notifications (e.g., from form hooks)
   still surface via sonner during the transition.

### Verification

- `cd panel-ui && npx tsc -b && npx vite build` passes.
- Manual: login flow works; protected routes redirect to `/login`
  when logged out; admin/user shell gating unchanged.
- `grep -r "@refinedev/react-router" src/` returns zero matches
  (the router adapter is fully removed).
- `grep -r "@refinedev/antd" src/` returns only the notification
  bridge import location (or zero, if you migrated it cleanly).

### Exit criteria

- Login works via `useAuth`. Routing is pure react-router. Refine
  still handles resource hooks but not auth or routing.
- Commit: `refactor(ui): drop Refine router + antd notifications`.

---

## Step 5 — Pilot: migrate `user/MyProfile.tsx` to shadcn end-to-end

**Parallel:** no (blocks 6, 7).
**Model tier:** default.
**Agent:** `mobile-dev` → `typescript-reviewer`.
**Est. complexity:** MEDIUM.

### Context brief

`MyProfile.tsx` is the smallest user-shell page — a form for the
logged-in user's own profile. Rewrite it using the full target
stack: shadcn `<Card>`, `<Form>` + `<FormField>`, react-hook-form
+ zod schema, `useAuth()` for identity, TanStack Query mutation via
`useUpdateMe()`, sonner for success/error toast. Prove the stack
works end-to-end on a real page. If anything doesn't feel right,
fix it here before multiplying the mistake across 29 more pages.

### Tasks

1. Add shadcn components the page needs:
   `npx shadcn@latest add form input label card button`. May also
   need `@/components/ui/password-input` — build as a custom variant
   if shadcn doesn't have one native.
2. Write `src/api/hooks/useMe.ts` — `useMe()` + `useUpdateMe()`.
3. Define zod schema: `const profileSchema = z.object({
   first_name: z.string().min(1), last_name: z.string().min(1), ...
   })`.
4. Rewrite `src/shells/user/MyProfile.tsx` using the new primitives.
   Delete all `antd` and `@refinedev` imports from this file.
   - **Validation UX parity** (review finding C2): AntD validated on
     change by default; react-hook-form defaults to `onSubmit`. If
     the pilot ships with RHF defaults, users type an invalid email
     and see no feedback until they click Save — a regression in
     immediacy. Use
     `useForm({ mode: "onChange", resolver: zodResolver(profileSchema) })`
     and render `<FormMessage />` under every field. Pin this as
     the house default in ADR-0024's "Forms" decision so the rest
     of the migration is consistent.
5. Update the page's sidebar/navigation entry in `App.tsx` resources
   list so Refine knows this is a "manual" page (keep it registered
   for the sidebar icon/label, but stop treating it as a Refine
   resource that needs CRUD routing).
6. Add a vitest unit test:
   `src/shells/user/MyProfile.test.tsx` — renders, form submits,
   toast appears on success.

### Verification

- `cd panel-ui && npx tsc -b && npm test -- --run shells/user/MyProfile`
- Manual: login as a regular user, load
  `/jabali-panel/me` (or whatever the path is), edit profile, save,
  see a toast, see the server update.
- File has zero `antd` or `@refinedev` imports.
- Bundle delta: should be mildly negative or flat (losing AntD imports
  for this page, gaining shadcn + RHF).

### Exit criteria

- `MyProfile` works in shadcn, proves the stack, pilot complete.
- Commit: `feat(ui): pilot — migrate MyProfile to shadcn + RHF + zod`.

---

## Step 6 — User shell: layout chrome + remaining user pages

**Parallel:** yes, with step 7 after step 5 (file-disjoint: `src/shells/user/` vs `src/shells/admin/`).
**Model tier:** default.
**Agent:** `mobile-dev` → `typescript-reviewer`.
**Est. complexity:** HIGH.

### Context brief

Rewrite `UserLayout.tsx` with shadcn sidebar-07 block (responsive
sidebar + top header + user menu). Port the 9 remaining user pages
off AntD:

- `user/domains/` — list, create, edit, DNSRecordsPage, redirects, nginxrules, ssl (if per-domain)
- `user/databases/` — list, create, grants modal
- `user/database-users/` — list, create, password rotation, add-grant modal
- `user/dns/` — records management
- `user/ssl/` — per-domain SSL toggle

Each page follows the pilot's pattern: shadcn primitives + RHF/zod +
TanStack Query hooks from step 3 + sonner.

### Tasks

1. `npx shadcn@latest add sidebar navigation-menu sheet dropdown-menu avatar`.
2. Add any further shadcn components needed: `table`, `dialog`,
   `select`, `checkbox`, `radio-group`, `tabs`, `alert`, `badge`.
   - **Accessibility parity** (review finding H5): shadcn `<Table>`
     is headless — it's `<table>` + semantic child elements but
     with no built-in ARIA labeling, no keyboard nav beyond native
     tab order. AntD Table ships WAI-ARIA for sort controls,
     pagination, row selection. Each migrated list page must:
     - Wrap `<Table>` with `aria-label="<resource-name> list"`.
     - If using TanStack Table with custom sort buttons, render them
       as `<button aria-sort="ascending|descending|none">`.
     - For pagination, reuse shadcn's `Pagination` block which is
       Radix-backed and accessible.
     - Before marking a page done, run `axe-core` (or the browser
       devtools Accessibility audit) on the page — no Critical
       or Serious findings on the data table.
   - **Search-and-replace hazards** (review finding M8):
     `<Button loading={…} danger>` pattern appears in ~12 places.
     shadcn Button has neither prop. Rewrites:
     - `loading` → render `<Loader2 className="animate-spin" />`
       next to label, set `disabled` when loading.
     - `danger` → `variant="destructive"`.
     - `icon={<X/>}` → place the Lucide icon as a child.
     Grep `loading=\|danger=\|icon=` across migrated files before
     declaring a page done.
3. Rewrite `src/shells/UserLayout.tsx` with the sidebar block.
   Preserve current routes and nav items; replace the Refine
   `<Menu>` + `<ThemedSiderV2>` with shadcn's
   `<Sidebar>`/`<SidebarProvider>` API.
4. For each user page, in this order (smallest blast radius first):
   a. `UserDatabaseList` + create
   b. `DatabaseUsersList` + create + AddGrantModal
   c. `UserDomainList` + create
   d. `DNSRecordsPage`
   e. Domain redirects + nginxrules + ssl (these are sub-pages of
      a domain — migrate as one unit)
5. Delete AntD imports from every file touched. Leave AntD in the
   admin shell for now.
6. Add one vitest smoke test per page: mounts, shows loading state,
   renders data.

### Verification

- `cd panel-ui && npx tsc -b && npx vite build && npm run lint`.
- Manual: log in as a non-admin user, click through every user page.
  Everything renders and works.
- `grep -r "from 'antd'" src/shells/user/` returns zero matches.
- `grep -r "@refinedev/antd" src/shells/user/` returns zero matches.

### Exit criteria

- Every user page is shadcn; user shell chrome is shadcn; zero AntD
  imports remain anywhere under `src/shells/user/`.
- Commit: `feat(ui): migrate user shell to shadcn/ui`.

---

## Step 7 — Admin shell: layout chrome + remaining admin pages

**Parallel:** yes, with step 6 after step 5 (file-disjoint).
**Model tier:** default.
**Agent:** `mobile-dev` → `typescript-reviewer`.
**Est. complexity:** HIGH (may split).

### Context brief

Bigger than step 6: 19 admin pages across users, packages, domains,
DNS, SSL, settings, databases, database-users. Same target stack.
Same pattern as step 6.

If the PR feels too big (> ~1500 LOC diff), split this step into
7a / 7b / 7c after 7 lands the admin shell chrome + dashboard. Good
split points:
- 7a: admin chrome + users + packages (identity management)
- 7b: domains + DNS + SSL (networking)
- 7c: databases + database-users + settings (data + config)

### Tasks

1. Rewrite `src/shells/AdminLayout.tsx` in shadcn.
2. Rewrite `src/shells/admin/Dashboard.tsx`.
3. Migrate admin pages, smallest first:
   a. `admin/settings/ServerSettingsPage`
   b. `admin/packages/*` (list, create, edit)
   c. `admin/users/*` (list, create, edit, impersonate action)
   d. `admin/databases/*`
   e. `admin/database-users/*`
   f. `admin/domains/*`
   g. `admin/dns/*`
   h. `admin/ssl/*`
4. Remove all AntD imports from admin pages.
5. One vitest smoke test per page.

### Verification

- `cd panel-ui && npx tsc -b && npx vite build && npm run lint`.
- Manual: log in as admin, click through every admin page.
- `grep -r "from 'antd'" src/shells/admin/` returns zero matches.

### Exit criteria

- Every admin page is shadcn; admin shell chrome is shadcn; zero
  AntD imports under `src/shells/admin/`.
- Commit: `feat(ui): migrate admin shell to shadcn/ui`.

---

## Step 8 — Drop Refine + AntD dependencies

**Parallel:** no (needs 6 + 7).
**Model tier:** default.
**Agent:** `mobile-dev`.
**Est. complexity:** MEDIUM.

### Context brief

After steps 6 and 7, no page imports AntD or Refine. This step
removes the packages, strips the residual plumbing from App.tsx
(the `<Refine>` root, `<ConfigProvider>`, dataProvider passed to
Refine, resources list), and measures bundle win.

### Tasks

1. `npm uninstall antd @refinedev/antd @refinedev/core
   @refinedev/react-router @refinedev/simple-rest
   @dnd-kit/core @dnd-kit/sortable @dnd-kit/utilities @rc-component/util`.
   (dnd-kit and rc-component are AntD-adjacent — remove iff they
   have no other callers; verify via `grep`.)
2. Strip `<Refine>` + `<ConfigProvider>` from `App.tsx`. Routes
   become a pure `<BrowserRouter>` tree. Keep TanStack's
   `<QueryClientProvider>` at the top.
3. Delete `src/dataProvider.ts` (step 3's hooks replaced it).
4. Delete Refine-specific utilities: `authProvider.ts` may still
   export the raw API calls — refactor into `src/auth/api.ts` (the
   HTTP surface) + `src/auth/AuthContext.tsx` (the React surface).
5. Delete any leftover AntD theme tokens in CSS.
6. Compare bundle size before/after via
   `du -sh panel-ui/dist/assets/*.js`. Record in the commit message.
   Target is a **measurable reduction**, realistic ≥ 20% (review
   finding H4 pushed back on the original 30%): shadcn's Radix
   primitives + Tailwind's runtime cost + RHF + zod recover some
   of the AntD-and-Refine win. 20% is defensible; ≥ 30% is a
   stretch and should not be the success gate.

### Verification

- `cd panel-ui && npm ci && npx tsc -b && npx vite build` passes.
- `grep -r "from 'antd'\|@refinedev/" src/` returns zero matches.
- `du -sh panel-ui/dist/assets/*.js` — document before/after in the
  commit message. Target: ≥ 30% JS bundle reduction.

### Exit criteria

- Packages gone; build green; bundle smaller.
- Commit: `refactor(ui): drop Refine + AntD; shadcn-only`.

---

## Step 9 — Playwright selectors + E2E refresh

**Parallel:** no (final).
**Model tier:** default.
**Agent:** `e2e-runner`.
**Est. complexity:** MEDIUM.

### Context brief

Playwright tests under `panel-ui/tests/` target AntD class names
(`.ant-btn`, `.ant-form-item-control-input`, etc.) which no longer
exist. Migrate every selector to either `getByRole` + name, text
content, or `data-testid` where stable. This is the biggest
shadcn-transition gotcha — it's why step 9 exists as its own PR.

### Tasks

1. Audit every `.spec.ts` under `panel-ui/tests/` for AntD-specific
   selectors.
2. Replace with role/text selectors (preferred) or add
   `data-testid` attributes in the relevant shadcn components where
   role isn't sufficient.
3. Update `playwright.config.ts` if any project-level config
   assumes AntD presence (probably not, but verify).
4. Add a new E2E for the M9 PHP pool UI once M9 step 8 ships (after
   this plan completes, that page becomes the proving ground).
5. Run `cd panel-ui && npm run test:e2e` on a dev build; fix every
   flaky spec.

### Verification

- `cd panel-ui && npm run test:e2e` passes end-to-end with zero
  flakes on two consecutive runs.
- `grep -r "ant-" panel-ui/tests/` returns zero matches.

### Exit criteria

- E2E suite green against the new UI.
- Commit: `test(e2e): migrate Playwright selectors off AntD classes`.

---

## Dependency graph

```
1 ──► 2 ──┬─► 3 ─┐
          └─► 4 ─┴─► 5 ──┬─► 6 ─┐
                         └─► 7 ─┴─► 8 ──► 9
```

Parallel pairs: 3 ∥ 4 (after 2); 6 ∥ 7 (after 5).

## Rollback

- **Step 2** revert: uninstall Tailwind/shadcn/sonner; delete
  `components.json`, `tailwind.config.*`, `postcss.config.js`,
  `src/components/ui/`, `src/shadcn-smoke.tsx`. AntD untouched.
- **Step 3 / 4** revert: pure code revert; no state to clean.
- **Step 5** revert: restore the AntD `MyProfile.tsx` from git;
  keep infrastructure (steps 2–4) for future migration attempt.
- **Step 6 / 7** revert: restore the AntD pages from git. The shell
  chrome revert is the riskiest because users see it on every page.
  Prefer fix-forward rather than revert after 6 or 7 lands.
- **Step 8** revert: `npm install antd @refinedev/*` back and
  restore `dataProvider.ts` + `<Refine>` root. Only makes sense if
  some regression surfaces within days of the step shipping.
- **Step 9** revert: pure test-file revert; no prod impact.

## Review log

Plan reviewed by `security-architect` (Opus) on 2026-04-17.
Verdict: FIX-BEFORE-SHIP. Fixes folded in-line:

- **C1 auth refresh reconciliation** — step 4 now requires an
  `auth:refreshed` event from apiClient wired to AuthContext so the
  React state picks up rotated tokens instead of only learning about
  them via the next API error.
- **C2 form validation semantics shift** — step 5 pins
  `useForm({ mode: "onChange" })` as the house default; ADR-0024's
  Forms decision anchors this for the rest of the migration.
- **C3 dual-cache transition** — step 3 adds an explicit policy:
  shadcn-migrated pages flip wholesale (no hybrid Refine+shadcn
  within a page), and the two caches use disjoint key namespaces.
- **H4 bundle claim tempered** — success gate moved to ≥ 20%, with
  ≥ 30% framed as aspirational rather than contractual.
- **H5 accessibility parity** — step 6 + 7 require aria-labels on
  tables, `aria-sort` on sortable column headers, and an axe-core
  pass before marking a page done.
- **M7 M9-step-8 cache discipline** — the PHP pool UI must use the
  new hooks from step 3; Refine fallback is forbidden even during
  transition.
- **M8 Button prop rewrites** — step 6 adds a grep-and-rewrite
  checklist for `loading=`/`danger=`/`icon=` patterns before a
  page is declared done.

H6 coexistence risk is acknowledged by the plan's existing preflight
scoping (step 2) — not folded in as a new fix. M-class items not
listed above were consolidated into step 6/7 task language.

## Anti-patterns explicitly forbidden

- ❌ **Mixing shadcn and AntD inside the same page.** If a page
  migrates, it goes fully shadcn. No `<Button>` from shadcn next to
  `<Form.Item>` from AntD.
- ❌ **Running AntD and shadcn preflight both enabled.** Preflight
  collisions cause invisible breakage. Pick one ownership model
  and document it.
- ❌ **Breaking the two-shell URL structure.** `/jabali-admin/*`
  and `/jabali-panel/*` are a security boundary. No single-tree
  "let's unify them while we're here" refactor in this plan.
- ❌ **Dropping TanStack Query.** It's the data layer; it predates
  Refine. Only the Refine wrappers go.
- ❌ **Parallel agents writing to the shared main worktree for
  steps 6 and 7.** Past parallel runs produced real bugs. Use
  `isolation: "worktree"` per agent, or run sequentially.
- ❌ **Adding shadcn components you don't immediately use.** Each
  `npx shadcn add X` should be paired with the page that uses X in
  the same commit.
- ❌ **Hand-editing shadcn component source** unless you've first
  tried to compose them externally. The point of shadcn is that the
  source is yours, but forks still have upgrade cost.
- ❌ **Leaving Refine + shadcn both depended-on after step 8.**
  The bundle win is the point. Step 8 exists to pay it off.

## Success criteria for the whole plan

- `panel-ui/package.json` has zero `@refinedev/*` or `antd` deps.
- Production JS bundle shrinks by ≥ 20% relative to the
  pre-migration measurement taken at step 2's baseline. A higher
  reduction (30%+) is a bonus, not a contract — RHF + zod + Radix
  recover some of the AntD + Refine removal.
- Every critical user flow works (login, admin→user impersonation,
  create domain, create DB, rotate DB-user password, bind domain
  to PHP pool) — manually and via Playwright.
- Lighthouse accessibility score on the dashboard is ≥ 95 on both
  shells.
- M9 step 8 lands as the first purely-shadcn page, using the
  TanStack hooks from step 3 directly with no AntD fallback.
