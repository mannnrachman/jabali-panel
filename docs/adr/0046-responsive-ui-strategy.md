# ADR-0046 — M23: Responsive UI strategy

Status: **ACCEPTED**
Date: 2026-04-22
Milestone: M23 — Responsive panel UI

## Context

As of 2026-04-22 the panel is desktop-first AntD with no responsive strategy:

- No `<Table>` in `panel-ui/src` sets `scroll={{ x: … }}` — confirmed by grep.
  Wide tables (Dashboard Disk/Services/Applications, domain list with action
  columns, DNS records, users with Slice column) overflow the viewport on
  phones. Evidence: 442×915 screenshot showed the Disk table's "Usage" column
  clipped off-screen entirely.
- `AdminLayout.tsx` and `UserLayout.tsx` use `<Sider collapsible breakpoint="lg"
  collapsedWidth="64">`. Below 992px the sider shrinks to a 64px icon strip, but
  the built-in re-open trigger is a chevron at the bottom of the sider — below
  the fold on phone heights and fiddly on touch.
- Forms using AntD `Row gutter={16}` + `Col xs/sm/md={…}` are already responsive
  and don't need rework.
- A few pages wrap content in fixed `maxWidth: 720/800/960` divs; those shrink
  naturally below 720px and don't need changes.
- `src/pages/Login.tsx` renders `<Card style={{ width: 420 }}>` — overflows at
  360px viewports and sits outside the shell layouts.

Responsive work has been ad-hoc: some components use `Col xs/sm/md/lg`, others
use inline `style={{ width: 400 }}`, none use a shared breakpoint helper.

## Breakpoints (no new ones introduced)

Adopt AntD's built-in breakpoints verbatim. A new page is tested at every
boundary; no viewport between these widths gets special treatment.

| Token | Width     | Target                             |
|-------|-----------|------------------------------------|
| xs    | <576px    | phones (portrait)                  |
| sm    | ≥576px    | phones (landscape) / small tablets |
| md    | ≥768px    | tablets                            |
| lg    | ≥992px    | small laptops — desktop chrome     |
| xl    | ≥1200px   | desktops                           |
| xxl   | ≥1600px   | large desktops                     |

## Decisions

### 1. Mobile shell uses a `<Drawer>` sidebar; desktop keeps `<Sider>`

At `<lg` (<992px) hide the persistent `<Sider>` and render an AntD
`<Drawer placement="left">` containing the same `<Menu>`. Open via a hamburger
`<Button icon={<MenuOutlined/>}>` in the header. Close automatically on route
change via `useEffect([location.pathname], () => setDrawerOpen(false))`.

At `≥lg` the current `<Sider>` is persistent (no change in behaviour).

Tablet range 768–992px is handled by the `≥lg` branch — the sider stays
persistent. This is deliberate: iPad portrait (768×1024) has enough horizontal
room for a 256px sider plus content; forcing a drawer there would waste the
slot. If real-device iPad QA surfaces a problem, revisit the breakpoint in a
follow-up ADR.

**Rejected alternatives**:

- *Separate `/mobile` routes*: doubles route surface, drift risk, duplicate
  auth guards. No benefit over conditional rendering.
- *Keep the 64px collapsed strip on mobile*: already shipped and the bug
  we're fixing — the chevron trigger is unreachable on phone heights.
- *Bottom nav bar (iOS-style)*: unfamiliar in an AntD SPA; the drawer matches
  the desktop sider's visual weight so users transfer their mental map.

### 2. Every `<Table>` sets `scroll={{ x: "max-content" }}`

`max-content` lets the table expand to its natural content width; the overflow
scrolls horizontally **inside** its parent container (usually a `<Card>`)
instead of at the viewport. Desktop layouts are unchanged — `max-content` only
engages when columns would otherwise overflow.

Implementation lands in two places:

1. `src/components/SearchableTable.tsx` sets it as a merged default so every
   list page gets it for free. Caller overrides survive via property-level
   merge that preserves `scroll.y` (for future virtual tables).
2. Standalone `<Table>` usages not wrapped by `SearchableTableStringQ` (the
   Dashboard's three tables, etc.) add it inline.

**Rejected alternatives**:

- *Stack columns vertically on xs*: would require a bespoke mobile table
  component or card-per-row renderer. Non-trivial engineering, diverges from
  desktop layout, makes sorting and pagination UX worse.
- *Hide less-important columns on xs*: violates the "don't hide data on
  mobile" rule — admins need the Usage bar just as much as on desktop.
- *Wrap `<body>` in `overflow-x: auto`*: lets the viewport scroll sideways;
  every page loses the "fixed viewport" assumption and feels broken.

### 3. No custom CSS media queries in component files

Responsive decisions flow through two APIs and two only:

- AntD `Col xs/sm/md/lg/xl/xxl` props on grid rows.
- `Grid.useBreakpoint()` hook — returns `{ xs: boolean, sm: boolean, … }` —
  for conditional rendering of non-grid elements (drawer toggle, hidden
  search input, footer layout).

Component files never contain `@media` queries, `matchMedia()` calls, or
custom breakpoint constants. Single source of truth.

Global `global.css` may contain layout-wide rules that genuinely belong in
CSS (the existing `.ant-card` elevation, tab overrides). Those are still
media-query-free — they apply to every viewport uniformly.

### 4. Login page (`src/pages/Login.tsx`) is in scope

Outside the shell layouts, but identified by the M23 blueprint as overflowing
at 360px (`<Card style={{ width: 420 }}>` exceeds viewport). Fix in Step 6 of
the blueprint: switch to `width: "100%"` + a `maxWidth: 420` outer wrapper.

### 5. Mobile Playwright project, existing desktop project unchanged

A new `panel-ui/tests/e2e/responsive.spec.ts` exercises iPhone 13 / Pixel 5 /
iPad Mini viewports through `playwright.config.ts` `projects[]`. Desktop E2E
is not modified — the new project runs alongside in CI.

## Consequences

- Every new list page should use `SearchableTableStringQ` (inherits horizontal
  scroll for free) rather than a bare `<Table>`. PR reviewers enforce.
- Every new multi-column form should use `<Row gutter><Col xs={24} md={12}>`
  (or md={8} for 3-column). Fixed-width `<div style={{ width: X }}>`
  wrappers are a code-review flag.
- The drawer closure `useEffect` in both shells is load-bearing — if a
  future refactor extracts the `<Menu>` into a hook or module, the
  `useLocation()` dependency must travel with it.
- No a11y regression: the hamburger `<Button>` is keyboard-focusable,
  `<Drawer>` is a focus-trapped dialog per ARIA, and `Escape` closes it
  (AntD default).

## References

- `plans/m23-responsive.md` — 9-step construction blueprint (Opus-reviewed).
- AntD Grid docs: https://ant.design/components/grid (breakpoints section).
- Evidence screenshot: 442×915 Dashboard Disk table overflow (2026-04-22).
