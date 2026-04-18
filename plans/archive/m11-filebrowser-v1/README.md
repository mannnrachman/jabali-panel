# Archived: M11 FileBrowser v1 plans

**Archived**: 2026-04-19
**Reason**: Superseded by the AntD-native file manager (ADR-0030)
**Successor**: `plans/m11-file-manager-antd.md`

These plans describe the first attempt at M11 using filebrowser v2.38 as the file-management layer. After ~1 week of debugging stateless proxy-auth, cookie glue, BoltDB per-process scope caching, CLI silently dropping scopes, Root+scope confusion, and POSIX-ACL choreography, the decision was made to remove filebrowser entirely and build an AntD-native file manager owned end-to-end.

See `docs/adr/0030-antd-file-manager-replaces-filebrowser.md` for the full rationale.

## Files

- `m11-filebrowser.md` — original M11 plan
- `m11-filebrowser-runbook.md` — ops runbook for the filebrowser integration
- `m11-filebrowser-session-fix.md` — Option B session-fix design (abandoned)

Kept for historical reference only. Do not implement.
