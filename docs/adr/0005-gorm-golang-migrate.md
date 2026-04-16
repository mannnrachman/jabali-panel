# 0005 — GORM for ORM, golang-migrate for schema

## Status
Accepted — 2026-04-16

## Context
Data persistence requires an ORM and a schema versioning tool. GORM is already in place, familiar, and adequate for single-node workloads. golang-migrate (file-based .up.sql/.down.sql) is simpler than embedded migrations and keeps schema under version control.

## Decision
Use GORM for CRUD, association preloads, and lifecycle hooks. Use raw SQL only for hot paths and bulk inserts. Migrations are `.up.sql` + `.down.sql` files under `panel-api/internal/db/migrations/`, with `multiStatements=true` for multi-statement migration files.

## Consequences

### Positive
- GORM handles common patterns (soft delete, timestamps, associations) automatically
- golang-migrate is a separate tool; schema is plain SQL and version-controlled
- Rollback is explicit and auditable
- No code generation overhead (vs sqlc)

### Negative
- GORM adds query overhead (not suitable for sub-millisecond tight loops)
- Raw SQL in GORM queries bypasses some type safety
- golang-migrate requires file discipline; migration naming must be strict

### Neutral
- Manual down.sql writing required (no auto-generation)

## Alternatives considered

- **sqlc**: Rejected — too rigid for evolving schema, requires codegen, slower iteration
- **Hand-rolled query builder**: Rejected — error-prone, wastes time on boilerplate

## References
- `panel-api/internal/repository/` — GORM models and repositories
- `panel-api/internal/db/migrations/` — .sql migration files
- `panel-api/cmd/cli/migrate.go` — CLI migration runner
