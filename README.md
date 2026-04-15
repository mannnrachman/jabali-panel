# Jabali Panel (jabali2)

Greenfield rewrite of Jabali Panel — a web hosting control panel for WordPress
and PHP hosting. React (Refine + Ant Design) frontend + Go (Gin) backend.

See [`BLUEPRINT.md`](../BLUEPRINT.md) for the full feature map.

## Status

**Phase 1 — repo skeleton.** This is the first focused vertical slice:

1. Skeleton + tooling (this phase)
2. Config loader + logger
3. DB layer (Postgres via golang-migrate + GORM)
4. Auth (JWT + refresh rotation)
5. Middleware (CORS, rate-limit, request-ID, JWT, RBAC)
6. Users CRUD
7. AgentClient (Unix socket + mock)
8. React skeleton (Vite + Refine + AntD)
9. Users page (admin-only CRUD)
10. Test harness + coverage gates

## Layout

```
jabali2/
├── panel-api/                    # Go backend
│   ├── cmd/server/               # main entry
│   ├── internal/
│   │   ├── api/                  # HTTP handlers (health, users, auth)
│   │   ├── app/                  # wiring + lifecycle
│   │   └── config/               # .env + config.toml loader (Phase 2)
│   └── migrations/               # golang-migrate SQL (Phase 3)
└── panel-ui/                     # React SPA (Phase 8)
```

## Dev quickstart

```bash
make run            # start the server (currently just /health)
make test           # run tests with race detector
make lint           # run golangci-lint
make test-coverage  # generate coverage.out
make coverage-check # fail if coverage < 80%
make help           # list all targets
```

Health check:

```bash
curl -s http://localhost:8443/health
# {"status":"ok"}
```

## Requirements

- Go 1.24+
- Node 20+ (later phases)
- `golangci-lint` (install: `go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@latest`)
- Docker OR a reachable Postgres (needed from Phase 3 onward)
