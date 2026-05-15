# M44 — Operator CLI extensions runbook

ADR: [0083-shared-ops-packages.md](../docs/adr/0083-shared-ops-packages.md)
Plan: [m44-operator-cli-extensions.md](./m44-operator-cli-extensions.md)

## What it is

`jabali db`, `jabali cron`, `jabali sshkey` cobra subcommands so an
operator can seed databases, cron jobs, and SSH keys from the panel
host CLI without a browser session — same M20-safe pattern as
`jabali user|domain|app|mailbox|package`.

These run **in-process with direct DB + agent access** (ADR-0083):
they do NOT HTTP-roundtrip the panel REST API, so they work on a
half-broken install where the panel/auth/port may be down and the
operator is recovering.

## Command surface

### `jabali db`

| Command | Flags | Notes |
|---------|-------|-------|
| `db create` | `--user --name [--engine mariadb]` | `--engine postgres` rejected (M37 deferred) |
| `db list` | `[--user] [--json]` | |
| `db delete` | `<db-name-or-id>` | cascades grants on the MariaDB side first |
| `db user create` | `--user --db --username --password --grant rw\|ro` | |
| `db user delete` | `<username>` | |
| `db user grant` | `--db --username --grant rw\|ro\|none` | |

### `jabali cron`

| Command | Flags | Notes |
|---------|-------|-------|
| `cron create` | `--user --name --schedule --command [--disabled]` | command + schedule + name run through `internal/cronvalidate` (same gate as `POST /admin/cron`) |
| `cron update` | `<job-id> [--name --schedule --command --enable\|--disable]` | re-validates the resulting row |
| `cron list` | `[--user] [--json]` | |
| `cron delete` | `<job-id>` | confirm prompt unless `--json` |
| `cron run-now` | `<job-id>` | wraps the `cron.run_now` agent command |

Allow-list: command first token must be `wp` or `php`.
`php` standalone flags (`-v -m -i -h --version --modules --info
--help -r`) need no docroot. `wp <sub> --path=<abs-docroot>` and
`php <abs-docroot>/<file>.php` require the path to be one of the
user's owned domain docroots. Anything else exits non-zero with the
cronvalidate reason and writes NO row + makes NO agent call.

### `jabali sshkey`

| Command | Flags | Notes |
|---------|-------|-------|
| `sshkey add` | `--user --name [--pub-key \| --pub-key-file \| --pub-key-stdin]` | key parsed + fingerprinted via `internal/sshkeys`; RSA <2048 rejected; ed25519/ecdsa/RSA≥2048 only |
| `sshkey list` | `[--user] [--json]` | |
| `sshkey delete` | `<key-id>` | |

`--pub-key-stdin` avoids quoting hell:
```sh
cat ~/.ssh/id_ed25519.pub | jabali sshkey add --user X --name laptop --pub-key-stdin
```

## Parity table (CLI → REST)

| CLI | REST endpoint | Shared validation source |
|-----|---------------|--------------------------|
| `jabali db create` | `POST /admin/databases` | `internal/dbops` (Create) |
| `jabali db delete` | `DELETE /admin/databases/:id` | `internal/dbops` |
| `jabali cron create` | `POST /admin/cron` | `internal/cronvalidate` (Name→Schedule→Command) |
| `jabali cron update` | `PATCH /admin/cron/:id` | `internal/cronvalidate` |
| `jabali sshkey add` | `POST /admin/ssh-keys` | `internal/sshkeys` (ParseAndFingerprint) |

`db` shares the full `dbops` ops package. `cron` and `sshkey` share
their **validators** but call repos/agent directly (no `cronops`/
`sshkeyops` package — see Tech debt).

## Error → exit code

Cobra returns non-zero on any `RunE` error. Specific cases:

| Condition | Behaviour |
|-----------|-----------|
| invalid cron name / schedule / command | non-zero, message `invalid name\|schedule\|command: <cronvalidate reason>`, no row, no agent call |
| RSA key <2048 | non-zero, "RSA key below 2048 bits" |
| unsupported key type | non-zero, "unsupported key type — use ed25519, ecdsa, or RSA ≥2048" |
| duplicate SSH fingerprint | non-zero, "a key with this fingerprint already exists" |
| `db create --engine postgres` | non-zero, M37-deferred error |
| user not found | non-zero, resolver error |

## Smoke test (canonical)

`~/jabali-test-bootstrap.sh` is the end-to-end smoke. Run as root on
the panel host:

```sh
bash ~/jabali-test-bootstrap.sh
```

Per user it now also:
- `jabali db create --user <email> --name wp<N>_extra`
- `jabali cron create --user <email> --name nightly-phpcheck
  --schedule '30 3 * * *' --command '/usr/bin/php -v'`
- `jabali sshkey add --user <email> --name bootstrap-test-key
  --pub-key 'ssh-ed25519 …'`

Idempotent via the `run()` wrapper (`|| true`). Re-running tolerates
"already exists".

### Targeted security check

The cron allow-list bypass that M44 closed (commit 67a398fb) — verify
a disallowed command is rejected with no row written:

```sh
jabali cron create --user test1@example.local \
  --name evil --schedule '* * * * *' --command 'nc -lvp 9000'
# expect: non-zero exit, "invalid command: ..." , no cron row
jabali cron list --user test1@example.local   # must NOT contain 'evil'
```

Unit guard: `panel-api/cmd/server/cron_cmd_test.go::
TestValidateCronInputs_GateIsWired`.

## Tech debt (deferred, not blocking)

Plan Step 1 envisioned `internal/cronops/` + `internal/sshkeyops/`
packages mirroring `internal/dbops/` so REST + CLI share one code
path end-to-end. Only `dbops` was extracted. `cron`/`sshkey` share
the **validators** (`cronvalidate`, `sshkeys`) so the security
invariant holds, but their write paths are duplicated between
handler and cobra command.

Revisit only if real validation drift surfaces — it's a ~1-day
refactor against a green baseline, not a blocker. See ADR-0083 for
the single-source rationale.

## Where to look

| Concern | Path |
|---------|------|
| db CLI | `panel-api/cmd/server/db_cmd.go` |
| cron CLI | `panel-api/cmd/server/cron_cmd.go` |
| sshkey CLI | `panel-api/cmd/server/sshkey_cmd.go` |
| db ops package | `panel-api/internal/dbops/dbops.go` |
| cron validator | `internal/cronvalidate/cron.go` |
| ssh key parser | `panel-api/internal/sshkeys/` |
| cron CLI test | `panel-api/cmd/server/cron_cmd_test.go` |
| Bootstrap smoke | `~/jabali-test-bootstrap.sh` (+ mx copy) |
| ADR | `docs/adr/0083-shared-ops-packages.md` |
