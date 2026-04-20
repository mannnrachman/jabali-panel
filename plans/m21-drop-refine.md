# M21 — Drop Refine (keep AntD, Kratos, react-router)

**Status:** Blueprint — not yet dispatched. Wave A is dispatchable.
**Branch:** `m21/drop-refine` (wt-b's worktree, `/home/shuki/projects/jabali2-b/`).
**Goal:** Remove the `@refinedev/*` packages. Replace them with a mainstream 2026 stack: React + AntD + TanStack Query + react-router v7 + a hand-rolled URL-synced table hook. The panel stays behaviorally identical; the chrome (auto-breadcrumbs, sticky save bar, auto-heading, auto-card container) goes away and pages render as plain AntD compositions.

**Do NOT touch Kratos.** `Login.tsx`'s Kratos browser-flow handling, `kratos.ts`, `apiClient.ts`'s cookie mode, and the `/.ory/*` nginx proxy all stay unchanged. M20's auth model is load-bearing for M16 Hydra and is not part of this work.

**Non-goals:**
- Rewriting `Login.tsx` (Kratos flow renderer) — stays.
- Changing the HTTP transport — `apiClient.ts` + axios stay.
- Visual overhaul of individual pages beyond removing Refine chrome — if a page looks wrong after the wrapper is gone, that's a follow-up.
- Updating `docs/BLUEPRINT.md` milestones other than §M21.
- Touching panel-api / Go — this is frontend-only.

---

## 0. Design decisions

1. **TanStack Query (@tanstack/react-query) is the data layer.** Replaces `useList`, `useOne`, `useCreate`, `useUpdate`, `useDelete`, `useInvalidate`. Industry-standard; ~10kB gzipped; better cache invalidation than Refine; ships `QueryClientProvider` at the root once, every component `useQuery`/`useMutation` afterward.

2. **Keep axios + existing `apiClient.ts`.** TanStack Query wraps the same apiClient the current Refine `dataProvider` wraps. The `/api/v1/*` URLs, cookie-mode auth, and 401 redirect behavior do NOT change.

3. **Auth via React context, not Refine `authProvider`.** New `src/auth/AuthContext.tsx` exposes `{ user, isAdmin, isLoading, refresh, logout }` via `useAuth()`. The context lazily calls `/.ory/sessions/whoami` on mount and caches the result with a small manual cache (or a TanStack `useQuery` keyed `["whoami"]`). `logout()` hits `/.ory/self-service/logout/browser` then navigates to `/login` — same as the current `authProvider.logout()` does. Kratos cookies are still the source of truth; this is just the React adapter.

4. **Routing via react-router v7 directly.** Drop `routerProvider` + `resources` + `<Refine>` root. Routes live in `App.tsx` as plain `<Routes>`/`<Route>` JSX. Sidebar menu items live in `src/nav.ts` (admin items + user items + icons + labels) — one source of truth, no magic reflection off resources.

5. **Gate with `<RequireAuth>` + `<RequireAdmin>` components.** Replaces `<Authenticated>` + `<CanAccess>`. Each is ~15 lines: reads `useAuth()`, renders children on match, renders `<Navigate to="/login" />` or `<Navigate to="/jabali-panel" />` otherwise. The existing `RoleGate.tsx` pattern is preserved and may be renamed for clarity.

6. **No Refine page wrappers.** Delete `<Create>`, `<Edit>`, `<Show>`, `<List>` usage entirely. Each page file becomes:
   ```tsx
   export const UserCreate = () => {
     const [form] = Form.useForm();
     const createMutation = useCreate({ resource: "users" });
     const navigate = useNavigate();
     const onFinish = async (values) => {
       await createMutation.mutateAsync(values);
       navigate("/jabali-admin/users");
     };
     return (
       <Form form={form} onFinish={onFinish} layout="vertical">
         {/* existing Form.Item fields */}
         <Button type="primary" htmlType="submit" loading={createMutation.isPending}>
           Save
         </Button>
       </Form>
     );
   };
   ```
   No auto-heading, no auto-breadcrumb, no auto-card container. Page heading goes in the Layout's breadcrumb/header slot if wanted; for now pages render bare.

7. **Sticky save-bar only on long forms, via shared `<FormPageFooter>` component.** `<Create>`'s sticky save bar is a real UX win on the Package Edit page (8+ fields, long scroll). Rather than reintroducing it globally, ship a 30-line `<FormPageFooter>` that pages opt into:
   ```tsx
   <Form ...>
     <fields />
     <FormPageFooter>
       <Button type="primary" htmlType="submit">Save</Button>
     </FormPageFooter>
   </Form>
   ```
   Opt in on Package/Edit, Domain/Edit, ServerSettings. Skip it on short forms.

8. **`useTableURL` hand-rolled.** 100-line hook that round-trips `{ page, pageSize, q, sort, order }` to URL search params via `useSearchParams` and feeds them into a `useQuery`. Preserves bookmarking + back/forward navigation. Replaces `useTable` entirely. Signature:
   ```tsx
   const { data, total, isLoading, params, setParams } = useTableURL({
     resource: "users",
     defaultSort: "created_at",
     defaultOrder: "desc",
   });
   ```
   `data` + `total` come from TanStack Query; `params` are the URL-bound state; `setParams` is an updater that writes back to URL search params. List pages wire `<Table>` pagination + `<Input>` search box + column sort against `params`/`setParams`.

9. **`useSelectQuery` hand-rolled.** 30-line hook replacing Refine's `useSelect`. Fetches a resource list once, maps to `{ label, value }` options for AntD `<Select>`. No pagination or filtering — if the list ever exceeds 100 items the call site switches to a search-as-you-type AntD `<Select showSearch>` with manual `onSearch` (future concern, not M21).

10. **Menu from `src/nav.ts`, not resources array.**
    ```ts
    // src/nav.ts
    export const adminNav: NavItem[] = [
      { key: "dashboard", label: "Dashboard", icon: <DashboardOutlined />, to: "/jabali-admin/dashboard" },
      { key: "users", label: "Users", icon: <TeamOutlined />, to: "/jabali-admin/users" },
      // ...
    ];
    export const userNav: NavItem[] = [ /* ... */ ];
    ```
    Layout components map this into AntD `<Menu>`. Highlighting the active item uses `useLocation()`.

11. **Notifications via AntD `message` / `notification`** — direct calls, no provider indirection. Replaces Refine's `useNotification`. The current `useNotification()` call-sites become `message.success(...)` / `message.error(...)`.

12. **Keep the existing `RoleGate.tsx` and `LandingRedirect.tsx`.** They're not Refine — they're custom. They sit on top of `useAuth()` instead of `useGetIdentity()`.

13. **Drop `@refinedev/simple-rest` — it's listed in deps but `dataProvider.ts` is a custom implementation that doesn't use it.** Gets removed in Wave E along with the other Refine packages.

14. **Testing strategy:**
    - `vitest` unit tests: must stay green. Most don't care about the routing/data wiring.
    - `playwright` E2E: several specs assert on `getByRole("heading", { name: /users/i })` or `/create user/i` — these currently resolve to Refine's auto-generated headings. After Wave B/C those headings come from the Layout breadcrumb or the Form's own header. Expect to update ~5–10 selectors across auth/users/packages/domains specs. Don't skip tests; update the assertions.
    - Full Playwright suite runs after Wave C and Wave D, and must be 18/18 green before the wave merges.

15. **Rollback:** git revert the M21 merge commits. No schema changes, no config changes, no new services — this is purely frontend code. The Refine packages re-install from `package-lock.json` if needed.

---

## 1. Waves / steps

| Wave | Step | Parallel? | Summary | Outputs |
|------|------|-----------|---------|---------|
| A | 1 | alone | TanStack Query install + root provider + shared data hooks | `package.json`, `package-lock.json`, `src/query.ts` (QueryClient singleton), `src/hooks/useQueries.ts` (`useListQuery`, `useOneQuery`, `useCreateMutation`, `useUpdateMutation`, `useDeleteMutation`) + tests |
| A | 2 | w/ 1 | AuthContext + RequireAuth/RequireAdmin | `src/auth/AuthContext.tsx` + `useAuth()` hook; `src/auth/RequireAuth.tsx` + `RequireAdmin.tsx` + tests |
| A | 3 | w/ 1, 2 | useTableURL + useSelectQuery | `src/hooks/useTableURL.ts`, `src/hooks/useSelectQuery.ts` + tests (Vitest — URL round-trip, default values, setParams patch) |
| B | 4 | gated on A | Shell rewrite: drop `<Refine>`, drop resources array, custom AdminLayout + UserLayout | `src/App.tsx` (rewritten root), `src/shells/AdminLayout.tsx`, `src/shells/UserLayout.tsx`, `src/nav.ts`, drop `ThemedLayoutV2` imports |
| C | 5 | w/ 6 | Admin List pages: users, packages, domains, SSL, DNS, server-settings, PHP-ext, applications — drop `<List>`, use `useTableURL` + plain `<Table>` | `src/shells/admin/*/List.tsx` (~8 files) |
| C | 6 | w/ 5 | Admin Create/Edit pages: users, packages, domains, DNS records — drop `<Create>`/`<Edit>`, use `Form.useForm()` + `useCreateMutation`/`useUpdateMutation` | `src/shells/admin/*/Create.tsx` + `Edit.tsx` (~10 files) |
| C | 7 | gated on 5+6 | Admin spec fixes: update E2E selectors in `users.spec.ts`, `packages.spec.ts` (if exists), `dashboard.spec.ts` | spec files touched |
| D | 8 | w/ 9 | User-shell List pages: databases, database-users, cron, DNS, SSH-keys, SSL, PHP settings, applications | `src/shells/user/*/List.tsx` (~8 files) |
| D | 9 | w/ 8 | User-shell Create/Edit pages: MyProfile, database create, cron create, DNS record create, SSH key add, etc. | `src/shells/user/*/Create.tsx` + `Edit.tsx` (~8 files) |
| D | 10 | gated on 8+9 | User spec fixes: `profile.spec.ts`, `cron.spec.ts`, `wordpress.spec.ts`, `sftp.spec.ts`, `php-extensions.spec.ts`, `limits.spec.ts`, `oauth2-wordpress.spec.ts` | spec files touched |
| E | 11 | gated on C+D | Remove Refine packages, update tsconfig paths if any, full suite run | `package.json` diff removing `@refinedev/*`, `package-lock.json` regenerated |
| E | 12 | w/ 11 | BLUEPRINT §M21 → SHIPPED, ADR-0037 → accepted, short note on `docs/PROJECT_TREE.md` if one exists | `docs/BLUEPRINT.md`, `docs/adr/0037-drop-refine.md` |

**Parallelism notes:** Wave A's three steps can run in parallel (all new files, no cross-dependencies). Wave C Step 5 and Step 6 can run in parallel too — List pages and Create/Edit pages don't share files. Same for Wave D. Wave B gates on Wave A (Layout uses `useAuth()`). Wave E gates on C+D (can't remove packages while pages still import from them).

---

## 2. Per-wave dispatch briefs

### Wave A Brief

**Goal:** Ship new foundation primitives without touching a single existing page. Everything is additive; main builds green the whole time.

**Step 1 — TanStack Query**
- `npm install @tanstack/react-query`
- Create `src/query.ts`:
  ```ts
  import { QueryClient } from "@tanstack/react-query";
  export const queryClient = new QueryClient({
    defaultOptions: {
      queries: {
        retry: 1,
        refetchOnWindowFocus: false,
        staleTime: 30_000,
      },
    },
  });
  ```
- Create `src/hooks/useQueries.ts` exposing:
  - `useListQuery<T>({ resource, params })` → `{ data: T[], total, isLoading, error }`
  - `useOneQuery<T>({ resource, id })` → `{ data: T, isLoading, error }`
  - `useCreateMutation<T, Input>({ resource })` → TanStack Query `useMutation` hitting `POST /api/v1/{resource}`; on success invalidates `["list", resource]`.
  - `useUpdateMutation<T, Input>({ resource })` → `PATCH /api/v1/{resource}/{id}`; invalidates both `["list", resource]` and `["one", resource, id]`.
  - `useDeleteMutation({ resource })` → `DELETE /api/v1/{resource}/{id}`; invalidates `["list", resource]`.
- All mutations return the TanStack Query mutation object; callers spread `mutateAsync`, `isPending`, `error`.
- Unit tests using `@testing-library/react` wrapping with `<QueryClientProvider>`.

**Step 2 — AuthContext**
- Create `src/auth/AuthContext.tsx`:
  ```ts
  export const AuthContext = createContext<{
    user: MeUser | null;
    isAdmin: boolean;
    isLoading: boolean;
    refresh: () => Promise<void>;
    logout: () => Promise<void>;
  } | null>(null);

  export const AuthProvider = ({ children }) => {
    const { data, isLoading, refetch } = useQuery({
      queryKey: ["whoami"],
      queryFn: async () => {
        const res = await axios.get("/.ory/sessions/whoami");
        return res.data.identity;  // or /api/v1/me — whichever the SPA currently uses
      },
      retry: false,
    });
    // ...
  };

  export const useAuth = () => {
    const ctx = useContext(AuthContext);
    if (!ctx) throw new Error("useAuth outside AuthProvider");
    return ctx;
  };
  ```
  Make sure to read `data.traits.is_admin` (per `fixtures.ts` line 115).
- `logout()` calls `/.ory/self-service/logout/browser`, follows the redirect, clears the query cache with `queryClient.clear()`, then `navigate("/login")`.
- Create `src/auth/RequireAuth.tsx`:
  ```ts
  export const RequireAuth = ({ children }) => {
    const { user, isLoading } = useAuth();
    if (isLoading) return <Spin />;
    if (!user) return <Navigate to="/login" replace />;
    return children;
  };
  ```
- Same shape for `RequireAdmin`, reading `isAdmin`.

**Step 3 — useTableURL + useSelectQuery**
- `src/hooks/useTableURL.ts`:
  ```ts
  export function useTableURL<T>({
    resource,
    defaultSort,
    defaultOrder = "desc",
  }: { resource: string; defaultSort?: string; defaultOrder?: "asc" | "desc" }) {
    const [searchParams, setSearchParams] = useSearchParams();
    const params = {
      page: Number(searchParams.get("page") ?? 1),
      pageSize: Number(searchParams.get("pageSize") ?? 20),
      q: searchParams.get("q") ?? "",
      sort: searchParams.get("sort") ?? defaultSort,
      order: searchParams.get("order") ?? defaultOrder,
    };
    const query = useListQuery<T>({ resource, params });
    const setParams = (patch: Partial<typeof params>) => {
      const next = new URLSearchParams(searchParams);
      for (const [k, v] of Object.entries(patch)) {
        if (v === undefined || v === "" || v === null) next.delete(k);
        else next.set(k, String(v));
      }
      setSearchParams(next);
    };
    return { ...query, params, setParams };
  }
  ```
- `src/hooks/useSelectQuery.ts` — 30 lines, fetches a resource list once via TanStack Query, returns `options: { label: string; value: string }[]`. Accepts `{ resource, labelField, valueField, extraParams? }`.
- Vitest tests covering: URL round-trip, default values kick in when search params absent, `setParams({ q: "" })` removes the key, back/forward navigation restores prior state.

**Exit criteria for Wave A:**
- `npm run test` green (Go + vitest)
- `npm run build` green
- No files under `src/shells/*` modified
- New exports available but unused — dead code until Wave B imports them. That's fine.

### Wave B Brief (gated on Wave A)

**Goal:** Replace the app shell without touching page contents yet.

- Rewrite `src/App.tsx`:
  - Remove `<Refine>` wrapper entirely
  - Remove `resources={[...]}`, `authProvider={...}`, `dataProvider={...}`, `routerProvider={...}`
  - Root becomes:
    ```tsx
    <QueryClientProvider client={queryClient}>
      <AuthProvider>
        <BrowserRouter>
          <ConfigProvider theme={...}>
            <Routes>
              <Route path="/login" element={<Login />} />
              <Route path="/jabali-admin/*" element={
                <RequireAdmin><AdminLayout /></RequireAdmin>
              } />
              <Route path="/jabali-panel/*" element={
                <RequireAuth><UserLayout /></RequireAuth>
              } />
              <Route path="/" element={<LandingRedirect />} />
              <Route path="*" element={<Navigate to="/" />} />
            </Routes>
          </ConfigProvider>
        </BrowserRouter>
      </AuthProvider>
    </QueryClientProvider>
    ```
  - Nested routes under `/jabali-admin/*` and `/jabali-panel/*` live inside the Layout components' `<Outlet>`.

- `src/nav.ts` — single source for menu items.

- `src/shells/AdminLayout.tsx`: AntD `<Layout>` + `<Sider>` (menu from `adminNav`) + `<Header>` (Jabali wordmark, user avatar, search input, theme toggle) + `<Content>` (renders `<Outlet />`). Pure composition — no magic.

- `src/shells/UserLayout.tsx`: same shape with `userNav`.

- Update existing `JabaliHeader.tsx`, `JabaliSider.tsx` if present — merge their rendering into the new Layouts, don't reference `ThemedLayoutV2`.

- Drop `@refinedev/antd` imports for `ThemedLayoutV2`, `ThemedSiderV2`.

**Exit criteria for Wave B:**
- `npm run build` green
- SPA boots end-to-end in `npm run preview`: can log in, can reach `/jabali-admin/users` (list may render blank / error if page still imports old `<List>` — that's fine, we fix in Wave C)
- vitest stays green
- Playwright may have failures — don't fix here, fix per-page in C/D

### Wave C Brief — Admin pages (gated on B)

**Goal:** Mechanical rewrite of every admin page file. One pattern per page type.

For each **List page** (e.g., `UserList.tsx`):
- Replace `useTable()` call with `useTableURL({ resource: "users" })`
- Replace `<List>` wrapper with a plain `<div>` that renders `<Table>` + the search input + the Create button
- The `<Table>` gets `dataSource={data}`, `loading={isLoading}`, `pagination={{ current: params.page, pageSize: params.pageSize, total, onChange: (page, pageSize) => setParams({ page, pageSize }) }}`
- Replace `<CreateButton>` with `<Button type="primary" onClick={() => navigate("/jabali-admin/users/create")}>Create</Button>`
- Replace `<EditButton>`/`<DeleteButton>` usage in row actions with plain `<Button>`s

For each **Create page** (e.g., `UserCreate.tsx`):
- Replace `useForm({ resource, action: "create" })` with `Form.useForm()` + `useCreateMutation({ resource })`
- Replace `<Create saveButtonProps={saveButtonProps}>` wrapper with plain `<Form layout="vertical" onFinish={...}>`
- `onFinish` calls `createMutation.mutateAsync(values)` then `navigate("/jabali-admin/users")`
- Submit button: `<Button type="primary" htmlType="submit" loading={createMutation.isPending}>Save</Button>` at the end of the form
- Opt-in sticky footer via `<FormPageFooter>` if the form is long

For each **Edit page** (e.g., `UserEdit.tsx`):
- `useOneQuery({ resource, id: params.id })` to fetch current record, set `initialValues={data}` on Form once loaded
- `useUpdateMutation({ resource })` for submit
- Same onFinish pattern: `await updateMutation.mutateAsync({ id, values }); navigate(...)`

**Page-by-page inventory** (wt-b runs `grep -l "@refinedev/antd" src/shells/admin/` to confirm):
- users/{UserList, UserCreate, UserEdit}
- packages/{PackageList, PackageCreate, PackageEdit}
- domains/{DomainList, DomainCreate, DomainEdit}
- dns/* (zones + records)
- ssl/* (SSLManagerPage + children)
- settings/ServerSettingsPage
- php/{VersionsTab, PHPExtensionsTab}
- applications/* (applications list, install modal) — IMPORTANT: apps framework is wt-a's territory; check with wt-a before touching if they're mid-edit.

**Exit criteria for Wave C:**
- `npm run test` green
- `npm run build` green
- All admin E2E specs green: `users.spec.ts`, `dashboard.spec.ts`, `auth.spec.ts`, `limits.spec.ts`, `php-extensions.spec.ts`
- Admin shell still logs in + navigates cleanly when manually tested

### Wave D Brief — User pages (gated on C)

Same mechanical rewrite for user-shell pages. Inventory:
- databases/{UserDatabaseList, UserDatabaseCreate}
- database-users/{UserDatabaseUsersList, UserDatabaseUserCreate}
- cron/* (UserCronList, create)
- dns/UserDNSZonesOverviewPage
- ssh-keys/UserSSHKeysPage
- ssl/UserSSLManagerPage
- php-settings/UserPHPSettingsPage
- applications/* — coordinate with wt-a
- MyProfile.tsx (not a CRUD page — special case, it's already close to plain AntD, just needs `useAuth()` plumbing)

**Exit criteria for Wave D:**
- Full suite green
- User-shell navigation smoke-tested manually

### Wave E Brief — cleanup (gated on C+D)

- Remove `@refinedev/antd`, `@refinedev/core`, `@refinedev/react-router`, `@refinedev/simple-rest` from `package.json`
- `npm install` (regenerate lockfile)
- `grep -r "@refinedev" src/ tests/` must come back empty. Any hit → unfinished migration, block Wave E.
- Full `make test-all` green
- Update `docs/BLUEPRINT.md` §M21 to SHIPPED
- Update `docs/adr/0037-drop-refine.md` status `proposed` → `accepted`

---

## 3. Contract tests that stay true

These assertions MUST still pass end-to-end through the rewrite. wt-b should run them locally after each wave as a smoke check:

- `GET /api/v1/users?page=1&pageSize=20&q=admin` returns paginated envelope (wire contract unchanged)
- Bookmarking `/jabali-admin/users?page=2&q=test` restores both page and search on reload
- Logging out via sider avatar dropdown hits `/.ory/self-service/logout/browser`
- Admin visiting `/jabali-panel` redirects to `/jabali-admin`; non-admin visiting `/jabali-admin` redirects to `/jabali-panel` (RoleGate behavior preserved)
- 401 whoami triggers redirect to `/login` (auth gate)
- The three test personas in `fixtures.ts` (`admin`, `user`) continue to sign in and land correctly
- Kratos mock routes (`/.ory/*`) in `fixtures.ts` keep working unchanged

---

## 4. Dispatcher coordination

- Branch: `m21/drop-refine` on wt-b (`/home/shuki/projects/jabali2-b/`)
- Merge model: each wave lands on main via fast-forward from wt-b's branch. Run `~/projects/merge.sh` after each wave's commit-and-push.
- Rebase onto main before final merge. If wt-a ships Wave F (Automation API) during M21, expect conflicts ONLY in `src/shells/*/applications/*` — coordinate by hand.
- No cherry-picking across waves. Each wave is a self-contained commit (or commit pair if Wave A's 3 steps want separate commits).

## 5. Acceptance criteria

- `grep -r "@refinedev" panel-ui/src panel-ui/tests` → 0 hits
- `grep -r "refinedev" panel-ui/package.json` → 0 hits (not even in overrides)
- Full `make test-all` green (Go + vitest + Playwright)
- Panel boots, login works, CRUD in all admin + user pages works, URL-bookmarking preserves filter/page state
- Bundle size: main chunk should drop by ~100–150kB (Refine is not small)
- BLUEPRINT §M21 → SHIPPED, ADR-0037 → accepted
