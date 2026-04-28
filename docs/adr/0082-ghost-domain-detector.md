# ADR-0082: M38 Ghost Domain Detector — periodic DNS-alignment check

**Date:** 2026-04-28
**Status:** Accepted
**Deciders:** shuki (operator/architect)
**Related:** ADR-0049 (M24 IP Manager), ADR-0056 (M14 dispatcher),
ADR-0047 (pdns-recursor on loopback), old jabali-panel issue #64

## Context

Hosting operators routinely accumulate "ghost" domains — rows in
`domains` whose public DNS no longer points at this server. Causes:

- The owner moved their site elsewhere, never told the operator,
  and didn't delete the account.
- A migration cutover changed authoritative DNS but the operator
  never trimmed the inactive accounts.
- The owner mistakenly dropped the A record while editing zone
  data at their registrar.

Symptoms invisible to the panel today:
- Quota + cgroup memory still allocated to the user.
- Mailbox queue still trying to deliver to a server that nobody
  uses.
- Backups still running over an inactive account.
- SSL renewals still firing HTTP-01 attempts that never succeed
  because the public IP doesn't reach this server.

Old jabali-panel issue #64 ("Ghost Domain Detector") asked for an
automated detector that flags these. M38 ships it.

## Decision

Add a periodic DNS-alignment detector wired into the existing
M14 event-source pipeline:

1. **Schema:** three new columns on the `domains` table —
   `ghost_state ENUM('unchecked','ok','mismatch','nxdomain','error')`,
   `ghost_checked_at DATETIME(0) NULL`, `ghost_detail VARCHAR(255)
   NULL`. Migration 000092.

2. **Detector:** a goroutine in `panel-api/internal/eventsources/`
   that ticks every 4 hours, walks domains in oldest-checked-first
   batches of ≤100, asks the agent's new `domain.dns_check` command
   to resolve apex A/AAAA via the host's configured resolver
   (pdns-recursor on the loopback per ADR-0047), and classifies
   the resolved set against the M24 managed_ips pool.

3. **Classification:**
   - `ok` — every resolved IP is in `managed_ips` with
     `IsBound=true OR IsDefault=true`.
   - `mismatch` — none of the resolved IPs match the pool, OR a
     mix where at least one matches but others don't (multi-A
     pointing partly elsewhere is operationally a misconfiguration
     for our purposes).
   - `nxdomain` — public DNS has no A/AAAA at all.
   - `error` — agent call or DNS lookup failed; treated as
     transient and does NOT fire an M14 event.
   - `unchecked` — initial state; never set by the detector.

4. **M14 integration:** state transitions to `mismatch` or
   `nxdomain` fire an event with kind
   `domain.ghost_detected.{mismatch|partial|nxdomain}`, severity
   `warning`, deeplink `/admin/domains?ghost=mismatch`. Per-
   `(domain_id, event_kind)` cooldown is 24 hours so a long-running
   misalignment doesn't spam the operator.

5. **Resolver choice:** the host's system resolver, NOT a custom
   public resolver. Operators sometimes run split-horizon DNS;
   the host's resolver is the right definition of "what does this
   server's nginx vhost actually see when a browser hits it from
   out there?" — public reachability is downstream of that. If the
   host's pdns-recursor is misconfigured, the detector will
   incorrectly flag domains; the recursor's own `/jabali-admin/dns`
   health is the place to fix it.

## Alternatives considered

### Public resolver (1.1.1.1 / 8.8.8.8)

Run all DNS lookups against an external public resolver to bypass
host-resolver misconfiguration. Rejected: split-horizon hosts
correctly point one set of clients at this server and another set
elsewhere; an external resolver would mis-flag every split-horizon
account as ghost. The host's resolver is the operationally correct
view.

### Authoritative-source via WHOIS

Walk WHOIS to find the domain's authoritative nameservers, then ask
those directly. Rejected: introduces WHOIS rate-limit + parsing
complexity, doesn't help with split-horizon, and adds a per-tick
fan-out of network calls. Same coverage as the system-resolver
approach for a fraction of the implementation cost.

### Reconciler pass instead of dedicated event source

Fold the check into the existing reconciler tick. Rejected: the
reconciler runs every 60s; ghost-checking 5000 domains every 60s
floods both pdns-recursor and the agent. The 4-hour cadence with a
batched walk is the right shape and doesn't fit the reconciler's
fast-tick model. Event sources are the right home for periodic
read-only system observers.

### Auto-disable / auto-suspend on detected ghost

Take action on the user account when a domain is flagged. Rejected
in v1: false positives (DNS propagation delay after a legitimate
domain move; transient resolver outage; a recently-registered
domain whose A record propagation is still in flight) make
auto-action dangerous. The detector reports; the operator decides.

## Consequences

### Positive

- Operators see a tangible "X domains haven't pointed at this
  server in N hours" signal in the admin notifications stream.
- Future-cleanup workflows have a queryable column to filter on.
- Cost: one DNS lookup per domain per 4h via the existing pdns-
  recursor; negligible.

### Negative

- Split-horizon hosts will mis-classify domains where the
  host-side resolver answers differently from the internet. The
  detector's resolver choice is documented; operators of such
  hosts can disable the detector via a future server-setting toggle
  (M38.1 if demand surfaces).
- A wide multi-A record where some IPs point at this server and
  others point at a CDN ingress mark as `mismatch`. This is by
  design — we want the operator to notice — but it's a class of
  false-positive worth surfacing in the runbook.

### Risks

- pdns-recursor downtime causes every domain to flip to `error`
  and stay there until the recursor is back. The detector itself
  does NOT fire M14 alerts for `error` state; the recursor's own
  service-down event is what surfaces the recursor outage.
- The 24-hour M14 cooldown means a domain that flaps in/out of
  alignment within the cooldown window only fires once. Acceptable
  trade-off vs. operator alert-fatigue.

## Implementation

- Migration: `000092_alter_domains_ghost_state.up.sql`
- Agent command: `panel-agent/internal/commands/domain_dns_check.go`
- Detector: `panel-api/internal/eventsources/domain_ghost.go`
- Repository methods:
  `panel-api/internal/repository/domain_repository.go` —
  `UpdateGhostState` + `ListForGhostCheck`
- UI: badge in admin DomainList + filter `?ghost=` on list
  endpoint deferred to follow-up commit (this ADR ships the
  backend + event surface only).
