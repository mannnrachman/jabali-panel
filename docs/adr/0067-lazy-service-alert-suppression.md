# ADR-0067: Suppress critical alert on `inactive` + `disabled` services

**Date**: 2026-04-26
**Status**: accepted
**Deciders**: shuki (operator) + assistant
**Supersedes part of**: ADR-0065 §5 (alert synthesis rule for service state)

## Context

ADR-0065 §5 shipped the original alert rule: any service in the agent's
allow-list with `ActiveState ∈ {inactive, failed}` produced a critical
alert. The rule made sense for always-on services (mariadb, nginx,
pdns), but Jabali also ships **lazy-started** units — most notably
`jabali-webmail.service` (boots on first `domain.email_enable`) and
historically `jabali-stalwart.service` on the same lifecycle. On any
host with no mail-enabled domain, `jabali-webmail` legitimately stays
`inactive` + `disabled`, and the Server Status page showed a permanent
red banner ("jabali-webmail.service is inactive") that operators
quickly learned to ignore — exactly the wrong UX for an alert.

`systemctl` exposes the operator's intent on every unit via
`UnitFileState` (`enabled` / `disabled` / `static` / `alias` /
`indirect` / etc.). The agent already shells out a single
`systemctl show` per probe; adding the field is one extra `-p` flag
and zero extra round-trips.

## Decision

`system.service_details` includes `UnitFileState` in `ServiceDetail`.
`synthesizeAlerts` treats `failed` as critical unconditionally and
treats `inactive` as critical **only when** `UnitFileState ∈
{enabled, enabled-runtime, static, alias}`. Inactive units with
`UnitFileState ∈ {disabled, indirect, generated, transient}` produce
no alert.

## Alternatives Considered

### Alternative 1: Hard-code a "lazy-started" allow-list panel-side
- **Pros**: explicit; easy to reason about; doesn't depend on systemd metadata
- **Cons**: drifts as services move between lazy and eager modes; a missed entry resurfaces the noise; couples panel-api to service inventory
- **Why not**: requires a follow-up edit every time a service changes lifecycle, and a missed update silently regresses the alert UX

### Alternative 2: Stop alerting on `inactive` entirely
- **Pros**: zero false positives
- **Cons**: an enabled service that fails to start typically lands in `failed`, but units stopped manually (`systemctl stop nginx`) report `inactive` — those should still alert
- **Why not**: loses a real signal class to fix a noise class

### Alternative 3: Read `LoadState` instead of `UnitFileState`
- **Pros**: already in the response; no new field
- **Cons**: `LoadState` reports the unit-file state as the systemd loader sees it (`loaded`, `masked`, `not-found`), not the operator's enable/disable intent — wrong dimension

## Consequences

### Positive
- Server Status page stops painting a permanent red banner on hosts with no mail-enabled domains
- Alert semantics now match operator intent: "you said this should run, it isn't" → critical
- Pattern extends to any future lazy-started unit without a code change

### Negative
- An operator who runs `systemctl disable nginx` (intentional or not) on a critical service silences the alert until something fails outright
- One more field on the service-details wire payload; trivial cost

### Risks
- **Misclassification of `static` units**: a static unit (no `[Install]`
  section) is treated as "should be running"; the assumption holds for
  every static unit Jabali manages today. Mitigation: revisit if a
  future static unit lands in the allow-list as a lazy-started one.
