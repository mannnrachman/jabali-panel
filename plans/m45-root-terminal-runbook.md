# M45 — Root web terminal runbook

ADR: [0096-root-web-terminal.md](../docs/adr/0096-root-web-terminal.md)
Plan: [proud-whistling-sloth] (`~/.claude/plans/proud-whistling-sloth.md`)

## What it is

A true unrestricted root shell (`bash -l`, uid 0) in the admin panel,
**off by default**. Browser xterm.js ↔ WSS ↔ nginx ↔ panel-api (jabali
user, byte-pump only) ↔ `/run/jabali/agent-pty.sock` ↔ panel-agent
(root) PTY. Every byte both directions is recorded to
`/var/log/jabali/terminal/<session-id>.cast` (asciinema v2).

## Layers

| Layer | Path | Owner |
|---|---|---|
| Gate | `server_settings.root_terminal_enabled` (mig 000134, default 0) | panel-api |
| Token | `terminal_sessions` (one-shot, IP+admin bound, 60s TTL) | panel-api |
| Mint | `POST /api/v1/admin/terminal/session` (RequireAdmin) | `api/terminal.go` |
| WS bridge | `GET /api/v1/admin/terminal/ws/:token` | `api/terminal.go` |
| PTY broker | UDS `/run/jabali/agent-pty.sock` root:`jabali` 0660 | `commands/terminal_pty.go` |
| Recording | `/var/log/jabali/terminal/<id>.cast` 0640 root:root | agent |
| Alert | M14 `security.root_terminal.opened` (critical) on open | dispatcher |
| UI | `/jabali-admin/terminal` | `shells/admin/terminal/AdminTerminal.tsx` |

Wire opcodes (browser-WS binary `[op][payload]` ↔ agent-UDS
`[op][4B len][payload]`): `0x00` stdout, `0x01` stdin, `0x02` resize
JSON, `0x03` exit, `0x10` init (panel-api→agent only).

## Enable

```sql
-- or: Server Settings → Storage → "Root Terminal (M45)" toggle
UPDATE server_settings SET root_terminal_enabled = 1;
```
No restart needed — the handler re-reads the gate on every mint + WS
upgrade.

## Operate

1. Admin UI → **Terminal** (left nav) → root prompt.
2. Resize browser → `tput cols` tracks (resize frame).
3. `exit` or close tab → PTY process-group killed, `ended_at` stamped.

Timeouts (agent-side, `commands/terminal_pty.go`): idle 15 min,
max 4 h → SIGKILL the process group.

## Audit / forensics

```sh
# replay a session exactly as it happened
asciinema play /var/log/jabali/terminal/<session-id>.cast

# who opened what, when
SELECT id,user_id,client_ip,started_at,ended_at,cast_path
  FROM terminal_sessions WHERE started_at IS NOT NULL ORDER BY started_at DESC;
```
`.cast` records BOTH output (`"o"`) and input (`"i"`) events — it can
contain secrets the admin typed. Files are root:root 0640, weekly
logrotate, kept 26 weeks (`install/logrotate/jabali`).

## Disable

```sql
UPDATE server_settings SET root_terminal_enabled = 0;
```
UI tab shows an enable-notice; `POST /admin/terminal/session` → 403;
existing live session is unaffected until it ends (gate is checked at
mint/upgrade, not mid-session — that's intentional: don't yank an
operator mid-command).

## Failure modes

| Symptom | Cause | Fix |
|---|---|---|
| Mint 403 `root_terminal_disabled` | gate off | enable toggle |
| WS 502 `pty_broker_unreachable` | agent down / socket missing | `systemctl status jabali-agent`; socket is `/run/jabali/agent-pty.sock` |
| WS 401 `token_invalid_or_used` | token reused/expired (>60s) | re-open the page (mint is automatic) |
| WS 403 `token_rebind_denied` | client IP or admin differs from mint | don't proxy-hop between mint and connect |
| no `.cast` file | recordDir unwritable | agent runs as root; check `/var/log/jabali/terminal` perms |

## Verification (VM 192.168.100.150)

1. `jabali update`; toggle on via Server Settings.
2. Terminal tab → `id` → `uid=0(root)`.
3. Resize window → `tput cols` reflects it.
4. `asciinema play /var/log/jabali/terminal/<id>.cast` replays.
5. M14 critical notification on open; `terminal_sessions` open→close rows.
6. Idle 15 min → auto-killed, `ended_at` set.
7. Toggle off → tab notice + mint 403; reused token → WS 401.
8. Non-admin Kratos session → mint 403.

Unit guards: `panel-agent/internal/commands/terminal_pty_test.go`
(frame codec, oversize reject, id sanitisation),
`panel-api/internal/repository/terminal_session_repository_test.go`
(single-use + expiry predicate lock).
