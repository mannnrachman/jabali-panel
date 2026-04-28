# M41 — Operator CLI extensions (db / cron / sshkey CRUD)

**Goal.** Add `jabali db`, `jabali cron`, and `jabali sshkey` cobra
subcommands so an operator can seed databases, cron jobs, and SSH
keys from the panel host's CLI without a browser session — the same
M20-safe pattern already used for `jabali user`, `jabali domain`,
`jabali app`, `jabali mailbox`, and `jabali package`.

Driver: bootstrap scripts (e.g. `~/jabali-test-bootstrap.sh`) that
seed a test account need every feature surface reachable from CLI.
Today the bootstrap script ends with a "TODO via UI (no CLI yet)"
list for these three.

Branch: `m41/operator-cli-extensions`. Default mode: branch + ff-merge
into `main` after every step.

ADR target: **0083** (next free above ADR-0082 ghost domain detector;
M34/M35/M36/M37 plans hold 0078..0081 as targets).

No new migrations. M41 is pure CLI + refactor work.

## Why a refactor, not a copy-paste

Each of the three target REST handlers is non-trivial:

| Handler | LOC | Validation work |
|---------|-----|-----------------|
| `internal/api/databases.go` | 660 | engine routing (mariadb today, postgres in M37), name + grant validation, agent dispatch, ULID generation, FK coupling to users + apps |
| `internal/api/cron.go`      | 489 | command allow-list per ADR-0029 (wp-cli, php, curl, scoped paths), schedule format validation, systemd-user-timer materialisation via agent, internal/cronvalidate shared with the agent |
| `internal/api/ssh_keys.go`  | 218 | OpenSSH key parsing, fingerprint + algorithm checks, label uniqueness per user, agent.ssh.authorized_keys.write atomic-rename |

Copy-pasting that validation into a cobra subcommand for each guarantees
drift the next time a REST handler picks up a new check. M41 takes the
slower, correct path: extract the create / update / delete flows into
shared `internal/<area>ops/` packages and have BOTH the REST handler
and the CLI call them. ADR-0083 captures the rationale.

## Constraints + invariants

- **Single source of truth = ops package.** REST handler becomes
  a thin "decode JSON → call ops.Create → render JSON" wrapper.
  CLI cobra command becomes a thin "decode flags → call ops.Create
  → render text/JSON" wrapper. Both share the validation, agent
  dispatch, and DB write paths.
- **No behaviour change in REST surface.** Existing E2E + handler
  tests must pass unmodified after the extraction.
- **CLI mirrors REST resource shape.** `jabali db create --user
  --name --engine` produces the same row a `POST /admin/databases`
  with the same fields would, with the same agent calls + same
  validation errors mapped to non-zero exit codes.
- **JSON output flag respected.** `jabali db list --json` produces
  the same envelope as `GET /admin/databases` minus pagination
  metadata.
- **No new repo methods.** Existing `database_repository.go`,
  `cron_job_repository.go`, `ssh_key_repository.go` are sufficient.
- **Agent contracts unchanged.** `db.create`, `db.user_create`,
  `cron.apply`, `ssh.authorized_keys.write` already exist.
- **Allow-list for cron commands stays in
  `internal/cronvalidate/`** — already shared between panel-api
  and panel-agent. CLI call path goes through the same validator.
- **No CLI for db engine = postgres yet** — locked behind M37
  PostgreSQL parity. CLI rejects `--engine postgres` with a clear
  "M37 deferred" error.

## Steps

### Step 1: WAVE GATE — extract internal/dbops/ + internal/cronops/ + internal/sshkeyops/

**Files:**
- `internal/dbops/create.go`, `delete.go`, `list.go`, `user_create.go`,
  `user_grant.go`, `errors.go`
- `internal/cronops/create.go`, `delete.go`, `list.go`, `errors.go`
- `internal/sshkeyops/add.go`, `delete.go`, `list.go`, `errors.go`
- ops contracts (per package):
  ```go
  type CreateInput struct {
      UserID string
      Name   string
      Engine string // mariadb only in v1
  }
  type CreateResult struct {
      ID   string
      Name string
  }
  func Create(ctx context.Context, deps Deps, in CreateInput) (CreateResult, error)
  ```
- Refactor `panel-api/internal/api/databases.go` etc. to call ops.

Wave gate decision: dispatcher reviews this step before Steps 2-4
dispatch. Specifically: ops package signatures + Deps struct +
error mapping (which ops error → which HTTP status, which CLI exit
code). Steps 2-4 build on all three.

**Verify:** every existing REST + repo test passes unmodified;
`go vet ./...` + `go test -race ./...` clean.

### Step 2: jabali db cobra subcommand

**Files:**
- `panel-api/cmd/server/db_cmd.go` — `db` parent + subcommands:
  - `db create --user --name [--engine mariadb]`
  - `db list [--user]`
  - `db delete <db-name-or-id>`
  - `db user create --user --db --username --password --grant rw|ro`
  - `db user delete <username>`
  - `db user grant --db --username --grant rw|ro|none`
- `panel-api/cmd/server/db_cmd_test.go` (smoke tests with a sqlite
  fixture per existing pattern)

**Verify:** `bash ~/jabali-test-bootstrap.sh` reaches a `jabali db
create --user X --name wp_test` line and lands a row with the same
shape `POST /admin/databases` would have produced.

### Step 3: jabali cron cobra subcommand

**Files:**
- `panel-api/cmd/server/cron_cmd.go`:
  - `cron create --user --name --schedule --command [--enabled]`
  - `cron list [--user]`
  - `cron delete <cron-id>`
  - `cron run-now <cron-id>` (wraps the existing
    `cron.run_now` agent command)
- Reuses `internal/cronvalidate/` allow-list — invalid `--command`
  arg surfaces the same error string the REST handler returns.

**Verify:** disallowed command (`--command 'nc -lvp 9000'`) exits
non-zero with the cronvalidate reason, no row written, no agent call.

### Step 4: jabali sshkey cobra subcommand

**Files:**
- `panel-api/cmd/server/sshkey_cmd.go`:
  - `sshkey add --user --label [--key '<openssh-pubkey>' | --stdin]`
  - `sshkey list [--user]`
  - `sshkey delete <key-id>`
- `--stdin` reads the public key from stdin so a real key can be
  piped from a file without quoting hell:
  ```
  cat ~/.ssh/id_ed25519.pub | jabali sshkey add --user X --label laptop --stdin
  ```

**Verify:** `jabali sshkey add` for a freshly-created user lands a
row + writes the user's `~/.ssh/authorized_keys` (via existing
`ssh.authorized_keys.write` agent command); `ssh -i <priv-key>
user@host` succeeds for the SFTP gateway.

### Step 5: bootstrap script + runbook update

**Files:**
- Update `/home/shuki/jabali-test-bootstrap.sh` (and the mx copy)
  to use the new CLIs instead of the "TODO via UI" stub.
- `plans/m41-operator-cli-extensions-runbook.md` covers:
  - command surface table (every flag, every subcommand)
  - parity table (CLI → REST endpoint)
  - error-mapping table (ops error → CLI exit code)
  - the bootstrap script as the canonical end-to-end smoke test

## Out of scope

- New REST endpoints — M41 is a refactor + CLI surface, not a
  feature expansion.
- PostgreSQL CLI surface — gated behind M37; CLI rejects
  `--engine postgres` until M37 lands.
- IMAP / autoresponder / forwarder CLI — already shipped under
  M6.5 (`jabali mailbox forwarder` / `jabali mailbox
  autoresponder`).
- Per-table grants (`db user grant --table`) — locked behind the
  M7 ADR-0019 deferral.
- Bulk operations (`jabali db delete --all-for-user`) — risk of
  footgun outweighs convenience; explicit per-row delete only.
- Dump/restore CLI — `jabali backup` covers this.

## Numbering note

ADR target 0083 assumes M34 (0078), M35 (0079), M36 (0080),
M37 (0081), M38 (0082) all ship before M41. If any of those slip,
M41's ADR renumbers down to fill the gap (whichever ships, takes the
next free number).
