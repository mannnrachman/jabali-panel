# M11 File Manager — Runbook

Operational reference for the AntD-native file manager shipped in Waves A–E
(2026-04-18 to 2026-04-19). See ADR-0030 for the design decision,
`plans/m11-file-manager-antd.md` for the full plan.

## Architecture at a glance

```
User SPA  ──────► panel-api /api/v1/files/*  ──UDS──►  panel-agent
                  (auth + scope lookup)              (root; filesafe-gated FS ops)
                         │                                    │
                         └── writes audited via panel logs ───┘
```

- All FS operations run as root in `panel-agent`, then `chown`ed to
  `<user>:www-data` (0640/0750) to match the per-user FPM isolation model.
- Path safety is enforced by `internal/filesafe/` in BOTH panel-api and
  panel-agent — same cross-boundary guard as M8 cron's `/internal/cronvalidate/`.
- No long-lived filesystem daemon: every request is a short-lived agent call.

## Endpoints

| Method | Path | Purpose |
|--------|------|---------|
| GET    | `/api/v1/files?path=…`          | list directory |
| GET    | `/api/v1/files/tree?path=…`     | dirs-only listing for lazy tree expand |
| GET    | `/api/v1/files/home`            | the caller's starting directory |
| GET    | `/api/v1/files/download?path=…` | stream content as attachment (nosniff) |
| GET    | `/api/v1/files/preview?path=…`  | text preview, 1 MiB cap |
| POST   | `/api/v1/files/upload?path=…`   | multipart upload, 100 MB cap |
| POST   | `/api/v1/files/mkdir`           | JSON `{path}` |
| POST   | `/api/v1/files/rename`          | JSON `{path, new_name}` |
| DELETE | `/api/v1/files?path=…&recursive=true` | single-path removal |

## Common issues

### "forbidden" / HTTP 403 on every call
- Caller's JWT user has no `username` → `/api/v1/files/home` returns 403.
  Fix: admin assigns a unix username on the user record (migration 0003 or
  admin UI → Users → Edit → set username).
- `not_in_scope`: path doesn't start with `/home/<username>`. Expected; UI
  only ever sends paths returned from `/files/home` or its descendants.

### Uploads fail with "request body too large"
- Multipart body > 100 MB. Cap is set at the `filesUploadSizeLimit`
  middleware (`panel-api/internal/api/files_middleware.go`). Raising it
  means also raising the agent-side cap in `files_write.go`.

### Download returns mangled bytes for binary files
- Known MVP limitation. `files.read` / `files.write` carry `content` as a
  JSON string; Go's `encoding/json` replaces invalid UTF-8 with U+FFFD.
  Text files work, binary is Phase-2 (see BLUEPRINT M11 backlog).

### "user has no linux account" on `/files/home`
- Admin-only accounts (no `username`) cannot use the file manager. This is
  expected — admins don't have a homedir; impersonate a target user to
  access their files.

### Preview truncated silently
- Preview is capped at 1 MiB via `filesReadAgentParams.Limit = maxPreviewBytes`.
  The agent returns whatever fits in that cap; the `size` field in the response
  is the cap value, not the file size. Use download for the full file.

## Verifying health

```bash
# Agent reachable from panel-api
curl -sH "Authorization: Bearer $JWT" https://localhost:8443/api/v1/files/home
# → {"path":"/home/<username>"}

# Path safety
curl -sH "Authorization: Bearer $JWT" "https://localhost:8443/api/v1/files?path=/etc"
# → 403 not_in_scope

# Upload smoke
echo hello > /tmp/smoke.txt
curl -sH "Authorization: Bearer $JWT" -F file=@/tmp/smoke.txt \
  "https://localhost:8443/api/v1/files/upload?path=/home/<username>"
# → {"path":"/home/<username>/smoke.txt","bytes_written":6}
```

## Decommissioning leftover filebrowser bits on upgraded hosts

After running `update.go` on an existing host, these should be absent:

```bash
systemctl status jabali-filebrowser.service    # should: "not-found" or "masked"
test ! -f /etc/nginx/conf.d/jabali-files.conf  # must return 0 (file gone)
getent group filebrowser                        # optional: group may linger; harmless
test ! -d /var/lib/jabali-filebrowser           # must return 0 — else `rm -rf` manually
```

If any of those fail, re-run `panel-api/cmd/server update` — the decommission
block in `update.go` is idempotent and will re-apply.
