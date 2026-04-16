# 0010 — Install via `curl | bash` only

## Status
Accepted — 2026-04-16

## Context
Installation can be via deb package, Ansible, Docker, or a shell script. A shell script is portable, requires no package repo infrastructure, and is easy to rollback (just switch git tag). The deb package was removed 2026-03-25.

## Decision
Single installation path: `curl -fsSL https://.../install.sh | bash`. The script is idempotent, reads from `/dev/tty` for interactive prompts (so it works under pipes), and handles systemd enablement. All prerequisites (Go runtime, MariaDB, nginx, certbot, pdns) are installed by the script.

## Consequences

### Positive
- One code path; no apt package divergence
- Easy rollback (switch to a git tag; re-run install.sh)
- No external repo infrastructure needed
- Portable across Ubuntu versions

### Negative
- Operator runs untrusted code (`curl | bash`)
- Script must be idempotent and robust (hard to test)
- No automatic updates (operator must re-run script)

### Neutral
- Requires local code review before running on production

## Alternatives considered

- **deb package**: Rejected — requires apt repo infrastructure, maintenance burden, removed 2026-03-25
- **Ansible role**: Rejected — adds Ansible dependency; forces operator workflow
- **Docker image**: Rejected — not applicable to bare metal

## References
- `install.sh` — installation script
- `docs/install.md` — installation documentation
