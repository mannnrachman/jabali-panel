# Contributing

Guide for working on the Jabali Panel codebase. The [BLUEPRINT](BLUEPRINT.md)
is the map of *what* ships; this file is the map of *how* to ship it.

## Prerequisites

| Tool | Version | Notes |
|------|---------|-------|
| Go | 1.25+ | Pinned in `go.mod`; `install.sh` fetches `1.25.1` on a clean box |
| Node | 20+ | Required to build `panel-ui/` |
| golangci-lint | v2 | `go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@latest` |
| MariaDB | 10.11+ | Phase 3+; the panel + PowerDNS use separate DBs on the same instance |

Optional for full local coverage: a reachable MariaDB with the test schema.
Without it, `make test-short` still runs (integration suites are skipped).

## First-time setup

```bash
git clone https://git.linux-hosting.co.il/shukivaknin/jabali2
cd jabali2

# Build both Go binaries to bin/
make build

# Run the server (reads config.example.toml + .env if present)
make run

# Verify
curl -s http://localhost:8443/health
```

For the frontend:

```bash
cd panel-ui
npm install
npm run dev                        # http://localhost:5173, proxies /api → :8443
```

## Backend targets

<!-- AUTO-GENERATED:make-targets — regenerate via /update-docs -->
| Target | Description |
|--------|-------------|
| `make build` | Compile both binaries (panel + agent) |
| `make run` | Run the panel server (dev) |
| `make test` | All Go tests across the workspace (race detector on) |
| `make test-short` | Only fast unit tests (skip integration) |
| `make test-coverage` | Tests with coverage (internal packages only) |
| `make test-integration` | Integration tests (requires `JABALI_TEST_DATABASE_URL` + real MariaDB) |
| `make coverage-check` | Fail if combined coverage below 80% |
| `make lint` | `golangci-lint` across the workspace |
| `make fmt` | Format all Go code |
| `make vet` | `go vet` |
| `make tidy` | `go mod tidy` |
| `make clean` | Remove build artefacts |
<!-- /AUTO-GENERATED -->

## Frontend targets

<!-- AUTO-GENERATED:npm-scripts — regenerate via /update-docs -->
Run from `panel-ui/`.

| Script | Description |
|--------|-------------|
| `npm run dev` | Vite dev server on :5173 with `/api` proxy |
| `npm run build` | Type-check (`tsc -b`) + production build to `dist/` |
| `npm run preview` | Serve the production build locally |
| `npm test` | Vitest unit tests (one-shot, CI-mode) |
| `npm run test:watch` | Vitest watch mode |
| `npm run test:e2e` | Playwright E2E tests |
| `npm run test:e2e:ui` | Playwright UI runner |
| `npm run lint` | ESLint; warnings fail the run |
<!-- /AUTO-GENERATED -->

## Testing

Backend uses the standard `go test` with **table-driven tests** and the race
detector. Target is 80%+ unit+integration coverage; `make coverage-check`
enforces it in CI.

Frontend uses **Vitest** for unit tests and **Playwright** for E2E flows.
New features should include both unit and (where a user flow is touched)
E2E coverage.

Integration tests require `JABALI_TEST_DATABASE_URL` pointing at a real
MariaDB; they run automatically when the var is set and are skipped
otherwise. Do **not** mock GORM in integration tests — real-DB behaviour
(migrations, unique-key collisions, isolation) is part of what we're
testing.

## Code style

- **Go:** `gofmt`/`goimports` enforced by `make fmt`; `golangci-lint run` must pass.
  Accept interfaces, return structs. Wrap errors with context via `fmt.Errorf("…: %w", err)`.
  See [`~/.claude/rules/golang/`](../../.claude/rules/golang/) for the full house style.
- **TypeScript:** strict mode. Prefer `interface` for object shapes, `type` for unions
  and mapped types. Avoid `any`; use `unknown` + narrowing. Immutable updates via
  spread, never in-place mutation.
- **No hardcoded secrets** in source; always env-var / config-file driven.
  Validate at startup that required secrets are set.
- **Every agent command argument** flows through safe escaping before it hits the shell —
  see `panel-agent/internal/commands/`.

## Architectural guardrails

Before adding a feature, check whether it conflicts with any of the
[ADRs](adr/README.md). The load-bearing ones:

- [0002](adr/0002-database-source-of-truth.md) — **database is the source of truth**;
  filesystem state is derivative, rebuilt by the reconciler.
- [0003](adr/0003-one-write-path-the-api.md) — **one write path**; CLI subcommands
  are thin HTTP clients, not peers to the API.
- [0001](adr/0001-go-agent-over-ndjson-unix-socket.md) — **no PHP agent ever**.
  Privileged ops go through the Go agent over the Unix socket.

If your change violates an accepted ADR, open a new ADR first.

## PR checklist

Before requesting review:

- [ ] `make test` passes locally (race detector on)
- [ ] `make lint` passes
- [ ] `cd panel-ui && npm test && npm run lint` passes (for UI changes)
- [ ] Coverage on new code ≥ 80% (check with `make test-coverage`)
- [ ] No hardcoded secrets; any new env var documented in [`ENV.md`](ENV.md) and `.env.example`
- [ ] Any new agent command has argument-sanitisation tests
- [ ] If this touches a shipped milestone, update [`BLUEPRINT.md`](BLUEPRINT.md)
- [ ] If this introduces a new architectural rule, write an ADR

## Commit style

Conventional commits (`feat:`, `fix:`, `refactor:`, `docs:`, `test:`,
`chore:`, `perf:`, `ci:`). Scope optional but useful: `feat(api): …`,
`fix(ui): …`, `docs(blueprint): …`. Body explains *why*, not *what* — the
diff shows the what.

**Never** `git push --force` on `main`. The codebase is shared with other
agents; always `git fetch && git pull --rebase` before pushing.
