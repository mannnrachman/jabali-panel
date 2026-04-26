# M13 — SSH shell sandbox (bubblewrap default; nspawn opt-in)

**Status:** Plan drafted, not dispatched.
**Goal:** Every hosting user gets `/usr/local/bin/jabali-ssh-shell` as
login shell. SFTP users still hit `ForceCommand internal-sftp` (chroot
path from M12 + 7480fff covers them). SSH-shell users land in one of two
sandboxes: `bubblewrap` (default, lightweight) or `nspawn` (ephemeral
container off a versioned, immutable base image). No plain-bash mode —
admin who wants no isolation must make the user SFTP-only.

---

## 0. Key design decisions

1. **Two sandbox modes only: `bubblewrap` and `nspawn`. No "none".**
   "Plain bash with no sandbox" is intentionally not an option.
   Sandboxing is mandatory for shell users; admin who doesn't want
   sandboxing makes the user SFTP-only (existing M12 path with new M12
   chroot).

2. **Default mode is `bubblewrap`.**
   Fresh installs and new users land on `bubblewrap`. Lightweight, no
   image build, no rootfs cost. Admin can flip to `nspawn` for tighter
   isolation at the cost of ~250 MB rootfs + 1-3s connect latency.

3. **Wrapper-as-login-shell for ALL hosting users (SFTP and SSH).**
   Defense-in-depth: even SFTP users get the wrapper as their `/etc/passwd`
   shell. The Match block's `ForceCommand internal-sftp` overrides shell
   for SFTP — wrapper never runs in that path. But if Match block ever
   misconfigures, the SFTP user lands in a sandboxed shell, not a bare
   one.

4. **Fallback on bad config = `/usr/sbin/nologin`. Never bash.**
   Wrapper exits to nologin when:
     - Sandbox mode file missing or unrecognized value
     - `nspawn` mode but pinned image directory missing
     - bwrap binary missing or non-setuid (bubblewrap mode)
   No path through the wrapper produces an unsandboxed bash. Failure mode
   is "user can't shell in" — they SFTP instead.

5. **Versioned, immutable nspawn base images.**
   Images live at `/var/lib/jabali-nspawn/images/<version>/` with stable
   names like `debian-12-v1`, `debian-12-v2`. Each version is built once
   and never modified. Upgrades are new versions, not in-place edits.
   `jabali nspawn-build` CLI produces new versions; install.sh seeds
   `debian-12-v1` on first install.

6. **Per-user pinned image version.**
   New columns: `users.nspawn_image_version TEXT NULL`. New SSH-enabled
   user is stamped with `default_nspawn_image_version` (a server setting)
   at user-create. Existing users keep their pin even after the default
   bumps — upgrades are an explicit admin action per-user.
   Wrapper reads the pin via `/etc/jabali/users/<username>/nspawn-image`
   (reconciler writes that file in lockstep with the DB column).

7. **nspawn privilege bridge: sudo NOPASSWD on a single absolute path.**
   `/etc/sudoers.d/jabali-nspawn` lets group `jabali-ssh-sandbox` run
   `/usr/local/bin/jabali-nspawn-enter <image-name>` with NOPASSWD. The
   helper validates the image name against an allowlist scan of
   `/var/lib/jabali-nspawn/images/`, validates `$SUDO_USER` is a real
   hosting user, then exec's `systemd-nspawn --ephemeral
   --image=<image-path> --bind=/home/<user> ...`. No caller-controlled
   flags.

8. **bwrap relies on its setuid bit; no sudo needed.**
   `apt install bubblewrap` ships `/usr/bin/bwrap` setuid root on
   Debian/Ubuntu. Wrapper exec's it directly post-privilege-drop.

9. **Mode toggle is a single-file write.**
   `/etc/jabali/ssh-sandbox-mode` (root:root 0644). Wrapper reads on
   every connect. No sshd reload, no chsh, no agent fan-out beyond the
   one file write.

10. **Reconciler manages shell + pin file.**
    On every sweep: every hosting user gets `/usr/local/bin/jabali-ssh-shell`
    as their shell (chsh idempotent). SSH-enabled users get
    `/etc/jabali/users/<username>/nspawn-image` materialized from their
    DB pin. SFTP-only users have the file removed (defensive).

11. **Per-user image upgrade is an explicit admin action.**
    Admin bulk-upgrades or per-user. No auto-bump even if default ships
    a new version. Existing users on `debian-12-v1` keep that image
    until admin promotes them to `debian-12-v2`.

12. **Versions are append-only. Old images stay on disk.**
    Removing an image version requires confirming no user is pinned to
    it. `jabali nspawn-prune` CLI surfaces orphaned versions safely.

---

## 1. Steps / waves

| Step | Wave | Parallel? | Summary | Outputs |
|------|------|-----------|---------|---------|
| 1 — ADR | A | w/ 2 | Record "wrapper-as-shell + bubblewrap default + versioned nspawn images + sudo bridge" decisions; surface tradeoffs vs ForceCommand and shared-rootfs. | `docs/adr/0067-ssh-shell-sandbox.md` |
| 2 — install foundations | A | w/ 1 | apt install `bubblewrap` `debootstrap` `systemd-container`. Create `/etc/jabali/`, `/etc/jabali/users/`, `/var/lib/jabali-nspawn/images/`. Ship `install/ssh/jabali-ssh-shell` wrapper. Write default `/etc/jabali/ssh-sandbox-mode = bubblewrap`. Create groups `jabali-ssh-sandbox`. | `install.sh`, `install/ssh/jabali-ssh-shell`, `update.go` sync |
| 3 — bubblewrap profile + smoke | B | w/ 4,5 | Wrapper's bwrap branch with v1 mount profile (§3). Smoke: bwrap → bash → `ls /home/<other-user>` returns ENOENT, `cat /etc/shadow` denied. | `install/ssh/jabali-ssh-shell` (bwrap branch), `tests/smoke/bwrap-isolation.sh` |
| 4 — nspawn image build CLI + seed v1 | B | w/ 3,5 | `jabali nspawn-build [--codename debian-12 --version v1]` runs debootstrap + minimal post-process at `/var/lib/jabali-nspawn/images/<codename>-<version>/`. install.sh invokes once on first install (idempotent: skip if v1 exists). Image is sealed read-only after build. | `panel-cli/cmd/nspawn_build.go`, `install/nspawn/postinstall.sh`, install.sh integration |
| 5 — nspawn enter helper + sudoers | B | w/ 3,4 | Ship `install/ssh/jabali-nspawn-enter` (validates image name from allowlist, validates `$SUDO_USER`, exec's `systemd-nspawn --ephemeral --bind=/home/<u>`). Ship `install/ssh/jabali-nspawn-sudoers` locked to absolute path + group `jabali-ssh-sandbox`. install.sh `visudo -c` checks. | `install/ssh/jabali-nspawn-enter`, `install/ssh/jabali-nspawn-sudoers`, install.sh |
| 6 — migration + agent commands | C | — | Migration: `users.nspawn_image_version TEXT NULL`. Agent commands: `system.set_ssh_sandbox_mode` (writes mode file atomically); `ssh.user.set_shell` (chsh helper, idempotent); `ssh.user.write_nspawn_pin` (writes/removes `/etc/jabali/users/<u>/nspawn-image`); `system.list_nspawn_images` (returns directory listing for UI dropdowns). | new migration, `panel-agent/internal/commands/system_set_ssh_sandbox_mode.go`, `ssh_user_set_shell.go`, `ssh_user_write_nspawn_pin.go`, `system_list_nspawn_images.go` |
| 7 — server_settings + user model + API | C | — | server_settings keys: `ssh_sandbox_mode` (default `bubblewrap`), `default_nspawn_image_version` (default `debian-12-v1`). User model + repo gain `NspawnImageVersion *string`. API: extend system settings GET/PATCH; expose per-user pin in user PATCH; `GET /api/v1/system/nspawn-images` for the dropdown. | `panel-api/internal/repository/server_settings.go`, `internal/models/user.go`, `internal/api/server_settings.go`, `internal/api/users.go`, `internal/api/nspawn_images.go` |
| 8 — reconciler shell + pin sync | D | w/ 9,10 | Every sweep: `ssh.user.set_shell` for all hosting users → wrapper. For SSH-enabled users: stamp DB pin to `default_nspawn_image_version` if NULL; call `ssh.user.write_nspawn_pin` to materialize the file. For SFTP-only users: remove pin file (best-effort). | `panel-api/internal/reconciler/ssh_keys_reconcile.go`, fan-out hook on settings change |
| 9 — UI: Server Settings → SSH Access | D | w/ 8,10 | AntD select for `ssh_sandbox_mode` (Bubblewrap default / nspawn). Inline help describing tradeoffs. Second select for `default_nspawn_image_version` (populated from `/api/v1/system/nspawn-images`). PATCHes server settings. | `panel-ui/src/shells/admin/server-settings/SSHAccessCard.tsx` |
| 10 — UI: User edit drawer pin | D | w/ 8,9 | Per-user "Sandbox image" select in admin user edit (visible only when ssh_sandbox_mode=nspawn AND user has SSH-enabled package). Shows current pin, "(default)" annotation if NULL, dropdown of available image versions, "Upgrade to latest" button. PATCHes user. | `panel-ui/src/shells/admin/users/UserEditDrawer.tsx` |
| 11 — bulk upgrade UI + nspawn-prune CLI | E | w/ 12 | Admin button "Upgrade all users to <latest>" on SSH Access card → bulk PATCH all SSH-enabled users' pins. `jabali nspawn-prune` lists image versions with no users pinned + offers to remove, requires `--yes`. | `panel-ui/src/shells/admin/server-settings/SSHAccessCard.tsx` (button), `panel-cli/cmd/nspawn_prune.go` |
| 12 — E2E + runbook + blueprint flip | E | w/ 11 | Playwright: install → SSH-enable a user → ssh in → verify bubblewrap mounts (cat /etc/shadow denied, ls /home/other ENOENT). Toggle to nspawn → ssh in → verify systemd-detect-virt returns `systemd-nspawn`. Build `debian-12-v2` → upgrade one user → verify pin file content. Runbook covers debootstrap retry, sudoers verification, image upgrade pipeline, prune safety. | `panel-ui/tests/e2e/ssh-sandbox.spec.ts`, `plans/m13-ssh-shell-sandbox-runbook.md`, BLUEPRINT |

---

## 2. Out of scope

- **Per-user mode override** (this user gets nspawn, that user gets
  bubblewrap). Server-wide in v1. Per-user pin is image VERSION inside
  nspawn mode only.
- **Outbound network isolation.** v1 leaves egress open — composer/git/
  wp-cli need it. `--unshare-net` + bridge is a separate design.
- **Resource limits inside sandbox.** M18 cgroup slice covers PHP-FPM;
  SSH session cgroup integration is M18.x.
- **Image content management UI.** Admin can build via CLI; no in-panel
  Dockerfile editor or layer browser.
- **Auto-upgrade of pinned users.** Pinning is a contract — admin
  upgrades explicitly. Bulk button exists but doesn't auto-fire.
- **CRIU / persistent containers.** nspawn is `--ephemeral` always.
- **Outbound mail from sandbox.** sendmail/postfix not in nspawn rootfs;
  document SMTP submission to host in runbook.
- **Custom rootfs distros (Ubuntu, Alpine).** Debian only in v1; the
  CLI's `--codename` flag is forward-compatible plumbing.

---

## 3. Security invariants

- **`/etc/jabali/ssh-sandbox-mode`** owned `root:root 0644`. Allowed
  values: `bubblewrap`, `nspawn`. Anything else → wrapper exits to
  `/usr/sbin/nologin`.
- **Wrapper is `root:root 0755`**, NOT setuid. Runs as the user. bwrap
  branch relies on bwrap's own setuid; nspawn branch uses sudo bridge.
- **`/etc/sudoers.d/jabali-nspawn`** locked to absolute path
  `/usr/local/bin/jabali-nspawn-enter` with no wildcards in args.
  NOPASSWD only for `%jabali-ssh-sandbox`. install.sh runs `visudo -c`
  and aborts on parse error.
- **`jabali-nspawn-enter` helper** is `root:root 0755`, NOT setuid. It
  validates:
    - `$SUDO_USER` resolves to a real Linux user with shell ==
      `/usr/local/bin/jabali-ssh-shell`
    - Argument matches `^[a-z0-9-]+$` and a directory exists at
      `/var/lib/jabali-nspawn/images/<arg>`
    - Refuses any other invocation
- **Bubblewrap profile filters `/etc`**: bind in `/etc/passwd` (filtered
  to user's row + system users), `/etc/group` (similar filter),
  `/etc/hostname`, `/etc/resolv.conf`, `/etc/ssl/certs/*`,
  `/etc/nsswitch.conf`. Hidden: `/etc/shadow`, `/etc/sudoers`,
  `/etc/jabali/`, `/etc/ssh/`, every other tenant's `/home/`.
- **nspawn image is read-only**, mounted with `--ephemeral` so writes
  are overlay'd and discarded. Image dir is `chmod 0555` after build.
- **nspawn `/etc/passwd` filter** at image build: only system users +
  the connecting user are reachable inside (ephemeral overlay rewrites
  on connect). Removes the ability to enumerate other tenants.
- **Pin file** `/etc/jabali/users/<username>/nspawn-image` is
  `root:root 0644`. Reconciler-managed. Wrapper reads only — never
  writes.
- **Image allowlist check** at every wrapper invocation. Pin file
  containing `../` or any non-`[a-z0-9-]` byte is rejected → nologin.
- **No image deletion while pinned.** `nspawn-prune` queries DB for
  active pins on a version before removing.
- **Wrapper integrity**: `dpkg -V bubblewrap` exits 0 at install. Mode
  bits on `/usr/bin/bwrap` checked for setuid. install.sh aborts if
  either fails.
- **Defense-in-depth from M12**: SFTP users still chrooted to
  `/home/<u>` per 7480fff. Wrapper-as-shell is a no-op for them
  (ForceCommand wins) but covers misconfigurations.

---

## 4. Image upgrade pipeline (operator workflow)

1. **Build new version**:
   `jabali nspawn-build --codename debian-12 --version v2`
   Runs debootstrap, post-process script, seals `chmod 0555`.
2. **Verify**:
   `jabali nspawn-test debian-12-v2` boots a throwaway container,
   runs sanity checks (bash present, network ok if expected, etc.).
3. **Promote default** (optional):
   Server Settings → SSH Access → "Default image for new users" →
   `debian-12-v2`. New user-creates from this point pin to v2.
4. **Upgrade existing users** (explicit):
   - Per-user: User edit drawer → Sandbox image → `debian-12-v2`.
   - Bulk: SSH Access card → "Upgrade all users to debian-12-v2" → DB
     bulk update + reconciler fan-out writes new pin files.
5. **Prune old version** (when no pins remain):
   `jabali nspawn-prune` lists candidates, requires `--yes` to delete.

Rollback:
- Set a user's pin back to `debian-12-v1` (PATCH user) → next connect
  uses old image. Image dir is read-only and ephemeral, no state
  carries between connects.

---

## 5. Open questions

1. **Image build reproducibility.** debootstrap pulls latest packages at
   build time; same `--version` string built on different days produces
   different binaries. Acceptable (admin owns the version label) or do
   we want apt-snapshot pinning? Lean accept; document caveat.
2. **Bind-mount `/var/lib/jabali-php-fpm-<u>/`** into nspawn so
   user-driven scripts can poke their own FPM socket? Or force them to
   ssh out and use the API? Lean: bind it in, matches bubblewrap which
   has access to the host paths anyway.
3. **WP-CLI / composer / git versions.** Inside nspawn the user gets
   whatever's in the image. Outside (bubblewrap) they get host
   versions. Documenting the difference in the runbook is enough; not
   trying to match toolchain across modes.

---

## 6. Wave A dispatch criteria

Before dispatching:
- Confirm the version string format `debian-12-v1`, `debian-12-v2`
  (vs alternatives like timestamp `debian-12-2026-04-26`).
- Confirm nspawn is in scope for v1 (vs ship bwrap-only, add nspawn
  in v2 — would drop Steps 4, 5, 11, parts of 6/7/8/9/10/12).
- Confirm chsh of every existing hosting user to wrapper is acceptable
  (one-time mass change at first reconciler sweep after deploy).
