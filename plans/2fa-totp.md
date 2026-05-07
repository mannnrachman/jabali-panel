# 2FA (TOTP + Backup Codes via Kratos) — Plan

**Status:** Draft (rewritten 2026-05-07 from bespoke-JWT plan to
Kratos-native). Old plan predated M20 Kratos cutover and would have
duplicated auth surface.
**Owner:** shuki
**Goal.** Every account that logs in with password can opt into TOTP
as a second factor. Lookup-secret recovery codes recover from a lost
phone. Admin break-glass CLI login is unaffected (escape hatch).
**Depends on:** M20 ✅ (Kratos identity).
**Branch:** `2fa/kratos-totp`. Default mode: branch + ff-merge into
`main` per step.
**ADR target:** **0090** (M43 took 0089, 0090 free at draft time).

---

## 0. Operating assumptions

- Branch + commit per step. Build, deploy to `root@192.168.100.150`
  via `jabali update`, smoke-test before next step.
- Conventional commits: `feat(auth): …`, `feat(ui): …`.
- `make test` green per step. `npx tsc -b` + `npm run build` clean.
- Kratos config templates live in `install/kratos.yml.tmpl`; renders
  via sed in `install_kratos()`. Never hand-edit the rendered
  `/etc/jabali-panel/kratos.yml` on VM — gets overwritten on next
  `jabali update` ('install.sh is truth' — memory rule).
- `feedback_never_agents`: hand-roll inline. No sub-agent dispatch
  on this auth-surface work.

## 1. Why Kratos-native (not bespoke)

Old plan drew its own `totp_backup_codes` table, AES-GCM-encrypted
the secret, minted a custom `2fa_pending` JWT, built dedicated
`/auth/2fa/*` endpoints. Kratos already ships:

- **`totp` method** (already enabled in kratos.yml: `issuer: "Jabali
  Panel"`). Stores secret in `identity_credentials.config` keyed by
  IdentityID. Enroll / verify / disable handled by the standard
  `settings` flow.
- **`lookup_secret` method** — N single-use recovery codes, hashed
  by Kratos. Generated via the settings flow, surfaced once in the
  flow response. Disable via the same flow.
- **AAL** — `aal1` = password, `aal2` = password + second factor.
  `session.whoami.required_aal` controls policy. When user has TOTP
  enrolled, login starts at aal1 and Kratos requires a second flow
  at aal2 before whoami succeeds.
- **AAL upgrade flow** — `GET /self-service/login/browser?aal=aal2`
  starts the second-factor challenge. Returns the same flow shape
  panel-ui already consumes from M20.

Bespoke duplicates means: two TOTP secret stores to rotate, two
recovery-code stores, custom JWTs that bypass Kratos session
policy, admin tooling that diverges from Kratos's own identity APIs
(which already support TOTP reset). All against ADR-0034 ("Kratos
is the only auth source").

## 2. Decisions

1. **Library:** Kratos built-ins. No `pquerna/otp` dep, no custom
   schema, no custom JWT.
2. **Issuer / label:** `Jabali Panel (<email>)` — already configured
   in kratos.yml `selfservice.methods.totp.config.issuer`.
3. **Backup codes:** Kratos `lookup_secret` method, **enable in
   kratos.yml** (currently disabled).
4. **AAL policy:** `session.whoami.required_aal: highest_available`.
   Users without TOTP keep aal1 (no behaviour change). Users with
   TOTP enrolled MUST present a second factor before any
   authenticated panel-api endpoint succeeds.
5. **Per-route override:** none. Whole panel is aal2-when-enrolled.
   Admin CLI break-glass (`jabali user 2fa-reset <email>`) bypasses
   the existing direct-DB session-mint path.
6. **Privileged-session for settings:** Kratos's
   `selfservice.flows.settings.privileged_session_max_age` already
   set to 15m — enrolling/disabling TOTP requires a fresh
   re-authentication within 15min. Reuses existing UI at
   `/login?refresh=true`.
7. **No admin TOTP enforcement v1:** opt-in. Future M can flip an
   "all admins must enable" gate at panel-api login handler.

## 3. Kratos config delta

```yaml
# install/kratos.yml.tmpl
selfservice:
  methods:
    lookup_secret:
      enabled: true        # NEW — surface backup codes
    totp:
      enabled: true        # already on
      config:
        issuer: "Jabali Panel"

session:
  whoami:
    required_aal: highest_available   # NEW — policy gate
```

## 4. Steps

### Step 1 — Kratos config: enable lookup_secret + AAL policy
**Files:** `install/kratos.yml.tmpl`. (No `install.sh` change —
`install_kratos()` already re-renders.)

- Add `lookup_secret.enabled: true`.
- Add `session.whoami.required_aal: highest_available`.
- Verify on VM: `kratos validate --config /etc/jabali-panel/kratos.yml`
  (already part of `install_kratos`). systemd reload.
- Smoke: identity without TOTP still gets `whoami` 200. Identity
  with TOTP enrolled but only aal1 session → `whoami` 401 with
  `session_aal2_required` body.
- **Wave gate.** Steps 2-5 depend on this config being live.

### Step 2 — UI: 2FA card on `/settings`
**Files:** `panel-ui/src/shells/.../SettingsPage.tsx` (new card),
new `panel-ui/src/components/TwoFactorCard.tsx`.

- States:
  - Not enrolled → "Enable two-factor" → POST
    `/.ory/self-service/settings/browser`, pick `totp` group, render
    QR (Kratos returns `image` node `totp_qr`, base64 PNG), text
    input for 6-digit code, submit, show `lookup_secret` reveal-once
    block on success.
  - Enrolled → "Disable" + "Recovery codes remaining: N" +
    "Regenerate".
- Reuse `apiClient` / Kratos browser-fetch helpers from M20 login.
- Render the PNG, not a hand-rolled QR.
- 15-min privileged-session: if Kratos returns 403
  `session_refresh_required_error`, redirect to
  `/login?aal=aal1&refresh=true&return_to=/settings/2fa`.

### Step 3 — UI: aal2 challenge on login
**Files:** `panel-ui/src/shells/.../LoginPage.tsx`.

- After password submit succeeds, call `whoami`. If 401 with
  `session_aal2_required`, start a new login flow with `aal=aal2` —
  same UI shell, only field is `totp_code` (or
  `lookup_secret_code` alternative). Submit. On success, navigate
  to original return_to.
- "Use a recovery code instead" link switches to `lookup_secret`
  group of the same flow (no separate flow).

### Step 4 — UI: recovery-code regeneration
**Files:** `TwoFactorCard.tsx` (extend Step 2).

- "Regenerate codes" → privileged-session prompt → settings flow →
  submit `lookup_secret_regenerate=true` → render new codes
  reveal-once.
- "Show remaining" — Kratos
  `whoami.identity.credentials_metadata.lookup_secret.used_at`
  array; count unused codes. Render badge "8 of 12 codes
  remaining". (If credentials_metadata isn't populated by default,
  count by difference from regenerate-flow response — verify
  during step.)

### Step 5 — Admin: TOTP reset for locked-out user
**Files:** `panel-api/internal/api/admin_users.go` (extend),
`panel-ui/src/shells/admin/users/UsersPage.tsx` (button).

- New admin REST: `POST /api/v1/admin/users/{id}/2fa/reset` →
  panel-api calls Kratos admin API `PATCH /admin/identities/{id}`
  with JSON-Patch removing `credentials.totp` +
  `credentials.lookup_secret`.
- Audit trail: write to `audit_log` (existing).
- UI: button on UsersPage row → confirm modal → success toast.
  Admin-only.

### Step 6 — Tests + CI
- `panel-api/internal/api/admin_users_test.go` — table-driven test
  for reset endpoint (mock Kratos admin client).
- `panel-ui/.../TwoFactorCard.test.tsx` — render states (not
  enrolled, enrolled, regenerate flow). Vitest only — Playwright
  E2E for actual TOTP UX out of scope (TOTP requires wall-clock
  stepping, fragile in CI).
- `make test && cd panel-ui && npx vitest run` clean.

### Step 7 — ADR-0090 + BLUEPRINT + runbook
- `docs/adr/0090-2fa-totp-via-kratos.md` — record decision to use
  Kratos built-ins over bespoke schema, list kratos.yml delta, AAL
  policy, admin-reset escape hatch.
- BLUEPRINT entry under "Authentication" (existing M20 section).
- `plans/2fa-totp-runbook.md` — operator runbook: how to reset
  locked-out user, how to inspect lookup_secret usage, what to
  delete from kratos DB if TOTP secret leaks.

## 5. Out of scope

- WebAuthn / passkeys (separate milestone).
- TOTP-required-by-default for admins (separate milestone with
  notice + grace period).
- Per-route AAL exceptions.
- Hardware-token methods (Kratos `webauthn` is the path, not reused
  here).

## 6. Open questions

- Does Kratos surface
  `credentials_metadata.lookup_secret.used_at` via `whoami` by
  default in v26.2? If not: count by difference (12 generated minus
  used set returned in regenerate flow), OR extend
  `internal/kratosclient/admin.go` with a small
  `IdentityWithMetadata(id)` call. Resolve in Step 4.
- AAL upgrade UX on mobile — Kratos returns same flow shape as
  aal1 login; verify LoginPage doesn't crash when only
  totp/lookup_secret nodes are present (no email/password). If it
  does, branch the form renderer.
