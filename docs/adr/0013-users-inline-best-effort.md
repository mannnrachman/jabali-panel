# 0013 — Users inline best-effort (not reconciler-managed)

## Status
Accepted — 2026-04-16

## Context
User creation involves generating system accounts, home directories, and SSH keys. Unlike DNS or nginx, user creation requires plaintext passwords (to hash and store). The reconciler cannot retry user creation without the password.

This decision treats users as a special case: inline best-effort in the handler, with a manual reprovision endpoint for recovery.

## Decision
User CRUD handlers call the agent inline (best-effort) to create/update system accounts. If the agent call fails (e.g., user limit), the DB transaction rolls back. Recovery is explicit: `POST /users/:id/reprovision {password}` regenerates system account, SSH keys, and home directories. The reconciler does NOT own user creation retry.

## Consequences

### Positive
- User creation is interactive and immediate (better UX)
- Secrets (passwords) never leave the request context
- Reconciler focuses on stateless resources (DNS, certs, configs)

### Negative
- If agent crashes during user creation, operator must manually provision or use reprovision endpoint
- No automatic retry; admin must notice and act

### Neutral
- Reprovision endpoint is idempotent (can be called multiple times)

## Alternatives considered

- **Reconciler-managed users**: Rejected — can't store plaintext passwords, slows UX
- **Async user creation**: Rejected — confusing to operators; no feedback on success

## References
- `panel-api/internal/handler/user.go` — user CRUD handler
- `panel-api/internal/handler/user.go` — POST /users/:id/reprovision endpoint
