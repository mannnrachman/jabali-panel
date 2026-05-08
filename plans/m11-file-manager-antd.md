# M11 File Manager — AntD-native, filebrowser replacement

**Status**: Proposed. Pending user confirmation.
**Supersedes**: `plans/m11-filebrowser-session-fix.md` (Option B design — abandoned).
**ADR**: `docs/adr/0030-antd-file-manager-replaces-filebrowser.md`.

---

## 1. Why

Filebrowser cost ~1 week of yak-shave: stateless proxy-auth, cookie glue, BoltDB per-process scope caching, CLI silently dropping `--scope`, Root+scope resolution opacity, POSIX-ACL choreography, SQLite-lock dances. The whole stack elsewhere (nginx, php-fpm, cron, WP, SFTP) is ours — file manager is the one place we imported a foreign opinion and it hurt.

We own auth (panel JWT + RBAC), scope (shared validator), UID execution (root agent + chown), UI (AntD). Filebrowser is removed, not fallbacked.

---

## 2. Scope (v1 MVP — LOCKED)

### 2.1 UI layout (per user spec)

```
+---------------------------------------------------------------+
|  Breadcrumbs: /domains/example.com/public_html     [Upload] [New Folder] [Refresh]  |
+------------------+--------------------------------------------+
|  Folder Tree     |  File Table                                |
|  (AntD Tree,     |  (AntD Table, columns: name, size, mtime, |
|   lazy-load)     |   action dropdown per row)                 |
|                  |                                            |
|                  |                                            |
+------------------+--------------------------------------------+
```

### 2.2 AntD components (per user spec, exact list)

`Layout` · `Tree` · `Table` · `Breadcrumb` · `Upload` (click-only, NOT Dragger) · `Dropdown` · `Modal` · `Spin`

### 2.3 Operations (v1 ONLY)

- **list** — load current folder into Table
- **tree-expand** — lazy-load subfolders on expand
- **upload** — click-to-upload single file, ≤100MB hard cap
- **download** — stream one file
- **mkdir** — new folder modal
- **rename** — rename modal (one file/folder)
- **delete** — delete one file/folder, with confirm modal
- **preview** — read-only text preview, ≤1MB (drawer or modal)

### 2.4 Explicitly OUT of v1 (Phase 2 — tracked, not blocked)

1. Right-click context menu (Dropdown overlay on row click actions in v1 is fine; right-click is Phase 2)
2. Drag-and-drop upload (`Upload.Dragger`)
3. Multi-select actions (`Table` rowSelection + bulk delete/move)
4. Permissions display + chmod edit (octal mode column + modal)
5. Monaco editor modal (for `.htaccess`, `.env`, config edits)
6. Image preview + lightbox
7. ZIP/unzip (agent `files.archive.{create,extract}`)
8. Move (cross-folder) — v1 supports rename only; Phase 2 adds a target-picker
9. Copy
10. Chunked/resumable upload

---

## 3. Architecture

### 3.1 Execution model

Agent runs as root. For every write op (upload, mkdir, rename), agent performs the op then `chown <user>:www-data`, `chmod 0640` (files) / `0750` (dirs). This matches the deployed per-user model verified on 192.168.100.150 (2026-04-19): PHP-FPM pool at `/etc/php/8.5/fpm/pool.d/jabali-<user>.conf` runs as `<user>:<user>` (full isolation — PHP reads user files via UID), while docroot + parents are `<user>:www-data` mode 0750 so nginx (worker as www-data) can read static assets via group-read, and OTHER users on the same box cannot read via shell (no "other" bits). Do NOT set files world-readable (0644) — that breaks cross-user isolation; user B on the box could shell-read user A's files. Do NOT set 02775 setgid — the deployed model uses plain 0750. Sensitive files like `wp-config.php` may use `<user>:<user>` 0600 (caller's decision, not filesafe's default). No `setresuid`, no per-user helper daemon. Scope-clamp is enforced in the shared validator, called in BOTH panel-api AND panel-agent (defense in depth — same pattern as M8 cron).

### 3.2 Scope

Default: `/home/<user>/domains/`. Admin impersonation: inherited from M5a (JWT carries `acting_as`).

### 3.3 Path safety

Shared package `/internal/filesafe/`:
- `Clean` the incoming path
- Reject `..`, null bytes, absolute paths not prefixed by user scope
- `filepath.EvalSymlinks` → re-check final path still inside scope
- Where practical, use `syscall.Open` with `O_NOFOLLOW` on the final `open(2)` to defeat TOCTOU
- (Out of v1: `openat2(2)` with `RESOLVE_BENEATH` via `golang.org/x/sys/unix` — Phase 2, `EvalSymlinks` + prefix recheck is adequate)

### 3.4 Wire contract

`/internal/filesafe/filesafe.go` is shared. Agent command shapes tested in `panel-agent/internal/commands/files_wire_test.go` (mirrors `cron_wire_test.go` from M8) — marshals each `files.*` param struct, asserts JSON keys. Cross-boundary drift is caught at build time.

---

## 4. File layout

### 4.1 Backend (new)

```
/internal/filesafe/
  filesafe.go                    — ValidatePath, EvalAndRecheck, IsWithinScope
  filesafe_test.go               — traversal, symlink, scope boundary, encoding

panel-agent/internal/commands/
  files_list.go                  — list directory (one level)
  files_read.go                  — stream read (download + preview)
  files_write.go                 — multipart recv, 100MB cap, chown post-op
  files_delete.go                — delete file or empty dir
  files_mkdir.go                 — mkdir + chown
  files_rename.go                — same-dir rename only (v1)
  files_stat.go                  — stat single path (for preview mime sniff)
  files_wire_test.go             — JSON-tag drift guard

panel-api/internal/api/
  files.go                       — HTTP handlers for /api/v1/files/*
  files_test.go                  — integration tests vs. agent
  files_middleware.go            — 100MB multipart cap
```

### 4.2 Frontend (new)

```
panel-ui/src/shells/user/files/
  UserFilesPage.tsx              — Layout container
  FolderTree.tsx                 — AntD Tree (lazy-load)
  FileTable.tsx                  — AntD Table (single-row actions)
  FilesBreadcrumb.tsx            — AntD Breadcrumb (path nav)
  ActionBar.tsx                  — [Upload] [New Folder] [Refresh] buttons
  UploadButton.tsx               — AntD Upload (click-only, customRequest)
  CreateFolderModal.tsx          — AntD Modal + Form
  RenameModal.tsx                — AntD Modal + Form
  DeleteConfirmModal.tsx         — AntD Modal.confirm
  PreviewDrawer.tsx              — AntD Drawer + <pre> text view
  hooks/useFilesList.ts
  hooks/useFolderTree.ts
  hooks/useFileUpload.ts
  hooks/useFilesMutations.ts     — mkdir, rename, delete
  hooks/useFilePreview.ts
  types.ts                       — FileEntry, FolderNode, etc.
```

---

## 5. Waves

Each wave self-contained. A cold agent can execute any wave given this plan.

### Wave A — Shared validator + agent stubs

**Goal**: `/internal/filesafe/` package + agent command stubs + wire-shape test.

**Files**: `filesafe.go`, `filesafe_test.go`, 7 `files_*.go` stubs, `files_wire_test.go`, registry wiring.

**Tests**:
- Unit: filesafe 80%+ coverage (paths: `..`, `../../etc`, `%2e%2e`, null bytes, symlink escape, relative, absolute)
- Wire-shape test defines expected JSON and FAILS until Wave B (stubs return "not implemented")

**Verify**:
```bash
go test ./internal/filesafe/... -v -cover
go build ./panel-agent/...
```

**Exit**: filesafe compiles with 80%+ coverage; stubs compile; wire-test documents shapes; registry includes `files.*`.

**Rollback**: delete `/internal/filesafe/` and `panel-agent/internal/commands/files_*.go`; revert registry.

---

### Wave B — Agent command implementation

**Goal**: 7 `files.*` commands working as root + chown post-op. Wire-test passes.

**Files**: implement stubs from Wave A.

**Tests**:
- Unit per command: happy path + scope-outside reject + symlink escape reject + ownership post-op (stat checks UID/GID/mode)
- 100MB cap enforced in `files.write` via `io.LimitReader`
- TOCTOU test: create symlink mid-op, verify `O_NOFOLLOW` blocks
- Wire-test passes

**Verify**:
```bash
go test ./panel-agent/internal/commands/ -run Files -v -cover
go test ./panel-agent/internal/commands/ -run WireShape -v   # PASS
```

**Exit**: 7 commands tested 80%+; ownership correct on every write; symlink safety tested; wire-test green.

**Rollback**: revert command files to stubs.

---

### Wave C — API handlers + middleware

**Goal**: REST endpoints exposing agent commands.

**Endpoints**:
```
GET    /api/v1/files?path=               list
GET    /api/v1/files/tree?path=           lazy tree expand
GET    /api/v1/files/download?path=       stream download
GET    /api/v1/files/preview?path=        text preview (≤1MB, nosniff)
POST   /api/v1/files/upload?path=         multipart, cap 100MB
POST   /api/v1/files/mkdir                JSON {path}
POST   /api/v1/files/rename               JSON {path, new_name}
DELETE /api/v1/files?path=                single path
```

**Files**: `files.go`, `files_test.go`, `files_middleware.go`, router wiring.

**Tests**: each endpoint — happy path, 403 out-of-scope, 404 missing, 413 too-large, consistent error envelope.

**Verify**:
```bash
go test ./panel-api/internal/api/ -run Files -v -cover
curl -H "Authorization: Bearer $JWT" "https://localhost:8443/api/v1/files?path=/"
```

**Exit**: all 8 endpoints tested; filesafe called before every agent dispatch; 100MB cap enforced at middleware; streaming download works; preview sets `X-Content-Type-Options: nosniff` + `Content-Disposition: attachment` for non-text.

**Rollback**: delete `files.go`, `files_middleware.go`; revert router.

---

### Wave D — Frontend (AntD)

**Goal**: UI at `/jabali-panel/files`.

**Components** (AntD-only, per user spec):
- `Layout` shell with Sider (Tree) + Content (Breadcrumb + ActionBar + Table)
- `Tree`: lazy-load on expand (`loadData` prop), icons for folders only
- `Table`: columns `name` (icon + text), `size`, `mtime` (relative), `actions` (Dropdown: Download, Preview, Rename, Delete)
- `Breadcrumb`: click any segment to navigate
- `Upload` (click-only, `showUploadList={false}`, `customRequest` → `useFileUpload`)
- `Modal` × 3: mkdir, rename, delete-confirm
- `Spin`: table loading, tree loading
- Dark-mode parity: inherits panel theme tokens

**Tests**:
- Component unit: Table columns render, row actions fire correct handler
- Hook unit: mock `/api/v1/files` responses, assert query keys + invalidation
- Manual E2E walk-through before Wave E

**Verify**:
```bash
cd panel-ui && npm run build && npm test -- files
```

**Exit**: page renders; tree lazy-loads; upload enforces 100MB client-side (before sending); modals submit + refresh list via TanStack invalidation; dark-mode parity.

**Rollback**: delete `panel-ui/src/shells/user/files/`; revert route registration and sidebar link.

---

### Wave E — Filebrowser decommission

**Goal**: Remove filebrowser from every code path. See `plans/m11-filebrowser-decommission-checklist.md` for the exact file list.

**Order (idempotent, for `update.go`)**:
1. Stop + disable `jabali-filebrowser.service`
2. Reload nginx with new location block redirecting `/files/*` → `/jabali-panel/files` (301)
3. Down-migrate `filebrowser_sso_tokens` table
4. Remove agent commands (`filebrowser_*.go`) and registrations
5. Remove API handlers + SSO routes (`sso_filebrowser_validate.go`, related sso.go entries)
6. Remove FE references (any `/files/` links in user shell)
7. Remove config dir (`/etc/jabali-panel/filebrowser/`) + BoltDB (`/var/lib/jabali-filebrowser/`)
8. Purge `/usr/local/bin/filebrowser`
9. Remove POSIX-ACL setfacl calls **only if no other feature consumes them** (verify via grep)
10. Remove `filebrowser` OS user/group if created by installer

**Files modified**: `install.sh`, `panel-api/cmd/server/update.go`, `install/nginx/*filebrowser*`, `install/filebrowser/` (delete), `panel-agent/internal/commands/filebrowser_*.go` (delete), migration `down`.

**Tests**:
- `install.sh` idempotent: fresh VM + existing filebrowser VM both succeed
- curl `/files/ping` → 301 to `/jabali-panel/files`
- E2E smoke: old bookmark redirects; new file manager works; no residual systemd unit

**Exit**: grep `-r filebrowser .` returns only history/docs references; systemctl `jabali-filebrowser` not-found; `/files/*` redirects 301.

**Rollback**: git revert; `migrate down` on the drop migration; reinstall filebrowser manually. **This wave is the point of no return — do not merge until Waves A-D are in staging.**

---

### Wave F — ADR, runbook, BLUEPRINT flip, Phase 2 backlog

**Goal**: Documentation + housekeeping.

**Files**:
- `docs/adr/0030-antd-file-manager-replaces-filebrowser.md` — full ADR (stub exists, flesh out)
- `plans/m11-file-manager-runbook.md` — ops: how to verify upload works, how to check ownership, common 403 causes
- `docs/BLUEPRINT.md` — M11 section: update title to "File manager (AntD-native)", supersede ADR-0026 if any, link ADR-0030
- `plans/m11-file-manager-phase2.md` — list the 10 Phase 2 items with effort estimates
- `CHANGELOG.md` — entry
- `plans/m11-filebrowser-session-fix.md` — mark SUPERSEDED at top, link ADR-0030

**Tests**: docs-only; run `/update-docs` skill.

**Exit**: all docs linked; BLUEPRINT reflects new M11 shape; Phase 2 backlog written; old session-fix plan marked superseded.

**Rollback**: revert doc edits.

---

## 6. Testing strategy

| Level | Where | What |
|---|---|---|
| Unit | `/internal/filesafe/` | 80%+ coverage; path traversal, symlink, encoding |
| Unit | `panel-agent/internal/commands/files_*.go` | 80%+ per command; scope reject, ownership, TOCTOU |
| Wire-shape | `files_wire_test.go` | JSON tag parity panel-api ↔ agent |
| Integration | `panel-api/internal/api/files_test.go` | real agent over UDS in test harness |
| Component | `panel-ui/**/files/` | Table + hooks (mocked API) |
| E2E | Playwright (Wave E) | upload → list → preview → rename → delete; scope-escape returns 403 |
| Adversarial | Wave E | `curl` with `../etc/passwd`, symlink escape via SFTP-created link, 100MB + 1 byte upload |

---

## 7. Honest assessment

### 7.1 Time estimate

- **Optimistic**: 6 days (6 waves × 1 day).
- **Realistic**: 8–10 days.
- **Breakdown**: A=1, B=2.5, C=1.5, D=2, E=1, F=0.5. Plus slippage.

**Gotchas that eat time**:
- Symlink + TOCTOU safety tests. Writing them right takes a half-day.
- Streaming download headers (`Content-Disposition`, range requests if we want resume — we don't in v1).
- `Upload` customRequest + progress UX is fiddly.
- `update.go` decommission must be idempotent across prod states we've already shipped.

### 7.2 Top 5 security risks

| Risk | Mitigation in v1 | Residual |
|---|---|---|
| **Path traversal** (encoded `..`, null, Unicode normalization) | filesafe called in API + agent; `filepath.Clean` + prefix check AFTER URL decode | low — broad test coverage |
| **Symlink escape** (user creates symlink via SFTP, reads via panel) | `EvalSymlinks` + prefix recheck; `O_NOFOLLOW` on open | low — tested |
| **TOCTOU** between validator and syscall | operate on FD after `O_NOFOLLOW`, chown by FD not path | low |
| **chown race** (window where file is root-owned) | chown BEFORE first `Close` response to client; errors abort + unlink | medium — document as "if chown fails, file is quarantined as root" |
| **Disk exhaustion** (no per-user quota) | 100MB per-request cap; accept DoS risk in v1 | **ACCEPTED** — monitor `df` |

### 7.3 Top 3 UX regrets (likely within 2 weeks of ship)

1. **No text editor** → users hit `.htaccess` edits, fall back to SFTP. Mitigation: ship Phase 2 Monaco in ~1 day when second support ticket arrives.
2. **100MB upload cap** → users try to upload DB dumps or media libraries, get 413. Mitigation: clear error message directing to SFTP; Phase 2 chunked upload.
3. **No ZIP download / no multi-select** → users want "download my whole docroot" or "delete all .log files". Mitigation: communicate in runbook; Phase 2 zip + multi-select.

### 7.4 When to revisit "text editor in v1"

**Breakeven**: ~1 extra day in v1 (Monaco modal, save via files.write, read via files.read). If user tickets hit 2+ in week 1, pull it into the very next milestone. Don't spend the day now; v1 ships faster and we see real demand.

### 7.5 Invariants reviewers MUST verify

1. Every `files.*` agent command calls `filesafe.ValidatePath` before any `os.*` syscall (grep).
2. Every write op (`files.write`, `files.mkdir`, `files.rename`) calls chown before returning success.
3. Ownership is `<user>:www-data`, mode `0640` files / `0750` dirs (never root:root, never world-readable). Matches deployed per-user model verified on 192.168.100.150: nginx (www-data group) reads static via group-read; cross-user shell reads blocked by no-other-bits.
4. `files.list` responses contain no paths outside user scope (unit test).
5. 100MB cap enforced at middleware AND `io.LimitReader` in `files.write` (two layers).
6. Preview endpoint sets `X-Content-Type-Options: nosniff` + `Content-Disposition: attachment` for non-text MIME.
7. Symlink at any point in the path triggers `EvalSymlinks` + prefix recheck; `O_NOFOLLOW` on final open.
8. Wire-shape test passes (JSON tag parity).
9. Every API endpoint has JWT middleware + scope extraction from JWT (not from query/body).
10. `grep -r "filebrowser" --exclude-dir=.git --exclude-dir=plans --exclude-dir=docs` is empty after Wave E.

---

## 8. Dependencies + non-dependencies

**Depends on** (all shipped): M1 users, M5a admin impersonation, M9 per-user slices (for agent pattern).

**Does NOT depend on**: reconciler (file manager is request/response only, no DB state to converge), systemd (no per-user daemon), POSIX ACLs (we chown instead).

---

## 9. References

- `plans/m10-wordpress.md` — wave format reference
- `plans/m8-cron.md` — shared-validator pattern reference
- `plans/m11-filebrowser-decommission-checklist.md` — produced in parallel (Wave E source of truth)
- `plans/m11-file-manager-threat-model.md` — produced in parallel (Wave A/B invariant source)
- `plans/m11-file-manager-fe-patterns.md` — produced in parallel (Wave D scaffold source)
- `docs/adr/0030-antd-file-manager-replaces-filebrowser.md` — ADR
