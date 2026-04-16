# ADR 0001 — Control plane model

**Status:** Accepted (2026-04-16)

## Context

Jabali Panel has three moving parts that touch state:

- **MariaDB** via `panel-api` — stores users, packages, domains
- **Filesystem** on the host — nginx configs, PHP-FPM pools, `/home/<user>` dirs
- **`panel-agent`** — a stateless root-level executor listening on a Unix socket

Until this ADR, the system had two problems:

1. **Split-brain writes.** Both the HTTP API handlers AND the `jabali-panel` CLI
   subcommands were writing directly to the DB and invoking the agent. The CLI
   bypassed auth, input validation, quota enforcement, and audit. Two peer write
   paths → drift guaranteed.

2. **No reconciliation.** Handlers called the agent inline. On agent failure
   we either rolled back the DB (strict mode) or marked `is_enabled=false` and
   returned a warning (best-effort, added in `40e62c9`). Neither recovered the
   state automatically. Orphaned nginx configs after a failed delete, missing
   vhosts after a failed create — silent drift forever.

## Decision

**The MariaDB row is the declarative source of truth. The filesystem is derived
state. A reconciler converges derived state toward declared state.**

Four rules, in order of importance:

### 1. One write path: the HTTP API

Every mutation enters through `panel-api`'s HTTP handlers where auth,
validation, quota, and audit live. The CLI (`jabali-panel user/package/domain`)
is now a thin HTTP client that mints a short-lived admin JWT from the local
`JWT_SECRET` and talks to `https://127.0.0.1:<port>`. It is not a peer to the
API — it is a caller.

Exceptions (allowed to touch DB/agent/FS directly, because there is no API
to call yet or they bootstrap the API):

- `jabali-panel serve` — the API itself
- `jabali-panel migrate` — schema migrations
- `jabali-panel update` — self-update (git pull, rebuild, restart)
- `jabali-panel system` — read-only diagnostics
- future `jabali-panel recover` — FS-from-DB rebuild when the API is down

### 2. DB first, agent second

Handlers write the DB row first, then schedule reconciliation. They do not
call the agent inline. The HTTP response returns as soon as the DB write
commits; OS-level provisioning happens out-of-band.

```go
// write intent
domain := ...
domains.Create(ctx, domain)
// hand off to reconciler
reconciler.Schedule(domain.ID)
return 201
```

Rationale: the DB is what the user just declared. The filesystem is what the
system should make true. Returning the DB state is honest; waiting on the
filesystem couples request latency to `useradd`/`nginx reload` which we don't
control.

### 3. Reconciler owns convergence

`internal/reconciler` runs a single loop that:

- Fires on `Schedule(id)` for targeted reconciliation (fast path after CRUD)
- Fires on a timer (every `ReconcilerInterval`, default 60s) for periodic drift
  detection via `ReconcileAll`

`ReconcileAll` asks the agent for `domain.list` (names in `sites-enabled`)
and diffs against the DB:

- Enabled-in-DB, missing-on-agent → `domain.create` (retry on next tick if it fails)
- Disabled-in-DB, present-on-agent → `domain.disable`
- Present-on-agent, absent-in-DB → **log only** (orphan; humans decide)
- Matched both sides → no-op

Deletion is a special case: once the DB row is gone, `ReconcileOne(id)` can't
find it. The DELETE handler therefore captures the domain name *before* the
DB delete and calls `ReconcileDeleted(ctx, name)` to explicitly tell the agent
to tear down.

### 4. Filesystem is disposable

A consequence of rule 3, but worth stating: if `/etc/nginx/sites-enabled/*`
is wiped, the next reconciler tick rebuilds everything from the DB. If a
`/home/<user>` directory is missing, the reconciler can re-provision it (once
we wire that path). We never treat the filesystem as authoritative.

Orphans (FS without a DB row) are the exception. We log them instead of
auto-deleting because the DB could be wrong — e.g., a migration rollback lost
a row the user cares about. A future `jabali-panel reconcile --prune-orphans`
subcommand will let ops opt in to auto-cleanup.

## Consequences

### Positive

- Single chokepoint for auth/validation/audit
- Self-healing after transient agent failures
- Clean disaster recovery path (FS rebuild from DB)
- Testable: reconciler has a fake-agent interface; unit tests cover diffs
- Readable request flow: handler = intent, reconciler = effect

### Negative / tradeoffs

- Responses are eventually consistent on the FS side. `POST /domains` returns
  201 before nginx knows. Frontend has to show "provisioning…" or poll.
- Orphan detection is log-only by default; ops has to notice.
- Reconciler is a single goroutine today. Under load we'd want a worker pool
  or per-domain serialization. Not a problem yet; flagged for future.
- `domain.list` agent command has to stay cheap (it fires every 60s).
  Implementation reads `/etc/nginx/sites-enabled/` directly — fine at O(1000s)
  of domains, revisit above that.

## Related

- Commit `40e62c9` — best-effort agent calls in CRUD (the stopgap this ADR
  replaces)
- Commit `a904f60` — reconciler integration
- `panel-api/internal/reconciler/` — implementation
- `panel-api/internal/clientapi/` — HTTP client used by CLI
- `panel-api/cmd/server/cli_token.go` — short-lived admin JWT minting
