# ADR-0102 — Authenticated admin API exempt from AppSec WAF body inspection

**Date**: 2026-05-17
**Status**: accepted
**Deciders**: shuki (reported the 403), Claude (`/diagnose`)
**Related**: ADR-0060 (AppSec geoblock), ADR-0063 (per-scenario remediation override), ADR-0050 (unix-socket model), M46 (DB admin ops)

## Context

`/diagnose` 2026-05-17: the M46 admin DB-config tuner Apply button
returned **403**. Root cause confirmed on the test VM — CrowdSec
AppSec alert: `anomaly score block: sql_injection` on
`PUT /api/v1/admin/databases/config`. The 403 is a CrowdSec AppSec
(OWASP-CRS-class) **false positive**, not panel-api: `GET` config =
200, `PUT` body (DB tuning values) trips the SQLi inband rules
re-enabled by commit `d08393b3 fix(appsec): re-enable CRS-class
LFI/SQLi/XSS coverage`. The request never reaches the handler.

This is structural, not incidental: the admin API is a **database
control panel**. It legitimately carries SQL-ish content —
config-tuner values today, and the shipped "Database console (all
databases)" / future query surfaces tomorrow. Body-inspecting it for
SQL injection will keep false-positiving forever.

## Decision

The authenticated admin API path prefix **`/api/v1/admin/`** is
exempt from AppSec WAF blocking, via an `on_match` allowlist in the
`crowdsecurity/jabali-appsec` config:

```yaml
on_match:
 - filter: req.URL.Path startsWith "/api/v1/admin/"
   apply:
    - CancelEvent()
    - CancelAlert()
    - SetRemediation("allow")
```

(Authoritative CrowdSec AppSec syntax — the documented "disable AppSec
for a path/FQDN" pattern: `on_match` + filter + `CancelEvent` /
`CancelAlert` / `SetRemediation("allow")`.)

The block is emitted by **both** writers of `jabali-appsec.yaml`:
1. `install.sh install_crowdsec_appsec` — fresh-write + an idempotent
   "ensure present" step (covers install, `jabali update`, and any
   pre-existing file).
2. `panel-agent security_crowdsec.go` geoblock skeleton `header` —
   because `csAppSecGeoblockSetHandler` **fully regenerates** the file
   on every geoblock Apply; a single-writer fix would be wiped by the
   first geoblock toggle.

## Why this is safe (threat model)

A WAF in front of an endpoint that already requires a valid Kratos
session **and** DB-authoritative `is_admin` (`RequireAdmin`) adds
near-zero marginal protection there: an attacker who can drive
`/api/v1/admin/*` has already defeated authentication, at which point
SQLi-pattern blocking is theatre (they have a root-equivalent DB
console by design — ADR-0099). The real controls on that surface are
authentication, RBAC, audit (`db_admin_audit`), and single-use tokens
— not request-body regex.

Scope is the **prefix only** (`/api/v1/admin/`): tenant/user APIs,
vhosts, phpMyAdmin/Adminer, and unauthenticated routes keep full
AppSec coverage. Non-admin SQLi must still be blocked — verified.

## Alternatives considered

- **`RemoveInBandRuleByName` per CRS rule** — brittle: must enumerate
  every SQLi/anomaly rule name; new CRS rules re-break it. Rejected.
- **Disable CRS globally** (`cscli appsec-rules remove
  crowdsecurity/crs`) — throws away LFI/SQLi/XSS coverage for *every*
  tenant vhost to fix one admin endpoint. Rejected.
- **Single-writer fix (install.sh only)** — wiped by the first
  geoblock Apply (agent regenerates the file). Rejected; both writers
  emit it.

## Consequences

- The M46 DB config tuner (and the admin DB console) work; future
  admin DB/query surfaces won't re-trip CRS.
- `db_admin_audit` + `RequireAdmin` + single-use tokens remain the
  controls on the admin DB surface (unchanged).
- Verification requires a `.150` smoke: admin config `PUT` → 200 **and**
  a SQLi probe to a non-admin path still 403 (exclusion is scoped).
- Follow-up (noted, not blocking): the agent regenerator and install.sh
  now both carry the literal block — a future refactor should
  single-source it (e.g. a shared `internal/appseccfg` template) so
  the two copies cannot drift (`feedback_cross_boundary_contracts`
  class).
