# ADR-0030: AntD-native file manager replaces filebrowser

**Date**: 2026-04-18
**Status**: proposed
**Deciders**: shuki + Claude
**Supersedes**: the implicit M11 decision to adopt filebrowser (documented nowhere as an ADR — this ADR records that decision retroactively and supersedes it in one stroke)

## Context

M11 shipped filebrowser as the file-management feature in April 2026. In practice the integration has cost roughly one week of debugging:

- `auth.method=proxy` in filebrowser is **stateless** — no cookie or JWT fallback inside filebrowser when the `X-Forwarded-User` header is absent. Every SPA sub-request after bootstrap needed the header re-injected, which our nginx catch-all wasn't doing.
- Filebrowser's CLI `filebrowser users add --scope=<path>` silently stores an empty string in its BoltDB; the scope only works when set via the HTTP admin API.
- Filebrowser caches user records per-process after first login; admin PUTs to fix scope are not visible until restart.
- Filebrowser's `-b /files` baseurl is honored on CLI but overridden by values persisted into BoltDB — config drift is invisible.
- `filebrowser_group_add` + POSIX ACLs (`setfacl -R -m g:filebrowser:rX`, default ACL with `-d`) added a per-user-home ACL surface that had to be reconciled on every user create.
- The overall architecture (proxy-auth + HMAC cookie via nginx njs OR session tokens + DB lookup per request — `plans/m11-filebrowser-session-fix.md`) was a growing patch on a fundamental impedance mismatch.

Every other system on the panel (nginx, php-fpm pools, cron, WordPress, SFTP, databases) is owned end-to-end by us: panel-api holds auth + RBAC + scope; panel-agent is the root executor; the UI is AntD throughout. The file manager was the one place we imported a foreign opinion, and paid for it.

We evaluated **@cubone/react-file-manager** (MIT, 144★, small maintenance, no search, no multi-select, no editor) and **SVAR React File Manager** (MIT, no pricing published = probable commercial freemium, TypeScript, has search). Both are UI-only — the hard parts (auth model, scope enforcement, per-user UID execution, path-traversal safety, chunked upload) remain ours to build. The UIs save ~1-2 dev-days but introduce theme/icon-system mismatch against our AntD-everywhere-else stack.

## Decision

Remove filebrowser entirely and replace it with an AntD-native file manager built in `panel-ui/src/shells/user/files/`, backed by new `files.*` commands in the root panel-agent and new `/api/v1/files/*` endpoints in panel-api. Privileged filesystem operations are executed by the agent as root and then `chown`ed to `<user>:<user>` (matches M9 per-user FPM isolation and M10 WordPress install pattern — `www-data` group membership would leak read access to nginx and weaken isolation). Path safety is enforced by a shared `/internal/filesafe/` package called in both panel-api and panel-agent (same cross-boundary pattern as M8 cron's `/internal/cronvalidate/`).

The v1 MVP ships exactly: list, tree-expand, upload (≤100MB, click-only), download, mkdir, rename (same-dir), delete (single), text preview (≤1MB). Layout is Breadcrumb + ActionBar on top, Tree on left, Table on right. Components limited to `Layout`, `Tree`, `Table`, `Breadcrumb`, `Upload`, `Dropdown`, `Modal`, `Spin`.

Full plan: `plans/m11-file-manager-antd.md`.

## Alternatives Considered

### Alternative 1: Keep filebrowser, continue bolting on fixes
- **Pros**: Tool exists; users know it; partial integration already shipped
- **Cons**: Each fix uncovers another — scope CLI bug → per-process cache → baseurl drift → ACL choreography. Architectural mismatch (stateless proxy-auth + in-process session cache + no admin API for provisioning) will keep leaking bugs. Not solvable without forking filebrowser.
- **Why not**: Cost curve is rising, not falling. Sunk-cost thinking.

### Alternative 2: @cubone/react-file-manager
- **Pros**: MIT; drop-in React component; drag-drop included
- **Cons**: 144 stars + 16 open issues = single-maintainer risk; no search, no multi-select, no editor, no chunked upload; uses react-icons + SCSS (theme mismatch vs. AntD); backend is still 100% ours to build; solves only ~30% of the problem.
- **Why not**: Saves ~1 dev-day on UI, adds theme-system divergence for years. Net negative.

### Alternative 3: SVAR React File Manager
- **Pros**: MIT, TypeScript, has search + list/tile/split views; CSS-variable theming
- **Cons**: No pricing disclosure = smells like freemium-bait; CSS-variables don't mesh cleanly with our AntD tokens; backend still entirely ours; no chunked upload, no editor.
- **Why not**: Same theme-mismatch cost as cubone, plus unclear licensing/roadmap trajectory.

### Alternative 4: AntD-native (CHOSEN)
- **Pros**: Zero theme delta; reuses AntD components already in our bundle (~30KB new code); owns the whole stack so there's no foreign auth/scope model to reconcile; future features (editor, zip, chmod, image preview) are natural extensions; matches M8/M9/M10/M12 architecture; Phase 2 roadmap is obvious
- **Cons**: ~1-2 more UI dev-days than a library would cost; we own path-safety, chunked upload, preview rendering (but we own those in any library alternative anyway)
- **Why chosen**: Lowest long-term cost, aligns with every other milestone's architecture, removes filebrowser's recurring bug tax.

## Consequences

### Positive

- One auth model across the entire panel (JWT + RBAC); filebrowser's proxy-auth + cookie duct tape is gone.
- Scope enforcement shares code with any future file-touching feature (backups, WP install/extract, log viewers) via `/internal/filesafe/`.
- UI matches the rest of the panel (theme tokens, icons, dark mode, impersonation banner).
- Phase 2 features (editor, zip, chmod, image preview, drag-drop, multi-select, right-click menu) are each 0.5-2 day deltas on the same architecture — no more filebrowser-shape fighting.
- Removes one systemd unit, one OS user/group, one BoltDB, one nginx auth_request chain, one ACL-on-user-home requirement, one SSO-token table.

### Negative

- Net 8-10 dev-days to build v1, vs. "just fix the scope bug" (~1 day).
- We now own path-traversal + symlink-escape safety end-to-end. Any CVE-class bug is ours.
- No text editor in v1 → users editing `.htaccess` fall back to SFTP for 1-2 weeks until Phase 2 ships the Monaco modal.
- 100MB upload cap in v1 is a hard ceiling; users uploading DB dumps hit it.

### Risks

| Risk | Mitigation |
|---|---|
| Path traversal via encoded or Unicode-normalized `..` | `filesafe.ValidatePath` called in both API and agent; `filepath.Clean` after URL decode; prefix recheck after `EvalSymlinks` |
| Symlink escape | `EvalSymlinks` + prefix recheck + `O_NOFOLLOW` on final `open(2)` |
| Disk exhaustion DoS (no per-user quota in v1) | Accepted for v1; monitor `df`; Phase 2 adds quota |
| Chunked upload absence drives SFTP growth | Accept — SFTP already shipped in M12; document 100MB cap in UI error |
| Filebrowser removal breaks bookmarked `/files/` URLs | nginx 301 redirect `/files/*` → `/jabali-panel/files` retained permanently |

### Phase 2 roadmap (documented in `plans/m11-file-manager-phase2.md`)

1. Right-click context menu (Dropdown overlay) — 0.5d
2. Drag-and-drop upload (`Upload.Dragger`) — 0.5d
3. Multi-select actions (Table rowSelection + bulk delete/move) — 1d
4. Permissions display + chmod edit — 1d
5. Monaco editor modal — 1-2d (includes save + concurrency-loss warning)
6. Image preview + `Image.PreviewGroup` lightbox — 0.5d
7. ZIP/tar archive create + extract — 2d (async agent command + progress channel)
8. Cross-folder move (target picker) — 0.5d
9. Copy — 0.5d
10. Chunked/resumable upload (tus protocol or multipart chunks) — 2-3d
