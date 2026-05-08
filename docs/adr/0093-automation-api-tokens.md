# ADR-0093 — Automation API scoped tokens (M44)

**Status:** Accepted
**Date:** 2026-05-09
**Related:** ADR-0034 (Kratos as sole auth source — superseded by this
ADR for non-browser callers), ADR-0050 (UDS topology), ADR-0080
(backup destination secret encryption pattern reused here).

## Context

Browsers authenticate via Kratos sessions per ADR-0034. External
non-human callers (CI scripts, monitoring systems, partner
integrations) need a small read-only HTTP surface without:

- Holding a Kratos session (no browser, no captcha-able login flow)
- Storing user-credential pairs in their secret manager
- Going through OAuth (M16 Hydra was rolled back per ADR-0038; no
  authorization server lives on the panel)

A revocable, scoped, HMAC-signed bearer token issued by an admin
fills this gap.

## Decision

Mint admin-issued bearer tokens carrying:

1. A 26-char ULID identifier (the "kid" in the auth header).
2. A 64-hex-char (32 random bytes) shared secret. Returned to the
   admin **exactly once** at mint time; afterwards stored as
   `secret_enc` AES-GCM ciphertext via the existing `ssokey` package.
3. A scope set drawn from a closed allowlist
   (`read:*`, `read:domains`, `read:users`, `read:applications`,
   `read:status`). The mint endpoint rejects unknown scopes.

External callers sign every request:

```
Authorization: Jabali-HMAC kid=<id>, ts=<unix>, sig=<hex>

sig = hex(HMAC_SHA256(secret,
                      METHOD || "\n"
                   || PATH   || "\n"  // includes query string
                   || ts     || "\n"
                   || hex(sha256(BODY))))
```

Server middleware:

- Parses + extracts kid/ts/sig from `Authorization`.
- Enforces 5-minute clock-skew window (rejects stale `ts`).
- Looks up token by kid; rejects revoked rows.
- Caps body at 1 MiB before signature recomputation (defense
  against memory-exhaustion attacks against unauthenticated input).
- Recomputes the signature; constant-time-compares.
- Stashes the verified token in `gin.Context` for downstream
  `RequireScope("read:domains")`-style middleware.
- Bumps `last_used_at` + `last_used_ip` in a goroutine — never
  blocks the verified request.

Public routes mount under `/api/v1/automation/*` OUTSIDE the Kratos
session middleware. Response shapes are intentionally thinner than
the Kratos-auth equivalents — listen-IP topology, doc-roots, and
per-user infra fields are stripped so external automations don't
accidentally cache them.

Admin endpoints under `/api/v1/admin/automation/tokens` (mint, list,
revoke) sit behind the standard `RequireAdmin` middleware.

Soft delete (`revoked_at` timestamp) preserves audit history. A
revoked token's row stays in the list with a "revoked" tag for
operator forensics.

## Alternatives considered

- **OAuth 2.0 / OIDC client credentials.** M16 Hydra was already
  rolled back; reintroducing an authorization server adds
  significant operational cost (client lifecycle, token
  introspection, revocation lists) for callers that just need
  read-only access. Bearer + HMAC is a 200-line subset of that
  surface.

- **JWT-as-bearer (HS256-signed claims).** Would let the server
  skip the per-request DB lookup, but introduces JWT parsing
  complexity (alg=none attack class), commits to client-side
  scope encoding, and complicates revocation (need a denylist
  cache or short token lifetime + refresh). Not worth the cost
  for the audit cadence we want.

- **mTLS client certs.** Operationally heaviest of the three —
  we'd need to ship a CA, issue per-token certs, distribute them.
  Defer until automations can't tolerate bearer-token compromise
  blast radius.

- **Per-token IP allow-lists.** Tempting but defer to v2; UFW or
  CrowdSec scenarios on the panel's listen IP cover the same
  threat at the edge.

## Consequences

Positive:
- External read-only automations gain a stable, scoped, revocable
  way to hit panel data.
- Token lifecycle stays admin-controlled; no self-service mint
  flow to compromise.
- Soft-delete revocation with audit trail.
- Single-knob deployment — `secret_enc` lives in the panel DB
  alongside other ssokey-encrypted secrets.

Negative:
- Per-request DB lookup adds ~1ms latency to every automation
  call. Caching is possible but defer until measured pain.
- 1 MiB body cap means the API can't accept large file uploads
  via this surface. Acceptable: read-only routes don't need
  large bodies.
- HMAC secret rotation requires revoke + remint (no in-place
  rotation). Acceptable for v1; v2 can add a key-set rotation
  flow.
- No nonce/JTI store — replay protection is timestamp-window
  only. Threat model assumes the secret is stored securely on
  the caller side; if leaked, attackers can replay until the
  next operator-driven revoke, but only within the 5-minute
  window per individual replayed request.

## Future work

- Per-token IP allow-lists.
- Webhook callback signing (server-side HMAC of outbound
  requests, mirroring the inbound shape).
- Write scopes for narrow operations (e.g.
  `write:applications.deploy` for a CI pipeline).
- Per-tenant tokens (user-level, not admin-only).
- Per-token rate limiting beyond the existing per-IP middleware.

## References

- `panel-api/internal/middleware/automation_hmac.go` — verify path.
- `panel-api/internal/api/automation.go` — public read routes.
- `panel-api/internal/api/admin_automation_tokens.go` — admin
  mint/list/revoke.
- `panel-api/internal/repository/automation_token_repository.go` —
  data access.
- `plans/automation-api-tokens.md` — milestone blueprint.
- `plans/automation-api-tokens-runbook.md` — operator + caller
  workflows including curl + bash + python signature reference.
