# 0090 — 2FA TOTP + recovery codes via Kratos built-ins

**Status:** Accepted (2026-05-07)
**Supersedes:** none
**Related:** ADR-0034 (Kratos is the only auth source)

## Context

Operators want a second factor on the panel. The previously drafted
plan (`plans/2fa-totp.md`, pre-M20) introduced bespoke surface:

- Custom `totp_backup_codes` table with bcrypt-hashed codes
- AES-GCM-encrypted TOTP secret on `users` row
- Custom `2fa_pending` JWT minted by panel-api
- Dedicated `/auth/2fa/{enroll,verify,disable}` endpoints
- Hand-rolled QR generation

That plan predated M20 (Kratos cutover, 2026-04-20). Kratos already
ships:

- `totp` self-service method (already enabled in
  `install/kratos.yml.tmpl`: `issuer: "Jabali Panel"`)
- `lookup_secret` method (single-use recovery codes, hashed at rest
  by Kratos)
- `session.whoami.required_aal` policy (`aal1` = password,
  `aal2` = password + second factor)
- AAL upgrade flow (`?aal=aal2` on the same login endpoint)
- Admin JSON-Patch on `/admin/identities/{id}` to remove credentials

ADR-0034 already states "Kratos is the only auth source". Building
bespoke 2FA on top of Kratos directly contradicts it: two TOTP
secret stores to rotate, two recovery-code stores, custom JWTs that
bypass Kratos session policy, admin tooling that diverges from
Kratos's own identity APIs.

## Decision

2FA is implemented entirely via Kratos built-ins. Panel-api owns no
TOTP secret, no recovery code, no 2FA-pending token. The only
panel-side surface is:

1. Two new lines in `kratos.yml.tmpl`:
   - `selfservice.methods.lookup_secret.enabled: true`
   - `session.whoami.required_aal: highest_available`
2. UI rendering of the existing settings flow's `totp` and
   `lookup_secret` groups on the user profile page (QR image + secret
   reveal + recovery-code reveal-once).
3. UI rendering of the existing login flow's aal2 escalation (Kratos
   already returns `requested_aal: "aal2"` and the right node tree on
   the same flow id; the existing renderer in `pages/Login.tsx`
   already handles this — no change to login submit logic).
4. Admin "Reset 2FA" endpoint
   (`POST /admin/users/:id/2fa/reset`) that calls the new
   `kratosclient.RemoveSecondFactor()` — a JSON-Patch sequence that
   removes `credentials.totp` and `credentials.lookup_secret`. 422
   on a missing path is treated as success (the desired end state is
   "absent").

The `required_aal: highest_available` setting means: users without
a second factor enrolled keep aal1 sessions (no behaviour change);
users who enrol TOTP/lookup_secret MUST present a second factor
before `whoami` succeeds. There is no per-route AAL exception — the
whole panel is aal2-when-enrolled.

The admin CLI break-glass path (`jabali admin login`, which mints a
session directly via the Kratos admin API) is unaffected because it
sets the session AAL itself; it remains the escape hatch when an
operator's 2FA is broken AND no other admin can reset it.

## Consequences

**Positive:**

- Zero panel-side TOTP/recovery-code storage; zero new tables; zero
  custom JWT.
- Consistent with ADR-0034 — one auth source, one place to inspect
  credentials, one place to rotate keys.
- TOTP secret encryption is Kratos's problem (AES-GCM with the
  Kratos secret material — same one that protects sessions).
- Backup-code rotation, code-reuse prevention, rate limiting, and
  AAL session invalidation all come for free from Kratos's existing
  flows.

**Negative / trade-offs:**

- Tightly coupled to Kratos's flow-rendering shape — the panel-ui
  reads `ui.nodes` directly. Kratos cosmetic changes between minor
  releases could move attribute names. Mitigated by narrow helpers
  (`totpEnrolmentDisplay`, `lookupSecretReveal`) with explicit
  fallbacks.
- No way to enforce "all admins must have 2FA" without code in
  panel-api (Kratos has no per-trait AAL policy in v26.x). Out of
  scope for this milestone; revisit when needed.
- WebAuthn / passkeys not in scope. Kratos supports them via the
  same flow shape, so adding later is additive.

## Implementation surface

| File | Change |
|---|---|
| `install/kratos.yml.tmpl` | `lookup_secret.enabled: true` + `session.whoami.required_aal: highest_available` |
| `panel-ui/src/kratos.ts` | Add `totpEnrolmentDisplay()` + `lookupSecretReveal()` helpers |
| `panel-ui/src/shells/user/MyProfile.tsx` | Render QR + recovery-code reveal blocks above the form |
| `internal/kratosclient/admin.go` | New `RemoveSecondFactor()` (JSON-Patch remove) |
| `panel-api/internal/api/users_2fa_reset.go` | New `POST /admin/users/:id/2fa/reset` handler |
| `panel-api/internal/api/users.go` | Mount new route |
| `panel-ui/src/shells/admin/users/UserReset2FAAction.tsx` | New row action + confirm modal |
| `panel-ui/src/shells/admin/users/UserList.tsx` | Wire row action |

## Operator notes

- Reset path requires the operator to confirm in a modal. Action is
  audit-logged (panel-api existing audit infrastructure).
- A user whose Kratos identity is missing
  (`KratosIdentityID == NULL`) gets a no-op success — these are
  pre-Kratos accounts that were never migrated and have nothing to
  reset.
- See `plans/2fa-totp-runbook.md` for the locked-out-user playbook.
