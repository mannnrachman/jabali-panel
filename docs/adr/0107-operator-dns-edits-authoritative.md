# ADR-0107: Operator/admin DNS record edits are authoritative

**Status**: accepted (2026-05-19)
**Supersedes the MX/SRV scope of**: ADR-0042-era "managed-record guard"
(PR#42), which over-blocked.

## Context

The panel guarded any `Managed=true` record of type SOA/NS/MX/SRV
against edit/delete (PR#42), returning 400 "managed by jabali …
direct edits are reverted". A user reported this as a faulty design:
an admin must be able to change the MX (or any record) and have jabali
push it into PowerDNS.

Investigation showed the "reverted" premise was largely false. The
reconciler is **not** a blind template-clobber. It uses a documented
three-tier ownership on `dns_records`:

| `managed` | `managed_by` | Owner | Reconciler behaviour |
|-----------|--------------|-------|----------------------|
| false | NULL | operator | hands off entirely |
| true | NULL | bootstrap (M4) | touches only if content == a known stale default |
| true | "m6" | M6 email | ensures its own `(name,type)`; respects an operator override at that tuple |

`models/dns.go` already states the override mechanism: *"…by flipping
`Managed` to false from the API."* The bootstrap apex MX the user hit
(`@ MX`, `managed_by=NULL`, content already an FQDN) is **never**
rewritten by any reconciler — `migrateBootstrapShape` only rewrites
MX when content is the legacy literal `"mail"`. The guard blocked an
edit that would have persisted fine.

## Decision

An operator/admin edit via the DNS API is **authoritative**.

1. On `PATCH /dns/records/:id`, after applying the change, demote the
   row to operator-owned: `Managed=false`, `ManagedBy=NULL`. Every
   reconciler path gates on `Managed=true` + a matching `ManagedBy`,
   so all of them (bootstrap apex converge, `migrateBootstrapShape`,
   M6 email ensure) hand off. The edited value becomes the desired
   state the DNS reconciler converges into PowerDNS; nothing reverts
   it.
2. The edit/delete guard is narrowed to **SOA and NS only**. These are
   zone *infrastructure*, not content records: PowerDNS regenerates
   the SOA serial on every change (a hand-edit is a no-op it
   overwrites), and the apex NS set must point at jabali's
   authoritative nameservers or the entire zone stops resolving.
   The message is corrected accordingly (no false "reverted" claim).
3. Delete of MX/SRV/etc is allowed. A feature that genuinely requires
   a record (e.g. M6 mail autoconfig SRV while email is enabled) will
   legitimately re-create it on the next reconcile — that is correct
   convergence toward the feature's requirement, not a reverted edit.

## Consequences

- An admin can repoint the MX (e.g. to an external mail provider) and
  it sticks + reaches PowerDNS — the reported requirement.
- Editing an M6-owned SRV converts it to an operator override; M6's
  reconcile already treats a `Managed=false` row at its `(name,type)`
  as a respected conflict (domain_email.go), so it will not duplicate
  it. Disabling/re-enabling email re-seeds M6 records from scratch as
  before.
- SOA/NS remain panel-owned (footgun guard, not a capability denial —
  PowerDNS owns the serial; NS delegation is load-bearing for the
  whole zone).
- No migration: purely handler behaviour + guard scope. Tests:
  `TestUpdateRecordManagedMXEditableDemotes` (200 + `managed=false`),
  `TestUpdateRecordManagedSOAForbidden` / `TestDeleteRecordManagedNSForbidden`
  (still 400).
