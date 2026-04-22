# Known Issues

Tracking file for non-blocking bugs that have been investigated but deferred. New entries go at the top; closed entries move to the bottom with a resolution SHA.

---

## Open

### KI-1 — Login.test.tsx: 4 failing tests (`useThemeMode must be used inside ThemeModeProvider`)

**Opened:** 2026-04-23
**Severity:** LOW (pre-existing on `origin/main` before M24 shipped, no production impact — tests are wrong, app works).
**Scope:** `panel-ui/src/pages/Login.test.tsx` — 4 of 4 tests fail.
**Discovered by:** wt-a's M24 ship-day smoke (2026-04-22) — noted as pre-existing and non-blocking; M24 merged on that basis.

**Failure signature (all 4 tests):**

```
Error: useThemeMode must be used inside ThemeModeProvider
 ❯ useThemeMode src/theme/ThemeModeContext.tsx:107
 ❯ LoginPage src/pages/Login.tsx:56
```

Tests:
- `LoginPage > renders the fields from the password-group flow`
- `LoginPage > shows an error when flow initialisation fails`
- `LoginPage > switches to TOTP input when the flow continues to AAL2`
- `LoginPage > surfaces top-level flow errors into an alert`

**Root cause:** Commit `f68e022 style(ui): center logo + title on login card` introduced a `useThemeMode()` call into `Login.tsx` (for light/dark logo selection on the login card). The test harness in `Login.test.tsx` renders `<LoginPage />` with `<BrowserRouter>` + `<QueryClientProvider>` but does NOT wrap in `<ThemeModeProvider>`, so the hook throws on mount. All 4 tests fail identically, before any assertions run.

**Why it hasn't broken production:** `App.tsx` wraps the entire tree in `<ThemeModeProvider>` — the real app is fine. Only the unit-test renderer is missing the wrapper.

**Fix sketch (cheap, not yet scheduled):**

Wrap the render helper in `Login.test.tsx`:

```tsx
import { ThemeModeProvider } from "@/theme/ThemeModeContext";

function renderLogin(/* args */) {
  return render(
    <ThemeModeProvider>
      <BrowserRouter>
        <QueryClientProvider client={queryClient}>
          <LoginPage />
        </QueryClientProvider>
      </BrowserRouter>
    </ThemeModeProvider>,
  );
}
```

If multiple test files need the same wrapper (likely), factor to `panel-ui/src/test/renderWithProviders.tsx` and update all callers.

**Reproduction:**

```bash
cd panel-ui && npx vitest run src/pages/Login.test.tsx --reporter=basic
```

**Blocks:** Nothing currently. CI job `panel-ui vitest` tolerates these failures via whatever test-tolerance config is in place (or they're being ignored — worth auditing as part of the fix).

**Close when:** All 4 tests pass on `origin/main` without the rest of the suite regressing.

---

## Closed

(none yet)

---

## How to add an entry

1. Next `KI-N` number (sequential, no reuse).
2. Short title.
3. Required fields: Opened, Severity (CRITICAL / HIGH / MEDIUM / LOW), Scope, Discovered by, Failure signature / symptoms, Root cause, Why-not-production-impact (if LOW/MEDIUM), Fix sketch, Reproduction, Blocks, Close-when criteria.
4. Keep to one screen per entry — if an issue needs more context, link to a plans/ or docs/adr/ file.

## How to close an entry

Move the entry to the `## Closed` section with a one-line note: `Closed 2026-NN-NN in <SHA>.` Keep the body intact for future reference.
