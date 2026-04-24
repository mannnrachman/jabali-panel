# ADR-0057: M14 — Web Push via VAPID, keypair in server_settings

**Status:** Accepted (2026-04-24)
**Driven by:** Plan `plans/m14-notifications.md` Step 1 (bootstrap) + Step 7 (UI enrolment + service worker).

## Context

M14 adds a bell-dropdown for in-app notifications. We also need out-of-browser delivery: an admin looking at a different tab, or with the browser closed on a laptop, still has to hear about a cert renewal failure or a disk-full threshold. SMS is out of scope (cost + vendor lock), desktop agents are out of scope (another install milestone), so the native-web choice is Web Push.

The W3C Push API requires VAPID (Voluntary Application Server Identification) keys — an EC P-256 keypair the push service uses to attribute push messages to a specific origin without per-browser registration. Two design questions:

1. **Where do the keys live?** Per-installation (one server-wide keypair) vs per-admin (each user gets their own).
2. **How do we sign + deliver push messages?** Hand-roll the VAPID JWT + ECDSA + payload encryption (HKDF/AES-GCM per RFC 8291), or use a library?

## Decision

**One server-global keypair per panel installation, stored in the existing `server_settings` table.** Seeded on first boot by `ServerSettingRepository.EnsureDefault` — not a migration (per `feedback_migration_data_seed_ordering.md`). Rotated only on explicit operator reset (a new endpoint, not part of M14; left as a follow-up).

**Library: `github.com/SherClockHolmes/webpush-go`.** Handles the VAPID JWT signing + P-256 ECDH + HKDF + AES-GCM encryption (RFC 8030 / 8291 / 8292) so panel-api doesn't reimplement any of that surface. Single dep; MIT-licensed; actively maintained; the dominant Go implementation (used by gotify, ntfy-sh, Mastodon clients).

### Key format + storage

Three nullable columns on the existing `server_settings` row (migration
`000065_server_settings_add_vapid.up.sql`):

| Column              | Value                                                                          |
| ------------------- | ------------------------------------------------------------------------------ |
| `vapid_public_key`  | base64-url-encoded uncompressed P-256 point (65 bytes → 87-char key)           |
| `vapid_private_key` | base64-url-encoded 32-byte scalar                                              |
| `vapid_subject`     | `mailto:admin@<panel_hostname>` (hostname from existing `hostname` column)     |

`server_settings` is the typed single-row table that already holds
installation-global values (hostname, public IPs, admin email). Adding
three columns stayed consistent with that model rather than introducing
a parallel key-value store for one feature.

Key generation is `webpush.GenerateVAPIDKeys()` from the library.
Seeding happens in `ServerSettingsRepository.EnsureVAPID`, called from
the first-boot seed goroutine in `panel-api/cmd/server/serve.go`
alongside the managed_ips default-row seed. Idempotent: only generates
if `vapid_public_key IS NULL`. Partial state (public key NULL while
private key or subject is set) is a fatal error returned to the caller
— we do not regenerate over a half-written row; the operator has to
explicitly NULL all three columns before retry (documented in the
runbook).

### Per-user subscription storage

Each browser the admin enrols in push delivers an `endpoint` URL, a `p256dh` public key, and an `auth` secret. Those go in the new `webpush_subscriptions` table, one row per browser per admin. `endpoint` is UNIQUE (the same browser re-enrolling updates the existing row; different browsers for the same admin live side by side).

The dispatcher's Web Push sender loads all rows for the target admin, posts encrypted payload to each endpoint, handles `410 Gone` by deleting the row (the endpoint is dead — usually because the user uninstalled the browser or cleared site data).

### Why per-installation keys, not per-admin

- Browser push services (FCM, Autopush, etc.) care about *application identity*, not *user identity*. One panel = one application. Per-admin keypairs would force admins to re-subscribe when they move to a new browser, which is the opposite of what VAPID is for.
- Per-installation is also what major implementations ship (Mastodon, GitLab, Gotify all use server-global keys). Matching the convention means tooling (e.g. `web-push` CLI) works out-of-the-box on our keys.
- The security delta is small: a leaked VAPID private key lets an attacker impersonate the panel *to the browser push service* — it does not let them decrypt the payloads (those are per-subscription ECDH with the client). Keypair rotation is straightforward (new keys → existing subscriptions need re-enrolment from the UI).

### Rotation semantics

Explicit operator-triggered reset. Post-M14 this will likely be an admin UI action; the underlying endpoint writes new rows to `server_settings`, wipes `webpush_subscriptions` (all are invalid against new keys), and existing push-subscribed browsers show a "re-enable notifications" banner on next bell-dropdown render.

## Alternatives considered

**Per-admin keypair.** Rejected per above — violates VAPID's model (app identity, not user identity).

**WebPusher's own hosted relay.** Rejected: another SaaS dep on the critical path for alerts, and closes the self-hosting story M14 is committed to.

**Server-Sent Events + in-browser polling instead of Web Push.** Rejected: requires the panel tab to be open. SSE is how the bell dropdown stays live while the tab IS open (Step 7), but it doesn't cover "closed browser / different tab" delivery.

**Hand-rolled VAPID + payload encryption (no library).** Rejected: the spec surface (JWT ES256 + HKDF + AES-GCM + content-encoding: aes128gcm) is large enough that even one off-by-one in the HKDF info string silently breaks delivery on Firefox while passing on Chrome. The library is 800 lines of Go we don't have to audit.

## Consequences

**Positive:**
- One dep, known-good, solves all the cryptographic glue.
- `server_settings` is already backed up / migrated / operator-readable — no new secrets store.
- Rotation story is clean (rows in one table).

**Negative:**
- Library pins us to VAPID v1 (the only VAPID spec that's final). Any future v2 spec shift would require switching libraries.
- `vapid_private_key` is readable by any process that can read `server_settings` via panel-api (i.e. panel-api itself + `mariadb` CLI as the `jabali-panel` user). The threat model is "root on the panel host owns the panel anyway" — the same model as the DB password. No dedicated secrets manager for one row.
- Rotating VAPID invalidates every enrolment. Operator has to communicate before rotating; UX for re-enrolment banner is Step 7 scope.
- The `json:"-"` tag on all three VAPID fields keeps the raw values out of the generic `/api/v1/admin/settings` GET response. Step 5 adds a dedicated `/api/v1/admin/vapid/public` endpoint that returns only `vapid_public_key` so the SPA service worker can register push; the private key and full subject stay out of the API surface.

## Related

- Plan: `plans/m14-notifications.md` — Step 1 (seed), Step 3 (sender), Step 7 (browser enrolment)
- Code (Step 3): `panel-api/internal/notif/senders/webpush.go`
- Code (Step 7): `panel-ui/src/sw-notifications.ts` + enrolment hook
- ADR-0056: Dispatcher that drives the sender
- ADR-0002: DB as truth — `server_settings` is the canonical store
- Spec: RFC 8292 (VAPID), RFC 8291 (payload encryption), RFC 8030 (web push protocol)
