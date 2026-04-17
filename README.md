# Jabali Panel (jabali2)

Greenfield rewrite of Jabali Panel — a web hosting control panel for WordPress
and PHP hosting. React (Refine + Ant Design) frontend + Go (Gin) backend,
privileged root ops off-loaded to a separate agent over a Unix socket.

See [`docs/BLUEPRINT.md`](docs/BLUEPRINT.md) for the full feature map and
[`docs/adr/`](docs/adr/) for architectural decision records.

## Status

**Phase 2 — Domain lifecycle + Auth enhancements + SSL.** Shipped milestones
(as of 2026-04-17):

- **M1:** Foundations (auth, users, hosting packages)
- **M2:** Domain lifecycle (CRUD, nginx vhosts, redirects, custom rules)
- **M3:** Server settings (hostname, nameservers, public IPs)
- **M4:** DNS zones + records + secondary NS (PowerDNS integration)
- **M5:** SSL / Let's Encrypt (try-ACME-first, self-signed fallback, exponential backoff retry)
- **M5a:** Admin impersonation (one-shot login URL in a new tab)
- **M5b:** Break-glass CLI login (`jabali-panel admin login` / `user login`)

See [`docs/BLUEPRINT.md`](docs/BLUEPRINT.md) for the full roadmap.

## Layout

```
jabali2/
├── panel-api/          # Go HTTP server (Gin) + reconciler + agent RPC client
│   ├── cmd/server/     # main entry
│   ├── internal/       # api/, auth/, repository/, reconciler/, config/, ...
│   └── migrations/     # golang-migrate SQL
├── panel-agent/        # Go binary running as root; handler registry for privileged ops
│   ├── cmd/jabali-agent/
│   └── internal/commands/
├── panel-ui/           # React SPA (Refine + Ant Design + TanStack Query)
│   ├── src/            # shells/, components/, theme/, pages/, ...
│   └── public/
├── agentwire/          # NDJSON RPC types shared by panel-api and panel-agent
├── docs/               # BLUEPRINT + ADRs + runbooks
├── install.sh          # single-supported install path (curl | bash)
├── config.example.toml # reference config (copy to /etc/jabali/config.toml)
├── .env.example        # env var reference (overrides config.toml)
├── Makefile            # build / test / lint targets
└── go.mod              # Go workspace root
```

## One-line install (fresh Debian 13 / Ubuntu 24.04 server)

```bash
curl -fsSL https://git.linux-hosting.co.il/shukivaknin/jabali2/raw/branch/main/install.sh | bash
```

Installs Go 1.25, creates a `jabali` service user, builds the binaries, writes
systemd units, starts them, and smoke-tests `/health`. Idempotent — re-run to
upgrade.

## Dev quickstart

<!-- AUTO-GENERATED:make-targets — regenerate via /update-docs -->
| Target | Description |
|--------|-------------|
| `make build` | Compile both binaries (panel + agent) |
| `make run` | Run the panel server (dev) |
| `make test` | Run all Go tests across the workspace (race detector on) |
| `make test-short` | Run only fast unit tests (skip integration) |
| `make test-coverage` | Run tests with coverage (internal packages only) |
| `make test-integration` | Run integration tests (requires `JABALI_TEST_DATABASE_URL` + real MariaDB) |
| `make coverage-check` | Fail if combined (unit + integration) coverage below 80% |
| `make lint` | Run golangci-lint across the workspace |
| `make fmt` | Format all Go code |
| `make vet` | Run `go vet` |
| `make tidy` | Tidy module deps |
| `make clean` | Remove build artefacts |
<!-- /AUTO-GENERATED -->

```bash
make build
make run
curl -s http://localhost:8443/health
# {"status":"ok"}
```

Frontend dev (from `panel-ui/`): `npm install && npm run dev` → <http://localhost:5173>
(the Vite dev server proxies `/api` and `/health` to `127.0.0.1:8443`).

## Requirements

- **Go** 1.25+ (pinned in `go.mod`; `install.sh` will fetch `1.25.1` on a clean box)
- **Node** 20+ (for `panel-ui/`; required to build the embedded SPA)
- **golangci-lint**: `go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@latest`
- **MariaDB** 10.11+ (Phase 3+; the panel stores everything in one `jabali_panel` DB, PowerDNS uses a separate `jabali_pdns` DB)

For contributor setup and the feature development workflow, see
[`docs/CONTRIBUTING.md`](docs/CONTRIBUTING.md). For environment variables, see
[`docs/ENV.md`](docs/ENV.md).
