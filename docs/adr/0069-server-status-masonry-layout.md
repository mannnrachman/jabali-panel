# ADR-0069: AntD `<Masonry>` for Server Status page layout

**Date**: 2026-04-26
**Status**: accepted
**Deciders**: shuki (operator) + assistant
**Related**: ADR-0065 (server-status backend), ADR-0046 (M23 responsive)

## Context

ADR-0065's first cut composed Server Status as a stack of `<Row>/<Col>`
groups: a banner `HostHeaderCard` on top, a 4-up `MetersGrid`, then
two `<Row>`s for Disks/Network and Queues/Processes/Updates. The
heavier `ServicesGrid` lived underneath as a full-width row.

Two operator-driven complaints surfaced as the page matured:
1. **Forced row heights** — Disks (2-5 rows) and Network (2-3 rows)
   share a row; whichever is shorter gets stretched to match, leaving
   visible whitespace the operator reads as a layout bug
2. **Top-of-page real estate** — operators wanted Services + System
   Information visible above the fold without losing the meters; the
   single-column banner card buried both

The latter prompted a pair of new cards (`ServicesSummaryCard` +
`SystemInfoCard`) that supersede the old banner; the former needed a
container that flows mismatched heights into a balanced grid.
`UserSlicesCard` (ADR-0068) would join the page next, making the
height-mismatch problem worse.

## Decision

Replace the manual `<Row>/<Col>` composition with one
`<Masonry columns={{xs:1, sm:1, md:2, lg:3}} gutter={16} items={...}>`
over an items array. Card order in the array determines visual order
(column-first packing). Deleted: `HostHeaderCard.tsx`, `ServicesGrid.tsx`.
Added: `SystemInfoCard.tsx`, `ServicesSummaryCard.tsx`, `UserSlicesCard.tsx`.
`MetersGrid.tsx` split into four standalone meter cards
(`CPUMeterCard`, `MemoryMeterCard`, `SwapMeterCard`, `LoadMeterCard`)
so each flows in Masonry independently rather than as a 4-up sub-row.

## Alternatives Considered

### Alternative 1: Keep `<Row>/<Col>`, hand-tune column ratios
- **Pros**: no new layout primitive; deterministic control
- **Cons**: every new card requires re-balancing; height mismatches keep coming back; no responsive auto-fit
- **Why not**: maintenance tax compounds as cards are added (UserSlicesCard would have triggered a third refactor)

### Alternative 2: CSS Grid with `grid-auto-flow: dense`
- **Pros**: native browser support; no library code; proper packing
- **Cons**: introduces hand-rolled CSS into a codebase the user explicitly wants kept on AntD primitives only (per "ants primitives only without custom css" feedback this session)
- **Why not**: violates the no-custom-CSS constraint

### Alternative 3: Third-party masonry library (react-masonry-css, masonic)
- **Pros**: proven Pinterest-style layout
- **Cons**: extra dependency; AntD already ships its own Masonry in v6.x
- **Why not**: AntD-native option exists in 6.3.6

## Consequences

### Positive
- Cards of different heights pack into balanced columns automatically
- Adding a new card is a one-line items-array entry, no row-rebalancing
- Responsive breakpoints handled by AntD (`xs:1, sm:1, md:2, lg:3`)
- Pure AntD primitives, no hand-written CSS

### Negative
- Visual order = column-first packing, not row-first; operators reading the page L→R, top-to-bottom see "skipping" between cards as their eyes move down a column. Mitigated by ordering small meters first so the eye-track stays local to the top region.
- Cannot pin a specific card to a specific column — order is determined by Masonry's balancing pass

### Risks
- **AntD Masonry API trap (already burned us)**: `<Masonry>` does NOT
  render arbitrary children passed between its tags — it only renders
  nodes from `props.items[].children`. The first cut shipped JSX
  children and produced a black page. Mitigation: file header comment
  in `ServerStatusPage.tsx` documents this; future refactors that
  touch the items array won't regress. (Same caveat called out in the
  ADR-0065 layout addendum.)
- **AntD Masonry GA timing**: shipped in AntD 6.x; if the project
  downgrades to 5.x for any reason, this ADR has to flip.
