# ADR-0096 — Root web terminal in the admin panel

**Date**: 2026-05-16
**Status**: accepted
**Deciders**: shuki (requested + scope confirmed), Claude (design)
**Related**: ADR-0050 (unix-socket group model), ADR-0067 (M13 SSH sandbox), M14 (notifications)

## Context

Operators want a browser terminal in the admin panel for host ops
without a separate SSH client. Confirmed scope: a **true unrestricted
root shell** (`bash -l` as uid 0), not an allowlisted/sandboxed shell
(M13 already covers per-user sandboxed SSH). This is the single
highest-risk surface in the product — an authenticated-admin RCE by
design — so the controls below are mandatory, not optional.

## Decision

Ship a root PTY exposed over WebSocket, with privilege isolation and
mandatory forensic recording:

- **panel-api never gains root.** It runs as `jabali`; it only upgrades
  the browser WS and pumps opaque bytes. The PTY is spawned by
  **panel-agent** (already root) behind a *second*, dedicated UDS
  (`/run/jabali/agent-pty.sock`, `root:jabali-sockets` 0660 — same
  group-reachability pattern as ADR-0050 sockets).
- **agentwire is request/response JSON-RPC and is NOT reused** for the
  stream. A long-lived bidirectional PTY does not fit it; a separate
  framed UDS protocol carries the session (1-byte opcode: `0x00`
  stdout, `0x01` stdin, `0x02` resize-JSON, `0x03` exit).
- **Off by default.** `server_settings.root_terminal_enabled` (tinyint,
  default 0). When false the mint endpoint 403s and the UI hides the
  tab. Enabling is an explicit, audited operator action.
- **One-shot, bound token.** `terminal_sessions` table mirrors
  `log_access_streams`: an authed admin `POST` mints a 256-bit token
  with a ~60s connect deadline, single-use (consumed atomically in the
  WS-upgrade UPDATE), bound to the admin `user_id` + client IP.
- **Full byte recording.** The agent tees every byte (both directions)
  to `/var/log/jabali/terminal/<session-id>.cast` in asciinema v2
  format (replayable with `asciinema play`). Non-negotiable: if the
  host is later compromised, the root-shell history must be auditable.
- **Alert + audit.** M14 notification on session open; `terminal_sessions`
  rows record open (`started_at`) and close (`ended_at`).
- **Timeouts.** Agent kills the PTY process group on idle (no stdin for
  N minutes) or max session duration.

## Alternatives considered

### Allowlisted / sandboxed root shell (reuse M13 jabali-ssh-shell)
- **Pros**: smaller blast radius.
- **Cons**: defeats the purpose of a real ops terminal; M13 already
  serves per-user sandboxed SSH.
- **Why not**: user explicitly chose a full root shell.

### PTY in panel-api directly
- **Pros**: no second socket, no agent hop.
- **Cons**: panel-api would need root (setuid/sudo) — re-introduces
  root into the large web process. Rejected.

### Tunnel the PTY through agentwire JSON-RPC
- **Cons**: agentwire is req/resp; framing a bidirectional byte stream
  through it is a protocol abuse with backpressure/lifetime hazards.
- **Why not**: a purpose-built framed UDS is simpler and safer.

## Consequences

### Positive
- Root ops from the browser; panel-api stays unprivileged.
- Every root action is replayable for incident response.
- Disabled by default; blast radius is opt-in.

### Negative / Risks
- An authenticated-admin RCE exists when enabled — accepted, mitigated
  by gate + one-shot IP-bound token + recording + alert + timeouts.
- Recording at `/var/log/jabali/terminal/` can contain secrets typed by
  the admin. Mitigation: 0750 root:adm, logrotate, documented in the
  runbook; this is the intended forensic tradeoff for a root shell.
- A second agent socket widens the agent's surface — mitigated by the
  gate (broker refuses sessions when `root_terminal_enabled` is false)
  and the same group-reachability model as existing ADR-0050 sockets.
