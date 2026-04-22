# M23 — Responsive Panel UI

**Objective**: Every page in `panel-ui` renders and works correctly on phone, tablet, and desktop viewports without horizontal scrolling, clipped content, or broken interactions.

**Scope**: Layout chrome (shells, header, footer), table-heavy pages, forms, modals, and the file manager. **Not in scope**: changing any backend contracts, shipping a dedicated mobile app, native PWA install prompts, or bespoke touch gestures — the UI stays desktop-first AntD with graceful mobile adaptation.

**Branch**: `m23/responsive`

**Anchors**:
- Current behaviour bug: at 442×915 viewport the Dashboard `Disk` table's "Usage" column is clipped off-screen (evidence screenshot supplied on blueprint request).
- Shell uses AntD `Sider` with `breakpoint="lg"` + `collapsedWidth="64"` — collapses on <992px but the 64px gutter still eats screen real estate and the user has no way to reopen it on touch.
- No `<Table>` in the codebase sets `scroll={{ x: … }}` — confirmed by grep across `panel-ui/src`.
- Forms using AntD `Row gutter` + `Col xs/sm/md/lg` already responsive; don't re-do.
- Fixed `maxWidth: 720/800/960` wrappers on a few pages are benign on mobile (shrinks naturally). Leave alone.

**Breakpoints** (follow AntD defaults; do not invent new ones):
| Token | Width | Target |
|---|---|---|
| xs  | <576px  | phones (portrait) |
| sm  | ≥576px  | phones (landscape) / small tablets |
| md  | ≥768px  | tablets |
| lg  | ≥992px  | small laptops — **current desktop breakpoint** |
| xl  | ≥1200px | desktops |
| xxl | ≥1600px | large desktops |

---

## Workflow conventions

1. **Every step is a PR to `main`** off `m23/responsive/<step-slug>` sub-branches. The umbrella `m23/responsive` branch is for tracking only — do not push code directly to it.
2. Before editing any symbol, run `mcp__gitnexus__impact({target, direction:"upstream"})` and record the blast radius in the PR description. Stop and flag if HIGH/CRITICAL.
3. Before committing, run `mcp__gitnexus__detect_changes()` and confirm the change set matches the step's declared scope.
4. After every step: `npm run build` **must** be green (`tsc -b` catches what `tsc --noEmit` misses — see feedback memory).
5. After every step that touches runtime code: open Chrome devtools → Responsive mode → test at **360×780** (iPhone SE / Pixel portrait) and **768×1024** (iPad portrait). Screenshot one golden-path page per step and paste into the PR.
6. Rebase each sub-branch onto latest `origin/main` before opening PR; re-run build post-rebase. This is required by dispatcher policy.

---

## Dependency graph

```
Step 1 (ADR + tokens) ─┬─► Step 2 (shells) ─┬─► Step 3 (header)        ──┐
                       │                     └─► Step 4 (footer)        ──┼─► Step 7 (E2E) ─► Step 8 (runbook + memory)
                       │                                                  │
                       └─► Step 5 (tables) ──┬─► Step 6 (pages/forms)   ──┤
                                             └─► Step 6a (file manager)──┘
```

- **Serial gate**: Steps 1 → 2 → 5 must land before 3/4/6/6a start (they all rely on the layout + table primitives).
- **Parallelizable**: 3, 4, 6, and 6a can go concurrently after 2+5 land. Assign to separate sub-agents if running in parallel — but 6a alone should get its own 2-day budget (see adversarial review critical #2).
- **Gate**: Step 7 waits on 2, 3, 4, 5, 6, 6a all green.

---

## Step 1 — ADR-0046 + design-token helpers

**Branch**: `m23/responsive/01-adr-tokens`
**Model**: strongest (Opus) — architecture decision
**Rollback**: `git reset --hard HEAD~1` — doc-only

### Context brief

The panel has no documented responsive strategy. Before refactoring shells, capture the decision and expose a single `useBreakpoint` hook so downstream steps don't invent their own. AntD ships `Grid.useBreakpoint()` which returns `{ xs: boolean, sm: boolean, … }` — use it directly; do not wrap.

### Tasks

1. Write `docs/adr/0046-responsive-ui-strategy.md`. Cover:
   - Breakpoints table above (verbatim).
   - **Decision**: mobile shell uses a `<Drawer>` sidebar triggered by a hamburger `Button` in the header; ≥lg keeps the persistent `<Sider>`.
   - **Decision**: every data `<Table>` gets `scroll={{ x: 'max-content' }}` so overflow scrolls horizontally inside the card instead of the viewport.
   - **Decision**: no custom CSS media queries in component files — route responsive decisions through AntD `Col xs/sm/md/lg` or `Grid.useBreakpoint()` (single source of truth).
   - **Rejected**: separate `/mobile` routes, React Native, hiding tables behind a "request desktop" banner.
   - Link to this plan.
2. Bump `docs/BLUEPRINT.md` milestone table with M23 entry.

### Verification

```bash
test -f docs/adr/0046-responsive-ui-strategy.md
grep -q "M23" docs/BLUEPRINT.md
```

### Exit criteria

- ADR committed and linked from `docs/BLUEPRINT.md`.
- No code changes.

---

## Step 2 — Shell layouts: drawer sidebar on mobile

**Branch**: `m23/responsive/02-shells-drawer`
**Model**: default
**Rollback**: revert branch; no data migrations.

### Context brief

`src/shells/AdminLayout.tsx` and `src/shells/UserLayout.tsx` currently render `<Sider collapsible breakpoint="lg" collapsedWidth="64">`. On mobile this shrinks the sider to a 64px icon strip, but users can't re-open it (the built-in trigger is a chevron at the bottom of the sider itself — easy to miss and positioned below the fold on phones). Replace with a dual-mode layout:

- **≥lg** (≥992px): render the current `<Sider>` (persistent, collapsible via the chevron you already style). Tablets 768–992px fall under this branch — the sider stays persistent because AntD's `breakpoint="lg"` no longer governs us (we handle it ourselves). Confirm at 768×1024 there is **no** drawer.
- **<lg** (<992px): hide `<Sider>` entirely. Render an AntD `<Drawer placement="left">` with the same `<Menu>` inside; opened by a hamburger `Button` in the header.

The screenshot evidence driving this milestone is at 442×915 (phone), well below lg. The ambiguity noted in adversarial review — "what happens at 768–992?" — is resolved here: sider stays, drawer never mounts. If QA on a real iPad surfaces a problem at 768, revisit the breakpoint in a follow-up, don't patch it under this step.

Run `gitnexus_impact` on `AdminLayout`, `UserLayout`, `JabaliHeader` before starting.

### Tasks

1. Add `useBreakpoint` call in `AdminLayout.tsx`:
   ```ts
   const screens = Grid.useBreakpoint();
   const isDesktop = screens.lg;
   ```
2. Extract the `<Menu>` element into a local `SidebarMenu` function so it can be reused in both `<Sider>` and `<Drawer>` without duplication.
3. Conditional render:
   ```tsx
   {isDesktop ? <Sider>…</Sider> : null}
   <Drawer open={drawerOpen} onClose={…} placement="left" width={256}
           styles={{ body: { padding: 0, background: siderBg } }}>
     <SidebarMenu onNavigate={() => setDrawerOpen(false)} />
   </Drawer>
   ```
4. Expose a `onMenuClick` prop on `JabaliHeader` (nullable). Wire a hamburger `<Button icon={<MenuOutlined/>} type="text" />` that renders **only** when `!isDesktop`. The button lives on the far left of the header (before `<JabaliTitle/>`).
5. Repeat steps 1–4 in `UserLayout.tsx`.
6. Drop `<Content style={{ padding: 24 }}>` → use `padding: screens.md ? 24 : 12` so mobile content doesn't lose 48px horizontal to padding alone.
7. **Close drawer on route change**: explicit template below. Without this the drawer stays open when a menu item is clicked:
   ```tsx
   const location = useLocation();
   useEffect(() => {
     setDrawerOpen(false);
   }, [location.pathname]);
   ```
   Put this in BOTH AdminLayout and UserLayout. Do not rely on the menu item's onClick; the effect is the single source of truth.

### Verification

```bash
npm run build
```
Manual (Chrome responsive mode, 360×780):
- Load `/jabali-admin` → no sidebar visible; hamburger icon on header left.
- Click hamburger → drawer slides in from left with full menu.
- Click a menu item → drawer closes, page navigates.
- Resize to 1200px → drawer gone, persistent sidebar back.

### Exit criteria

- No horizontal scroll on `/jabali-admin/users` at 360px.
- Drawer open/close works on touch; no visual regression at ≥lg.
- `npm run build` green.

### Known pitfalls

- `Grid.useBreakpoint()` returns `{}` on the server; in SSR you'd need a fallback, but this SPA runs client-only so defaults are fine.
- Don't delete `breakpoint="lg" collapsedWidth="64"` from `<Sider>` — keep those; they handle the 992px→1200px transition when the persistent sider is still shown.
- AntD `<Drawer>` by default renders a mask that blocks clicks on the page behind. That's desired behaviour on mobile — do not disable.

---

## Step 3 — Header responsive layout

**Branch**: `m23/responsive/03-header`
**Model**: default
**Rollback**: revert branch.

### Context brief

`src/components/JabaliHeader.tsx` renders logo + centered AutoComplete + theme toggle + user dropdown in a single flex row with fixed 24px horizontal padding and 64px height. At ≤sm the email text in the user-dropdown button (`admin@jabali.local`) pushes the theme toggle and search box off-screen.

### Tasks

1. Use `Grid.useBreakpoint()`:
   - `xs` → hide the email text in the dropdown trigger; keep avatar + caret only (`icon={<Avatar />}`, remove the ` {email}` text).
   - `xs` → drop the header padding to `0 12px`.
   - `sm` → show email.
2. Search box: on `xs` hide the inline AutoComplete and render a `Button icon={<SearchOutlined/>}` instead; clicking opens a `Modal` (`closable`, `footer={null}`, `width="100%"`) containing the same AutoComplete. **Do NOT** fork the AutoComplete into two instances with different handlers — render ONE AutoComplete and conditionally parent it into either the header flex slot (`screens.sm`) or the Modal body (`!screens.sm`) using a single JSX element referenced from both slots. The gate is `Grid.useBreakpoint()` — approved per ADR-0046 (not a custom media query). Keyboard "/" shortcut still focuses the inline version; on xs it opens the Modal and focuses the AutoComplete inside.
3. Wire the `onMenuClick` prop from Step 2; render `<MenuOutlined/>` hamburger at `xs+sm` (i.e. `!screens.lg`), positioned before `<JabaliTitle/>`.
4. `<JabaliTitle>` already has logo + wordmark. On `xs` hide the wordmark (keep logo only) to save horizontal room — do this by passing a prop `showWordmark={screens.sm}` and conditional-rendering inside `JabaliTitle.tsx`.

### Verification

At 360×780:
- Hamburger, small logo, search-icon button, theme toggle, avatar — fits in one row without overflow.
- Tap search-icon → modal opens covering the screen; typing works; selecting a result closes the modal and navigates.

### Exit criteria

- `npm run build` green.
- Header fits in 360px with no horizontal scroll.
- Desktop visual unchanged (≥lg).

---

## Step 4 — Footer responsive layout

**Branch**: `m23/responsive/04-footer`
**Model**: default (suitable for haiku/sonnet — tiny file)
**Rollback**: revert branch.

### Context brief

`src/components/JabaliFooter.tsx` uses `justifyContent: space-between` with left-cluster (logo + tagline) and right-cluster (GitHub + license + version). On narrow viewports the two clusters collide.

### Tasks

1. Add `Grid.useBreakpoint()` → on `xs` set `flexDirection: "column"`, `alignItems: "flex-start"`, `gap: 8`; keep row layout on `sm+`.
2. On `xs`: hide the "Web Hosting Control Panel" tagline to one-line the left cluster.
3. Right cluster: on `xs` drop the "·" dividers (just space items) and the `AGPL-3.0` link text (keep only the GitHub icon + version tag). Keep full right-cluster on `sm+`.

### Verification

At 360×780 and 768×1024:
- Footer occupies at most 2 lines; no horizontal scroll.

### Exit criteria

- `npm run build` green.
- Desktop footer unchanged (≥sm).

---

## Step 5 — Tables: universal horizontal scroll

**Branch**: `m23/responsive/05-tables-scroll`
**Model**: default
**Rollback**: revert branch.

### Context brief

No `<Table>` in `panel-ui/src` sets `scroll`. On narrow viewports, wide tables (Dashboard Disk/Services, Domain list with action buttons, DNS records, user list with Slice column) overflow the card or get clipped by the viewport. Fix in two layers:

1. **`SearchableTable` wrapper** (`src/components/SearchableTable.tsx`): add `scroll={{ x: "max-content" }}` as a default the caller can override.
2. **Standalone `<Table>` usages** outside the wrapper: `grep -rn '<Table' src --include="*.tsx"` to find them; add the same prop inline. Key offenders confirmed: `shells/admin/Dashboard.tsx` (Disk, Services, Applications tables).

Run `gitnexus_impact` on `SearchableTableStringQ` before editing the wrapper.

### Tasks

1. `src/components/SearchableTable.tsx` — default `scroll.x` to `"max-content"` while preserving any caller-supplied `scroll.y` (e.g. virtual tables that set a vertical height). Use a merge, not a naive spread:
   ```ts
   const scroll = {
     x: "max-content" as const,
     ...(tableProps.scroll ?? {}),
   };
   const { scroll: _ignored, ...restTableProps } = tableProps;
   <Table<T> {...restTableProps} scroll={scroll}>
   ```
   Rationale: naive `{...defaults, ...tableProps}` lets a caller that passes `scroll={{ y: 300 }}` stomp our x default (scroll is a single object, not two keys). The merge keeps x=max-content unless the caller explicitly overrides x.
2. Grep for every `<Table` that is NOT inside `SearchableTableStringQ` and add `scroll={{ x: "max-content" }}`. Include: Dashboard (3 tables), DNS records page, any bespoke table in user/admin shells.
3. Regression guard: if any caller currently sets a numeric scroll.x (to force a minimum width greater than content), keep their value — don't stomp it.

### Verification

At 360×780, `/jabali-admin` dashboard:
- Disk table shows horizontal scrollbar within its Card; all 5 columns reachable by swipe.
- No page-level horizontal scroll.

### Exit criteria

- `npm run build` green.
- Zero viewport-level horizontal scroll on any table page at 360px.
- Desktop tables unchanged visually (`max-content` only kicks in when columns overflow).

---

## Step 6 — Per-page overflow fixes

**Branch**: `m23/responsive/06-pages`
**Model**: default
**Rollback**: revert branch.

### Context brief

After steps 2–5 the chrome and tables are responsive, but individual pages may still have fixed-width widgets, long action-button rows, or layout decisions that break on mobile. This step is a targeted sweep.

### Tasks (per offender — each is small, commit separately)

1. **Dashboard** (`shells/admin/Dashboard.tsx`, `shells/user/UserDashboard.tsx`): Row gutters OK; the `Usage` column `<Progress>` bar renders fine inside horizontal scroll (from Step 5). No action needed other than verify.
2. **Domain list** (`shells/admin/domains/DomainList.tsx` + `shells/user/domains/UserDomainList.tsx`): action column has Dropdown + Edit + Delete. Confirm the Dropdown still opens on mobile (it does — AntD handles touch). No code change expected unless verify fails.
3. **DNS records page** (`shells/dns/DNSRecordsPage.tsx`, 895 LOC): several form rows with fixed 600px max-width. Change fixed `maxWidth: 600` → `maxWidth: "100%"` where it wraps forms. Verify the create-record modal at 360px — if action buttons stack, let them.
4. **Server settings** (`shells/admin/settings/ServerSettingsPage.tsx`): wrap both `<Card>`s' inner `<Row gutter={16}>` — already uses `Col span={12}`. Change to `Col xs={24} md={12}` so each field stacks on phones.
5. **Install Application modal** (`shells/user/applications/InstallApplicationModal.tsx`, 747 LOC): `<Modal width={…}>` — if it's a fixed pixel width, switch to `width={720}` + `styles={{ body: { padding: screens.md ? 24 : 12 } }}`. AntD clamps `width` to viewport so it's already fine below 720px; verify the reveal-once password panel doesn't overflow.
6. **Domain settings modals** (`shells/DomainSettingsButton.tsx`, `DomainRedirectsButton.tsx`): Modals with tabs + forms. Verify tabs don't overflow; drop `width` if set above 720.
7. **Login page** (`src/pages/Login.tsx`): renders outside the shells (no Sider / Header). The card is `width: 420` — at 360px viewport that already overflows. Change to `width: "100%"` and wrap in a div with `maxWidth: 420, padding: 16, margin: "0 auto"`. Verify Kratos error alerts don't overflow. This catches the case adversarial review flagged as out-of-scope otherwise.

### Verification

For each listed page, load at 360×780 and 768×1024 and confirm:
- No horizontal page scroll.
- All interactive elements (buttons, inputs, dropdowns) reachable.
- Form field rows wrap cleanly.

Screenshot each at 360px, attach to PR.

### Exit criteria

- `npm run build` green.
- Manual sweep passes for every item above.
- Desktop visuals unchanged.

### Known pitfalls

- `<Modal width={720}>` — AntD v5 clamps this to viewport width minus gutters, so it's already responsive. Don't switch to `width="100%"` (that removes the comfortable desktop max).
- The file manager's PDF preview at `maxHeight: "65vh"` is fine on mobile (vh unit handles it).

---

## Step 6a — File Manager responsive split

**Branch**: `m23/responsive/06a-file-manager`
**Model**: default
**Budget**: ~2 days (separate from Step 6 by explicit gate — scope is large).
**Rollback**: revert branch.

### Context brief

`shells/user/files/FileManagerPage.tsx` is 1513 LOC — too big to fold into the Step 6 sweep without under-delivering. Split-pane UI: tree on the left, file list in the center, optional preview pane. On mobile all three cannot coexist.

### Tasks

1. Add `Grid.useBreakpoint()` at the top of `FileManagerPage`.
2. On `!screens.md` (<768px):
   - Render file list full-width.
   - Hide the tree pane inline; expose a `<Button icon={<FolderOutlined/>}>Folders</Button>` above the list that opens the tree in a `<Drawer placement="left">`.
   - Selecting a folder in the drawer closes it and reloads the list.
3. On `!screens.md` the preview pane (currently docked right) becomes a full-screen `Modal` opened on preview action.
4. Upload/action toolbar: if buttons wrap to 2+ rows, use `<Dropdown menu={…}>More</Dropdown>` to collapse rarely-used actions on xs.
5. Gut-check the 65vh PDF preview inside the modal — should still be fine.

### Verification

- 360×780: tree drawer opens/closes; file list scrolls; preview modal covers screen.
- 768×1024: layout matches desktop (tree + list + preview all visible).
- Existing Playwright `tests/e2e/` file manager spec (if any) passes; add a responsive smoke assertion.

### Exit criteria

- Build green, no horizontal viewport scroll at 360px on the files page.
- Desktop unchanged.

### Known pitfalls

- The file list may be inside a `<Row gutter={…}>` + `<Col>`; if so, the conditional render must hide/show at the `Col` level, not by mutating the `<Row>` children array.
- Drag-drop uploads already work via native HTML5 DnD; that survives any layout change.
- Don't migrate state management (current approach is fine); this is purely a layout step.

---

## Step 7 — Playwright mobile E2E smoke test

**Branch**: `m23/responsive/07-e2e`
**Model**: default
**Rollback**: revert branch.

### Context brief

Existing `panel-ui/tests/e2e/*.spec.ts` runs at desktop viewport only. Add a single mobile-viewport smoke spec that exercises the critical flows.

### Tasks

1. First read `panel-ui/tests/e2e/fixtures.ts` to understand the existing fixture pattern (admin/signIn/mockApi custom fixtures). The new spec must extend the SAME fixtures — don't spin up a parallel test harness. Pattern should be:
   ```ts
   import { devices } from "@playwright/test";
   import { test, expect } from "./fixtures"; // existing custom fixture file

   test.describe("mobile — iPhone 13", () => {
     test.use({ ...devices["iPhone 13"] });

     test("login → dashboard → users without horizontal overflow", async ({ page, signInAsAdmin }) => {
       await signInAsAdmin();
       await page.goto("/jabali-admin");
       await expectNoOverflow(page);
       await page.getByRole("button", { name: /menu/i }).click(); // hamburger
       await page.getByRole("menuitem", { name: "Users" }).click();
       await expectNoOverflow(page);
     });
   });
   ```
2. Helper (add to fixtures.ts so other specs can reuse):
   ```ts
   export async function expectNoOverflow(page: Page) {
     const hasOverflow = await page.evaluate(() =>
       document.documentElement.scrollWidth > document.documentElement.clientWidth
     );
     expect(hasOverflow).toBe(false);
   }
   ```
3. Coverage — include these viewports and flows:
   - **iPhone 13** (390×844): login, admin dashboard, users list, users create, domains list, domains create, server settings, **login page before auth** (not just post-login — new Step 6 task 7 depends on this).
   - **Pixel 5** (393×851): smoke only — login → dashboard → one table page. Proves the pattern works on Chromium Android DPI.
   - **iPad Mini** (768×1024): dashboard + domains, confirms the sider stays and drawer never mounts (adversarial review critical finding #5).
4. Keyboard-only drawer test (adversarial review HIGH #9):
   ```ts
   test("mobile drawer is keyboard-navigable", async ({ page }) => {
     await page.goto("/jabali-admin");
     await page.keyboard.press("Tab"); // should focus hamburger
     await page.keyboard.press("Enter"); // opens drawer
     await expect(page.getByRole("dialog")).toBeVisible();
     await page.keyboard.press("Escape"); // closes drawer
   });
   ```
5. Wire the new spec into `playwright.config.ts` `projects[]` under a `"mobile-chromium"` project so CI can run it separately. Do NOT delete or modify the existing desktop project.
6. Confirm the CI job `.gitea/workflows/ci.yml` picks it up; if projects[] is enumerated explicitly there, add `"mobile-chromium"`.

### Verification

```bash
cd panel-ui && npx playwright test tests/e2e/responsive.spec.ts
```

### Exit criteria

- New spec passes locally.
- CI run on PR is green.
- Desktop specs still pass (no regression).

---

## Step 8 — Runbook + memory + sanity sweep

**Branch**: `m23/responsive/08-runbook`
**Model**: default
**Rollback**: revert branch — doc-only.

### Tasks

1. `docs/runbooks/m23-responsive.md`:
   - How to verify a new page is mobile-friendly before merge (checklist).
   - How to run the mobile E2E locally.
   - Pattern: wrapping new tables in `SearchableTableStringQ` automatically gets horizontal scroll; bare `<Table>` needs `scroll={{ x: "max-content" }}` added.
2. Update `docs/BLUEPRINT.md`: mark M23 as SHIPPED with the final commit SHA on `main` after merge.
3. Append entry to `MEMORY.md` index: `- [M23 Responsive SHIPPED](project_m23_responsive.md) — AntD Grid breakpoints + Drawer sidebar + scroll=max-content on all tables; ADR-0046`.
4. Write the backing memory file `project_m23_responsive.md` with the key decisions + merged SHAs.

### Verification

- Runbook renders on the panel `docs/` viewer.
- `MEMORY.md` lint passes (each line under 150 chars).

### Exit criteria

- Docs merged.
- Memory index updated.
- M23 marked SHIPPED in BLUEPRINT.md.

---

## Anti-patterns to avoid

1. **Do not** introduce custom CSS media queries inside component files. Use AntD `Col xs/sm/md/lg` or `Grid.useBreakpoint()` — single source of truth.
2. **Do not** hide content on mobile by default (`display: none` at xs) without a way to reveal it. If data is important on desktop, it's important on mobile — make it scrollable, not invisible.
3. **Do not** set `overflow-x: auto` on the `<body>` or `<html>`. Page-level horizontal scroll is the regression we're fixing. Scroll goes inside the offending container (Table, Card, pre, code).
4. **Do not** wrap every page in a `<div style={{overflow:"auto"}}>` as a blanket fix. Targeted solutions only.
5. **Do not** rename or re-theme AntD components to "Mobile*"/"Desktop*" variants. One component, conditional props.
6. **Do not** split step 2 (shells) between AdminLayout and UserLayout into separate PRs. They share the same pattern; one branch keeps the diff reviewable.

---

## Verification checklist (every PR)

- [ ] `npm run build` green (tsc -b included)
- [ ] `npx playwright test` (desktop specs) green
- [ ] Chrome responsive mode: 360×780 and 768×1024 both inspected
- [ ] No horizontal viewport scroll at 360px on the pages touched
- [ ] Impact analysis run on every edited symbol (`mcp__gitnexus__impact`)
- [ ] Rebased onto latest `origin/main` post-implementation and re-built
- [ ] PR description lists blast radius + affected files

---

## Out of scope

- Offline PWA / service worker
- Landscape-specific layouts (we live with whatever AntD does)
- RTL text support (separate concern; Hebrew users currently use English UI)
- Touch gestures (swipe to dismiss drawer) — AntD default click-mask behaviour is enough
- Fixing responsive issues in upstream packages (Roundcube, phpMyAdmin) — those are iframe'd
