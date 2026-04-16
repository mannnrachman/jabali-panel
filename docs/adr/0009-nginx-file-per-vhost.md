# 0009 — Nginx file-per-vhost with force-regen path

## Status
Accepted — 2026-04-16

## Context
Nginx configuration can be centralized (one file for all vhosts) or split (one file per vhost). Per-vhost is modular and easier to regenerate. A force-regen command lets operators rebuild all configs from the DB if drift occurs.

## Decision
Each domain has `/etc/nginx/sites-available/<domain>.conf`. The reconciler regenerates from the DB template. Operators can force a full regeneration via `jabali nginx regenerate --force`, which rewrites all vhost files atomically and validates syntax before reload.

## Consequences

### Positive
- Vhost isolation (one domain doesn't crash others if config is malformed)
- Modular regeneration (easier testing)
- Force-regen enables manual recovery from drift

### Negative
- Requires atomic reload (sequence `validate → rename → reload`)
- If reload fails partway, operator must debug systemd state

### Neutral
- Generated files should never be edited; intent is in DB only

## Alternatives considered

- **OpenResty runtime config**: Rejected — bigger attack surface, harder to debug, loses file transparency
- **Single monolithic nginx.conf**: Rejected — hard to parallelize; one syntax error blocks all domains

## References
- `panel-api/internal/reconciler/domain.go` — nginx regenerator
- `panel-api/cmd/cli/nginx.go` — `nginx regenerate --force` subcommand
