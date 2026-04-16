# 0007 — English-only UI, no i18n infrastructure

## Status
Accepted — 2026-04-16

## Context
A multi-language UI requires upfront infrastructure (translation files, locale switching, RTL support) and ongoing maintenance. For v1, English-only is honest and reduces scope.

## Decision
`panel-ui` ships English strings only. No Refine i18n provider, no .json locale files (yet). All UI strings are hardcoded or in a simple `en.json` if convenience warrants. Defer the 7-locale investment (UK, DE, FR, ES, IT, JA, ZH-CN) to a future release.

## Consequences

### Positive
- Eliminates i18n boilerplate setup
- Simpler testing (no locale-specific rendering bugs)
- Faster feature shipping (no translation delays)

### Negative
- Non-English users see English UI (accessibility issue)
- Refactoring strings later adds work
- Locale switching becomes a visible gap

### Neutral
- Internal docs can be in any language

## Alternatives considered

- **8-locale i18n from day 1**: Rejected — unprovenanced translations, maintenance burden for solo dev, translation debt

## References
- `panel-ui/src/` — React source (no locale provider)
