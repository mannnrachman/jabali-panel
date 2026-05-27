# File Manager

`/jabali-panel/files`. AntD-native (M11, ADR-0030). The earlier `filebrowser` daemon was decommissioned 2026-04-19.

## What you can do

- Browse: navigate inside your own home directory (`/home/<you>`). Symlinks resolved, no escaping above your root.
- Upload: drag-and-drop multiple files; chunked uploads for large files.
- Download: single file or selected files (zipped server-side).
- Create / rename / delete: files and directories.
- Edit text files in-browser (Monaco editor; syntax highlighting for common languages).
- View images, archives (list contents), PDFs.
- Set permissions: octal (`755`, `644`, etc.) or per-bit checkboxes.
- Compress / extract: tar.gz, zip, 7z.

## What you can't do

- Browse outside your home directory.
- Set ownership (`chown` — handled by the agent for app installs; you can't change UIDs).
- Run arbitrary shell commands from the file manager (use SSH / SFTP for that).
- Execute uploaded scripts directly (they execute only when served by nginx → PHP-FPM; the file manager doesn't shell out).

## Implementation note

Filesystem ops run through the agent over Unix socket. The panel-api translates UI requests into agent calls; the agent enforces "this UID can only touch its own home". No bind-mounts, no chroot helpers, no separate file-browser process.

## Why the rewrite

The original `filebrowser` was a separate Go daemon with its own auth, session, and storage assumptions. Inside Jabali this meant:

- Two auth stacks to keep in sync.
- A second listening port (or socket) to lock down.
- Its own JSON config to diff vs. panel state.
- UI mismatch (its UI is Vue, Jabali is AntD).

The in-panel file manager eliminates all four; everything is one panel-api call away.
