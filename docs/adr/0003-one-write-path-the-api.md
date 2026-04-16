# 0003 — One write path: the API

## Status
Accepted — 2026-04-16

## Context
Distributed write paths (CLI → DB, UI → DB, agent → DB) cause ordering bugs and make RBAC hard to audit. A single write path centralizes validation, authorization, and audit logging.

CLI commands, admin actions, and manager bridge calls all go through the same HTTP API endpoints, minting short-lived JWTs from the local `JWT_SECRET`.

## Decision
Only `panel-api` writes to the database and enqueues reconciler work. CLI is a thin HTTP client that mints short-lived admin JWT from the local `JWT_SECRET`. Exceptions (services that touch DB directly):
- `serve` (starts panel-api)
- `migrate` (schema changes)
- `update` (binary upgrades)
- `system` (diagnostics, read-only)

No new direct-write CLI subcommands without explicit approval.

## Consequences

### Positive
- Centralized RBAC and validation logic
- Single place to audit all writes (API logs)
- Predictable ordering; no race conditions
- Enables future multi-node setup (central API as coordinator)

### Negative
- CLI cannot bypass API (slower for bulk operations)
- Local JWT minting must be secure; `JWT_SECRET` is highly sensitive
- Operator must run API for any CLI writes

### Neutral
- Requires all CLI tools to speak HTTP + JWT

## Alternatives considered

- **Direct CLI writes to DB**: Rejected — defeats RBAC, hard to audit, allows silent failures
- **Event-driven multi-writer (Kafka etc)**: Rejected — overkill for single node, adds operational burden

## References
- `panel-api/internal/auth/jwt.go` — JWT minting from local secret
- `panel-api/cmd/cli/` — CLI client implementation
- `./0002-database-source-of-truth.md` — enforces DB discipline
