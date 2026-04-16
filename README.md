# Jabali Panel (jabali2)

Greenfield rewrite of Jabali Panel — a web hosting control panel for WordPress
and PHP hosting. React (Refine + Ant Design) frontend + Go (Gin) backend.

See [`BLUEPRINT.md`](../BLUEPRINT.md) for the full feature map.

## Status

**Phase 2 — Domain lifecycle + Auth enhancements + SSL.** Shipped milestones (as of 2026-04-17):

- **M1:** Foundations (auth, users, hosting packages)
- **M2:** Domain lifecycle (CRUD, nginx vhosts, redirects, custom rules)
- **M3:** Server settings (hostname, nameservers, public IPs)
- **M4:** DNS zones + records + secondary NS (PowerDNS integration)
- **M5:** SSL / Let's Encrypt (try-ACME-first, self-signed fallback, exponential backoff retry)
- **M5a:** Admin impersonation (log in as user for support/debug)
- **M5b:** Break-glass CLI login (emergency admin access via `/auth/cli-login`)

See [`docs/BLUEPRINT.md`](docs/BLUEPRINT.md) for the full feature roadmap and what's planned next.

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

## One-line install (fresh Debian 13 / Ubuntu 24.04 server)

```bash
curl -fsSL https://git.linux-hosting.co.il/shukivaknin/jabali2/raw/branch/main/install.sh | bash
```

Installs Go 1.25, creates a `jabali` service user, builds the binary, writes a
systemd unit, starts it, and smoke-tests `/health`. Idempotent — re-run to
upgrade.

## Dev quickstart

```bash
make build          # compile both binaries (panel-api + panel-agent)
make run            # start the panel server (dev mode)
make test           # run all Go tests with race detector
make test-short     # run fast unit tests only (skip integration)
make test-coverage  # run tests with coverage (unit tests only)
make test-integration  # run integration tests (requires JABALI_TEST_DATABASE_URL + MariaDB)
make coverage-check # fail if coverage < 80% (requires JABALI_TEST_DATABASE_URL)
make lint           # run golangci-lint across workspace
make fmt            # format all Go code
make vet            # run go vet
make tidy           # tidy module dependencies
make clean          # remove build artifacts
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
