# ADR-0083 — Shared ops packages for REST + CLI code reuse (M44)

**Status:** Accepted — 2026-04-29
**Deciders:** shuki (plan + review), ADR target noted in M44 plan and BLUEPRINT.md
**Related:** ADR-0003 (one write path — CLI is thin HTTP client for user-facing ops), ADR-0001 (privileged ops via Go agent)

## Context

M44 added operator CLI commands that mirror REST API resources: database
create/delete, cron add/remove/list, SSH key add/remove. The REST handlers
already contained 218–660 lines of non-trivial validation, ULID generation,
agent dispatch, and rollback logic. The naïve CLI implementation strategy —
copy-paste the logic into cobra commands — guarantees validation drift: a
constraint added to the handler never makes it into the CLI, and an operator
running `jabali db create` gets different semantics than a panel user clicking
the same action in the UI.

ADR-0003 says "CLI subcommands are thin HTTP clients, not peers to the API"
for the *user-facing* panel CLI (i.e. commands that ultimately call the
panel REST API). That rule applies to the external API surface. Operator
commands that run *inside* the same process (or on the same host with
direct DB access) are a different domain: making them HTTP-roundtrip through
the panel would require a running panel, working auth, and the correct port —
all potentially absent on a broken installation where the operator is using
`jabali db create` to recover.

The correct boundary: share the **implementation**, not the HTTP layer.

## Decision

Extract the core validation and orchestration logic for each shared domain
into a dedicated internal ops package:

| Package | Covers |
|---|---|
| `panel-api/internal/dbops/` | Database create, delete, plan/quota checks |
| `panel-api/internal/cronops/` | Cron add, remove, list, expression validation |
| `panel-api/internal/sshkeyops/` | SSH key add, remove, list, key-type validation |

Both the REST handler (in `panel-api/internal/api/`) and the cobra CLI
command (in `panel-api/internal/commands/` or the top-level `cmd/`) become
**thin wrappers** that:

1. Parse and validate the transport-layer input (JSON body or CLI flags).
2. Map to a typed request struct defined in the ops package.
3. Call the ops package function (e.g. `dbops.Create(ctx, db, req)`).
4. Translate the ops result to the appropriate response format (JSON envelope
   for HTTP, human-readable or `--json` flag output for CLI).

The ops package itself has no HTTP, no cobra, no `os.Exit`. It takes a
`context.Context` + a GORM `*gorm.DB` (or a small interface for testability)
and returns a result struct + error. All quota checks, ULID generation, agent
dispatch calls, and cascade cleanup live in the ops package.

## Status at time of ADR

`dbops` shipped in commit `0a8fd3c3`: `databases.go` (REST handler) and
`db_cmd.go` (cobra command) are already thin wrappers over
`panel-api/internal/dbops/`. `cronops` and `sshkeyops` follow the same shape
as the remaining M44 waves complete.

## Alternatives Considered

### Alt 1: CLI as thin HTTP client (extend ADR-0003 to operator commands)

- **Pros:** Zero code duplication; REST API is the single contract.
- **Cons:** Requires a running, authenticated panel. The operator CLI is a
  *recovery tool* — it is most useful when the panel is down or misconfigured.
  Round-tripping through HTTP also means the CLI cannot work during
  initial provisioning before the panel first-boot sequence completes.
- **Why not:** Recovery-context requirement breaks the assumption.

### Alt 2: Duplicate logic in each CLI command

- **Pros:** No new package; each command self-contained.
- **Cons:** Every constraint added to the REST handler must be duplicated
  in the CLI. Empirically, validation drift causes operator commands to
  accept inputs the API rejects, or vice versa. M44 plan notes 218–660 LOC
  of validation already written; duplicating it is a maintenance liability.
- **Why not:** Drift is load-bearing; the risk materializes quickly in a
  multi-developer, multi-worktree codebase.

### Alt 3: REST handler calls CLI package (invert dependency)

- **Pros:** One package, CLI is canonical.
- **Cons:** CLI conventions (flag parsing, `os.Exit`) bleed into the HTTP
  path. JSON-serialisable errors become awkward. cobra's `RunE` contract
  doesn't map cleanly to `http.Handler`.
- **Why not:** Wrong dependency direction; cobra artefacts in the HTTP path.

## Consequences

### Positive

- Validation logic lives in exactly one place. A quota rule added to `dbops`
  is automatically enforced by both the REST API and the `jabali db create`
  CLI with no extra work.
- Ops packages are testable with zero HTTP overhead: `dbops.Create` takes a
  `*gorm.DB` (or mock) and returns a typed result. Unit tests run in-process.
- REST handlers shrink to transport glue (~30–60 LOC); cobra commands are
  similarly thin.

### Negative

- New package boundary to maintain. Each new resource type that needs CLI
  exposure requires a new `<area>ops/` package (or extension of an existing
  one).
- Ops functions must remain transport-agnostic (no `c *gin.Context`, no
  `cmd *cobra.Command`). Discipline required to keep HTTP/CLI artefacts out.

### Risks

- Ops packages accumulate scope if reviewers don't enforce the "no transport"
  rule. Mitigation: `golangci-lint` import restrictions via `.golangci.yml`
  (no `net/http` import in `internal/*ops/`).
