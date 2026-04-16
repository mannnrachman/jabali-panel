# 0001 — Go agent over NDJSON Unix socket

## Status
Accepted — 2026-04-16

## Context
Panel needs a privileged agent to manage system resources (user creation, domain config, SSL provisioning). The legacy system used a PHP agent via HTTP POST. The new greenfield design must be secure, minimal-surface, and language-coherent.

A local Unix domain socket avoids network exposure and eliminates the need for distributed authentication. NDJSON (newline-delimited JSON) is trivial to parse, single-threaded safe, and human-debuggable.

## Decision
The `panel-agent` is a Go binary (`jabali-agent`) running as root, listening on `/run/jabali/agent.sock` (Unix domain socket). Communication protocol: one JSON object per line, request-response pairs. The `panel-api` is the sole caller; no other services speak directly to the agent.

## Consequences

### Positive
- Single language stack (Go only); no runtime dependencies beyond libc
- Unix socket prevents accidental network exposure
- NDJSON is line-buffered, human-debuggable with `nc -U` or `socat`
- Easier privilege isolation: agent runs root, API runs unprivileged user
- In-process command registry (no .so plugins) reduces attack surface

### Negative
- Unix socket limits to single node (no remote agent for now)
- NDJSON requires strict line discipline; malformed JSON stalls the stream
- No built-in RPC framework; must hand-craft request/response envelopes

### Neutral
- Requires `systemd` socket activation or manual socket creation
- Debugging requires knowledge of `strace -p` or `socat` utilities

## Alternatives considered

- **PHP agent via HTTP POST**: Rejected — legacy, requires PHP runtime, higher attack surface, slower
- **gRPC**: Rejected — overkill for local socket, requires protobuf codegen, harder to debug
- **Named pipe (FIFO)**: Rejected — no acknowledgment semantics; Unix socket is standard for this pattern

## References
- `panel-agent/cmd/jabali-agent/main.go` — agent binary entry point
- `panel-api/internal/agent/client.go` — API-side socket client
- `./0003-one-write-path-the-api.md` — documents the sole caller discipline
