# 0017 — SSL: try ACME first, fall back to self-signed, retry with exponential backoff

## Status
Accepted — 2026-04-17

## Context
Every hosted domain should have HTTPS. The previous opt-in flow left domains without any 443 vhost block until an admin toggled SSL on, and had no retry story when ACME issuance failed (DNS not yet propagated, Let's Encrypt rate limit, firewall blocking the HTTP-01 challenge, etc.). Operators ended up either toggling SSL manually per domain or re-triggering issuance by hand — neither scales, and a transient ACME failure would leave a domain permanently cert-less until someone noticed.

Two goals, in tension:
1. **HTTPS must work from day one** — users shouldn't see connection errors.
2. **We want a real Let's Encrypt cert whenever possible** — self-signed is a stopgap, not a destination.

## Decision
`ssl_enabled` defaults to `true` (migration 000017 flips the DB default and backfills existing rows). On domain creation, the reconciler runs an inline `ssl.issue` attempt with a 30-second budget. On any failure — timeout, ACME rejection, DNS lookup fail — the agent generates a 365-day self-signed cert via the new `ssl.self_sign` command, and the cert row moves to `pending_acme_retry` with a `next_retry_at` timestamp. nginx's 443 block is emitted unconditionally whenever any cert (self-signed or real) exists.

A 1-minute retry ticker in the reconciler loop walks rows whose `next_retry_at` has elapsed and retries ACME. Backoff: 5m → 15m → 45m → 135m, capped at 4h. After 20 consecutive failures the cert transitions to status `failed` — the retry ticker stops touching it until an operator invokes the manual `POST /domains/:id/ssl/retry` endpoint, which resets `retry_count` to zero and makes the row eligible again.

## Consequences

### Positive
- HTTPS is always available the moment a domain is created.
- Transient ACME failures self-heal in the background without operator intervention.
- The reconciler is the single retry authority — handlers stay thin, tests are straightforward.
- Single `ssl_enabled` flag remains as an explicit opt-**out** for genuinely non-public domains (internal IPs, placeholder hostnames that will never pass HTTP-01).

### Negative
- Browsers show a security warning during the self-signed window. Acceptable: "connection refused" (previous behavior) is strictly worse UX.
- Self-signed certs persist until ACME eventually succeeds or the operator flips `ssl_enabled` off. No automatic self-signed expiry rotation (they're 365-day so this rarely matters in practice).
- Domain creation API call has up to a 30s tail latency while the inline ACME attempt runs. Acceptable because it's the common path for a cert that *will* issue successfully, and failed attempts short-circuit into the self-signed path quickly.

### Neutral
- Two new status values extend the existing enum: `self_signed` (stopgap, ACME not currently scheduled) and `pending_acme_retry` (ACME failed, retry pending). Existing `pending`/`issuing`/`issued`/`renewing`/`revoked`/`failed` are unchanged.
- Two new columns on `ssl_certificates`: `next_retry_at DATETIME NULL` and `retry_count INT NOT NULL DEFAULT 0`.

## Alternatives considered

- **No self-signed fallback** (previous behavior): rejected — single ACME failure leaves the domain without any HTTPS at all. Connection errors on every https:// visit until an operator notices and manually retries.
- **Opt-in SSL (`ssl_enabled=false` default)**: rejected — every hosted domain realistically wants HTTPS. Making the default opt-in is friction with no corresponding benefit.
- **Try ACME inline only, no retry ticker**: rejected — a single failure during a DNS propagation window would leave the domain on self-signed forever.
- **Unbounded retry (no `failed` terminal state)**: rejected — a domain with a permanent misconfiguration (wrong DNS, unresolvable, etc.) would hammer Let's Encrypt indefinitely. 20 retries over ~1.5 days is enough signal to stop and wait for human attention.
- **Self-signed generated via a Go crypto package**: rejected — shelling out to `openssl` avoids a Go CA-pool dependency, matches what operators already trust, and keeps cert generation in the same process (the agent) that writes the filesystem.

## References
- `panel-api/internal/db/migrations/000017_ssl_enabled_default_true.up.sql` — flip default + backfill
- `panel-api/internal/db/migrations/000018_add_next_retry_at_to_ssl_certificates.up.sql` — retry columns
- `panel-api/internal/reconciler/reconciler.go` — `tryACMEOrFallback`, `ReconcileSSLInline`, `RetrySSLDueForACME`, `computeBackoff`
- `panel-api/internal/api/ssl.go` — `POST /domains/:id/ssl/retry`
- `panel-agent/internal/commands/ssl_self_sign.go` — openssl-based self-signed generator
- `panel-ui/src/components/ssl/SSLManagerTable.tsx` — status tags, Retry button
- `panel-ui/src/shells/admin/domains/DomainSSLSection.tsx` — per-domain banner
