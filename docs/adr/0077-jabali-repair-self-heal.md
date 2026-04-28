# ADR-0077: `jabali repair` — host-state self-heal subcommand

**Date:** 2026-04-28
**Status:** Accepted
**Deciders:** shuki (operator/architect)
**Related:** ADR-0010 (install via `curl|bash`), `jabali update` (`panel-api/cmd/server/update.go`)

## Context

`jabali update` is the only sanctioned way to upgrade an installed host.
It is intentionally narrow: fetch → reset → build → migrate → restart.
That narrow scope makes it deterministic on a healthy host but brittle
when host state has drifted in any of the recurring ways we have seen
across M22, M25, M29, M30, M32, M33:

- `/opt/jabali-panel/.git` becomes a worktree-pointer FILE (`gitdir:
  <abspath>`) instead of a directory after a partial rsync from a dev
  box's worktree. Every git command then dies with `fatal: not a git
  repository: <abspath>`.
- `.git` ownership flips to `root` after a hand-run `git fetch` as root,
  blocking the next `jabali update`'s `git fetch` (run as `jabali`) with
  permission-denied or "dubious ownership" errors.
- `panel-ui/node_modules` ends up in a partial state (`.bin/tsc`
  missing) after an interrupted `npm ci`. `npm ci` retries on the next
  update but silently produces the same partial result on heavy trees.
- Stale `ondrej/nginx` PPA on Ubuntu noble returns 404 on every
  `apt-get update`, blocking dependency installs.
- `/var/lib/jabali-uploads` missing after partial install state, breaking
  uploads silently.
- `systemctl daemon-reload` not run after `install -m 0644` of a unit
  file; the new unit is on disk but systemd is still running the old.

Each of these has a known one-line fix. Today an operator who hits one
must read the runbook, find the matching scar, and copy-paste the fix.
That assumes they know the runbook exists and can identify which scar
they hit from the error message — neither holds for first-time
operators or for a panic-time recovery on a production host.

## Decision

Ship a `jabali repair` cobra subcommand that:

1. Detects each known host-state scar via a small, idempotent
   detector function (no IO mutations).
2. Reports findings via `--diagnose` (the safe default when no mode
   flag is given) or fixes them via `--auto` (non-destructive only),
   `--all --yes` (everything), or `--<id>` (specific repair).
3. Returns a non-zero exit code only on detector errors or fix
   failures — a clean diagnose with no findings exits 0.
4. Lives in `panel-api/cmd/server/repair.go` next to `update.go` so a
   new scar discovered while editing `update.go` can be added to
   `repairSteps()` in the same patch.

`jabali update`'s error path appends a short hint pointing the operator
at `jabali repair --diagnose` whenever any update step fails.

### Repair step list (v1)

| ID | Destructive | What it fixes |
|----|-------------|---------------|
| `git-pointer` | yes | `.git` is a worktree-pointer file → re-clone preserving node_modules / .cache / .env / bin |
| `git-ownership` | no | `.git` not owned by service user → `chown -R` |
| `git-stale-worktrees` | no | `.git/worktrees/<name>` from a dev box → `git worktree prune --expire now` |
| `uploads-dir` | no | `/var/lib/jabali-uploads` missing → mkdir + chown root:jabali 0750 |
| `ondrej-nginx-ppa` | no | stale ondrej apt source → unlink |
| `node-modules` | yes | `node_modules/.bin/tsc` missing despite `package-lock.json` → rm -rf node_modules |
| `daemon-reload` | no | any `jabali-*` unit reports `NeedDaemonReload=yes` → `systemctl daemon-reload` |

### Safety model

- `--auto` runs every non-destructive repair without confirmation. The
  destructive set (re-clone, rm -rf node_modules) is excluded.
- `--all` runs every repair. Destructive ones prompt interactively
  (y/N) unless `--yes` is also passed.
- `--<id>` overrides the destructive gate when paired with `--yes`,
  letting CI/scripted recovery target a single fix.
- No mode flag → defaults to `--diagnose`. Operators who type
  `jabali repair` to "see what it does" must explicitly opt into a mode
  before any state changes.

### Why a separate subcommand and not part of `jabali update`?

- Surface clarity. `jabali update` MUST be a clean fast-path on a healthy
  host. Mixing repair logic into the update path makes it slow, noisy,
  and hard to reason about which step actually fixed which thing.
- Operator intent. Most update failures want a fix-and-rerun cycle,
  not a fix-and-merge. Exposing repair as a separate command lets the
  operator read the diagnose output, decide what they want fixed, and
  re-run update afterwards.
- Destructive guardrails. Running `--diagnose` by default and gating
  destructive repairs behind explicit flags is incompatible with the
  way `jabali update` runs unattended in transient systemd units.

### Adding a new repair

1. Add a `repairStep` entry to `repairSteps()` with `id`, `label`,
   `destructive`, `detect`, `fix`.
2. The detector must NOT mutate host state. The fix must be
   idempotent (calling it twice when not broken must succeed).
3. The label appears in `--diagnose` output and in the per-fix flag's
   help text — keep it under one terminal line.

## Alternatives considered

### Auto-repair inside `jabali update`

Detect the scars in `update.go` and self-heal silently. Rejected: a
silent re-clone of `/opt/jabali-panel` during routine update is too
big a footgun for the case where the diagnosis is wrong.

### Bash script under `install/repair.sh`

Distribute repairs as a shell script. Rejected: the existing scars
are detected most accurately from Go (stat + readfile + systemctl
property reads with proper error handling), and we already require the
operator to have the panel binary installed when their update fails —
they'd have to download a separate script otherwise.

### One repair per CLI subcommand

`jabali repair-git`, `jabali repair-node-modules`, etc. Rejected:
proliferation of top-level subcommands hurts discoverability; the
shared `--diagnose / --auto / --all` mental model is the value-add.

## Consequences

### Positive

- First-time operator hitting any of the seven scars now has a single
  command to recover.
- Future scars get a 30-line repairStep entry and ship in the next
  update without touching update.go's flow.
- `--diagnose` doubles as a "is this host healthy?" check that can
  be hooked into monitoring later.

### Negative

- Repair list is a separate place that drifts from install.sh truth
  if someone adds a system-state expectation in install.sh without
  also adding a detector here. Mitigation: check in code review.

### Risks

- `git-pointer` re-clone preserves only an explicit allowlist
  (`.env`, `node_modules`, `dist`, `.cache`, `bin`). Anything else
  the operator stashed under `/opt/jabali-panel/` outside git is left
  in `<repo>.broken/` for them to recover. The broken tree is not
  auto-deleted — operator must remove it once they're sure the new
  clone works.
