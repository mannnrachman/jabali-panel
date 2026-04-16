# 0012 — Refine + Ant Design + TanStack Query frontend

## Status
Accepted — 2026-04-16

## Context
The admin UI is a React single-page app. Refine provides resource/auth/data providers, Ant Design provides UI components, and TanStack Query handles data fetching. This combination is battle-tested for admin dashboards.

## Decision
React 18, Refine 4.x for resource routing and auth, Ant Design 5 for components, native axios with JWT interceptor for HTTP. No custom design system. Build with Vite.

## Consequences

### Positive
- Mature ecosystem; many plugins available
- JWT authentication integrates cleanly with axios interceptor
- Ant Design is enterprise-grade (well-designed components)
- Refine handles boilerplate (CRUD routes, resource names)

### Negative
- Ant Design bundle is large (increases JS payload)
- Refine adds abstraction layers (steeper learning curve)
- Limited customization without CSS overrides

### Neutral
- Dark mode and theming require Ant Design ConfigProvider setup

## Alternatives considered

- **Next.js**: Rejected — SSR unneeded for admin UI, adds complexity
- **Mantine**: Rejected — smaller ecosystem than Ant Design
- **Hand-rolled React**: Rejected — wastes time on component library

## References
- `panel-ui/src/` — React application
- `panel-ui/refine.config.ts` — Refine configuration
