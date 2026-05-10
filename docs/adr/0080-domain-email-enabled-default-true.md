# ADR-0080 — Email enabled by default for new domains

**Status:** Accepted — 2026-04-30

## Context

`domains.email_enabled` was introduced in M6 (migration 000054, ADR-0042) as
a column defaulting to `0`. The intent was a per-domain opt-in: a tenant
adds a domain, then explicitly turns email on when ready, which triggers
`domain.email_enable` (DKIM keypair generation, Stalwart Domain registry,
nginx `mail.<domain>` vhost, LE SAN cert).

In practice the toggle has been a friction point. Tenants who add a domain
in the user shell can't see an "Enable email" toggle there — the control
lives only in the admin shell. When the inline auto-enable on
`domain.create` (`EnableDomainEmailInline`) fails (agent transient,
Stalwart cold-start, DNS-autoconfig race), `email_enabled` stays 0 and
the tenant hits the `Mail → Mailboxes → Create` gate
("No email-enabled domains. Enable email on at least one domain before
creating a mailbox.") with no path forward in their own shell. The
typical resolution is admin intervention or a re-create.

The opt-in default predates the Stalwart-integrated reconciler convergence
and the panel-primary-domain auto-bootstrap (ADR-0048). With both in
place, the cost of always-on is low: DKIM is Ed25519 (~100µs), the
Stalwart Domain registry scales linearly to thousands, and `mail.<domain>`
LE SAN issuance follows the same ACME path the panel uses for the apex.

## Decision

1. **Flip the column default to `1`.** New domain rows come up with
   `email_enabled = 1` automatically. The reconciler then converges
   DKIM, the Stalwart Domain registry, the nginx `mail.<domain>` vhost,
   and the SAN cert as part of the standard create flow.

2. **Existing rows are not backfilled.** A tenant or admin who explicitly
   turned email OFF for a parked / web-only / marketing-redirect domain
   keeps that opt-out. Migration 000104 only modifies the column default
   for future inserts.

3. **Opt-out lives in the admin shell only.** The toggle stays in
   `Admin → Domains → <id> → Mail` for the rare cases above. Tenant
   shell does not expose a toggle — the typical tenant should never
   need to flip it.

4. **The `Mail → Mailboxes` gate stays.** Still meaningful for the
   deliberate opt-out case: admin disables email on a domain, mailbox
   creation correctly blocks until email is re-enabled. The gate text
   does not need to change — it remains accurate when triggered.

## Consequences

**+ Tenant friction removed** for the common path: a tenant adds a
domain, mailbox creation just works.

**+ Reconciler retry covers transient failures.** When inline
auto-enable on `domain.create` hits a transient (agent down, Stalwart
mid-restart), the row still lands with `email_enabled = 1` and the
existing reconciler convergence picks it up on the next tick. No more
"degraded to email_enabled=0" silent state.

**+ Reversible.** A tenant with a parked domain can ask the admin to
opt-out per-domain via the existing `domain.email_disable` flow. The
disable handler already preserves DKIM material (test
`Disable_KeepsDKIM`) so re-enabling is cheap.

**− MX always points at us for new domains.** A tenant who genuinely
doesn't want mail (rare) will see external SMTP land at our Stalwart
and `550` on unknown recipients. This is correct behavior, not a
regression — the alternative (no MX) is only better in a single edge
case (avoiding the load of bouncing).

**− LE SAN issuance on every domain create.** Previously deferred until
explicit enable. Mitigated by the existing per-domain ACME flow's
self-signed fallback when LE rate-limits hit; this is the same
fallback path used for the apex, so no new failure mode.

**− Existing tests that asserted `email_enabled = false` on a freshly
created domain now need updating.** Mechanical change, scoped to
`panel-api/internal/api/domain_email_test.go` and any fixture file
that touches a default-state domain row.

## Implementation

- Migration `000104_email_enabled_default_true` — `MODIFY COLUMN
  email_enabled TINYINT(1) NOT NULL DEFAULT 1`. Existing rows are
  intentionally untouched.
- `panel-api/internal/models/domain.go` — GORM tag flipped `default:0`
  → `default:1` to mirror the DB.
- `panel-api/internal/api/domains.go` (`create` handler) — explicitly
  set `EmailEnabled: true` on the new struct so the INSERT carries
  `email_enabled = 1` regardless of GORM's omit-on-zero behaviour.
- No agent-side code changes. The reconciler convergence path was
  already idempotent on `email_enabled = 1 + missing DKIM`.
- No UI code changes for fresh installs. The admin opt-out toggle
  is unchanged; tenant shell continues to have no toggle.

## Rollback

`000104_email_enabled_default_true.down.sql` reverts the default to
`0`. Existing `email_enabled = 1` rows would stay enabled (the down
migration only changes the column default). To fully revert intent,
also revert the `domains.go` create-handler change so new rows land
with `email_enabled = 0`.

## Related

- Migration `000054_create_mailboxes` — original column introduction.
- ADR-0042 — M6 mail-stack control plane (where `email_enabled` was
  defined as the intent flag).
- ADR-0048 — panel-primary domain bootstrap (the first place
  `domain.email_enable` was driven from a reconciler tick rather than
  an explicit operator click).
