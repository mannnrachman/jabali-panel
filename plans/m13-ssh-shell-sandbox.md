# M13 — SSH shell sandbox (bubblewrap or systemd-nspawn)

**Status:** Plan drafted, not dispatched.
**Goal:** SSH-enabled hosting users (those NOT in `jabali-sftp` group) get an
optional sandboxed shell. Admin picks one of three modes per server: `none`
(plain bash), `bubblewrap` (lightweight namespaced shell), `nspawn`
(systemd-nspawn ephemeral container). Toggle lives in Server Settings →
SSH Access. SFTP-only users are unaffected (chroot path covers them
already; this plan is about shell sessions).

---

## 0. Key design decisions

1. **Wrapper-as-login-shell, not ForceCommand.**
   Set `/usr/local/bin/jabali-ssh-shell` as every hosting user's login
   shell. Wrapper reads `/etc/jabali/ssh-sandbox-mode` on each connect and
   exec's the matching backend (or falls through to `/bin/bash` when
   `mode=none`). Switching modes never touches sshd or chsh — admin write
   to one file, next connect picks it up.

2. **One server-wide setting. No per-user overrides in v1.**
   Stored as `ssh_sandbox_mode` in `server_settings` (existing table).
   Values: `none` | `bubblewrap` | `nspawn`. Default: `none` (matches
   current behavior, zero migration risk).

3. **Bubblewrap runs as the user via setuid bwrap binary.**
   Debian/Ubuntu ship `bwrap` setuid root by default — wrapper exec's it
   directly post-privilege-drop. No sudo, no extra capabilities.

4. **systemd-nspawn requires root entry path; sudo NOPASSWD bridge.**
   nspawn needs CAP_SYS_ADMIN. sshd has dropped to user UID by the time
   the login shell runs. Bridge: install `/etc/sudoers.d/jabali-nspawn`
   that lets members of group `jabali-ssh-sandbox` run
   `/usr/local/bin/jabali-nspawn-enter` as root with NOPASSWD. The
   sudoers entry is locked to that single absolute path with no args, no
   shell escape.

5. **nspawn template is a shared read-only rootfs at
   `/var/lib/jabali-nspawn/rootfs`.**
   Built once at install time via `debootstrap stable` (~250 MB). Every
   session boots a transient container off this template + bind-mounts
   `/home/<user>` rw, `/etc/jabali/host-passwd` (filtered passwd entry
   for the user only, ro), and a tmpfs `/tmp`. No outbound network
   isolation in v1 — keeps WordPress/composer/git network access intact.

6. **Bubblewrap profile: read-only system, writable home, no extra net
   isolation.**
   Mounts: `--ro-bind /usr /usr`, `--ro-bind /etc /etc` (filtered — see
   §3), `--bind /home/<user> /home/<user>`, `--proc /proc`, `--dev /dev`,
   `--tmpfs /tmp`. Other users' homes are not bound, so cross-tenant `ls`
   becomes "no such file" instead of relying on UID perms.

7. **No chsh churn on mode toggle.**
   Wrapper is always the shell for SSH-enabled users; mode change is
   purely a setting flip. SFTP users keep `ForceCommand internal-sftp`
   from the existing Match block — wrapper never runs for them.

8. **Reconciler owns the shell flip.**
   Same place the SFTP group flip happens: when `package.ssh_enabled`
   becomes true, reconciler calls `ssh.user.set_shell` with
   `/usr/local/bin/jabali-ssh-shell`; when false, sets `/usr/sbin/nologin`
   (defense-in-depth — Match block's ForceCommand is the real gate).

9. **Setting change requires no reload.**
   Wrapper reads `/etc/jabali/ssh-sandbox-mode` on every connect. No
   sshd reload, no agent fan-out — just an atomic file write.

10. **Audit log is sshd's own session log.**
    No per-sandbox audit layer in v1. CrowdSec already monitors sshd auth.

---

## 1. Steps / waves

| Step | Wave | Parallel? | Summary | Outputs |
|------|------|-----------|---------|---------|
| 1 — ADR | A | w/ 2 | Record "wrapper-as-shell + setuid bwrap + sudo-bridged nspawn" decision; surface tradeoffs vs ForceCommand and per-user shells. | `docs/adr/0067-ssh-shell-sandbox.md` |
| 2 — install foundations | A | w/ 1 | Add `bubblewrap` to `apt install` list. Create `/etc/jabali/` dir. Ship `install/ssh/jabali-ssh-shell` wrapper. Write default `/etc/jabali/ssh-sandbox-mode` = `none`. Create `jabali-ssh-sandbox` group. | `install.sh`, `install/ssh/jabali-ssh-shell`, `update.go` sync |
| 3 — bubblewrap profile + smoke | B | w/ 4 | Wrapper's bwrap branch with the v1 mount profile (§0.6). Manual smoke: bwrap → bash → `ls /home/<other-user>` returns ENOENT. | `install/ssh/jabali-ssh-shell` (bwrap branch), `tests/smoke/bwrap-isolation.sh` |
| 4 — nspawn template + enter helper | B | w/ 3 | install.sh: debootstrap rootfs at `/var/lib/jabali-nspawn/rootfs`; ship `install/ssh/jabali-nspawn-enter`; install `/etc/sudoers.d/jabali-nspawn`. Wrapper's nspawn branch sudo-exec's enter helper. | `install.sh` (debootstrap step), `install/ssh/jabali-nspawn-enter`, `install/ssh/jabali-nspawn-sudoers` |
| 5 — agent commands | C | — | `system.set_ssh_sandbox_mode` (writes `/etc/jabali/ssh-sandbox-mode` atomically + validates value); `ssh.user.set_shell` (chsh helper). | `panel-agent/internal/commands/system_set_ssh_sandbox_mode.go`, `ssh_user_set_shell.go` |
| 6 — server_settings key + API | C | — | Add `ssh_sandbox_mode` default seed to `ServerSettingsRepository.EnsureDefault`. Extend system settings GET/PATCH to expose+update it. Reconciler hook fires `system.set_ssh_sandbox_mode` on change. | `panel-api/internal/repository/server_settings.go`, `internal/api/server_settings.go`, reconciler |
| 7 — reconciler shell flip | D | w/ 8 | When user gains/loses SSH (via package), reconciler calls `ssh.user.set_shell` with wrapper or nologin. Idempotent. | `panel-api/internal/reconciler/ssh_keys_reconcile.go` |
| 8 — UI: Server Settings → SSH Access "Shell Sandbox" select | D | w/ 7 | AntD Select with three options + inline help text ("Bubblewrap: lightweight namespacing. nspawn: full container, ~250 MB rootfs, slower start. None: plain shell."). PATCHes server settings. | `panel-ui/src/shells/admin/server-settings/SSHAccessCard.tsx` |
| 9 — E2E + runbook + blueprint flip | E | — | Playwright: toggle each mode, SSH in, run `ls /home/`, verify isolation behavior matches mode. Runbook covers debootstrap retry, sudoers verification, mode-switch ops. | `panel-ui/tests/e2e/ssh-sandbox.spec.ts`, `plans/m13-ssh-shell-sandbox-runbook.md`, BLUEPRINT |

---

## 2. Out of scope

- **Per-user sandbox override.** v1 is server-wide. v2 could store
  `users.ssh_sandbox_mode_override` if real demand surfaces.
- **Network isolation.** v1 leaves egress open — composer/git/wp-cli need
  it. Adding `--unshare-net` plus a NAT bridge is a separate design.
- **Resource limits inside sandbox.** M18 cgroup slice already binds
  PHP-FPM; SSH session cgroup integration is M18.x.
- **Image management UI.** Admin doesn't pick rootfs version; install
  fixes it to `debian stable`. Upgrades are operator-driven.
- **CRIU / persistent containers.** nspawn here is ephemeral —
  `--ephemeral` flag, every connect is a fresh overlay.
- **Outbound mail from sandbox.** sendmail/postfix not in nspawn rootfs;
  use SMTP submission to host instead. Documented in runbook.

---

## 3. Security invariants

- **`/etc/jabali/ssh-sandbox-mode`** owned `root:root 0644`. Wrapper
  reads with `cat`; only `none|bubblewrap|nspawn` accepted, anything else
  falls through to `none` (fail-safe).
- **Wrapper is `root:root 0755`**, not setuid. It runs as the user;
  bubblewrap branch relies on bwrap's own setuid bit; nspawn branch
  exec's `sudo /usr/local/bin/jabali-nspawn-enter`.
- **`/etc/sudoers.d/jabali-nspawn`** locks to absolute path, no
  arguments, NOPASSWD only for group `jabali-ssh-sandbox`. Verified with
  `visudo -c` post-install.
- **nspawn-enter helper** is `root:root 0755`, NOT setuid. Sudo is the
  privilege bridge. It validates `$SUDO_USER` matches a real hosting
  user and refuses any other invocation. Hardcoded image path; no
  caller-controlled flags.
- **Bubblewrap profile filters `/etc`**: bind in `/etc/passwd`,
  `/etc/group`, `/etc/hostname`, `/etc/resolv.conf`, `/etc/ssl/certs/*`,
  `/etc/nsswitch.conf`. Anything else (`/etc/shadow`, `/etc/sudoers`,
  `/etc/jabali/`) is hidden.
- **nspawn rootfs filtered passwd**: container's `/etc/passwd` contains
  only the connecting user's entry + system users needed by bash. Bind-
  mount is read-only. No way for the user to see other tenants exist.
- **SSH-shell users in `jabali-ssh-sandbox` group** for nspawn sudoers
  entry. Reconciler manages group membership in lockstep with the
  jabali-sftp group flip (mutually exclusive in v1).
- **Mode validation** at every layer: API rejects unknown values; agent
  command rejects unknown values; wrapper falls through to `none` on
  unknown values. Defense in depth.
- **bwrap binary integrity check** at install: `dpkg -V bubblewrap`
  exits 0. Fail install if not.

---

## 4. Open questions for review

1. **Default to `bubblewrap` instead of `none`?** New installs would get
   isolation by default, but adds dependency surface. Lean toward `none`
   default for backward compat; admin opts in.
2. **Should SFTP-only users also get the wrapper?** Currently no — they
   have `ForceCommand internal-sftp` which overrides shell. But a
   defense-in-depth move would set wrapper as their shell too, so any
   misconfiguration falls through to a sandboxed bash instead of bare
   bash.
3. **nspawn rootfs version policy.** Pin to current Debian stable at
   install time, or bump on every `jabali update`? Leaning pin, with
   manual `jabali nspawn-rebuild` CLI for upgrades.
4. **Bind-mount `/home/<user>` vs the chroot path.** SFTP path uses
   chroot to `/home/<u>` with `root:<u> 0751`. SSH wrapper bind-mounts
   `/home/<u>` rw — but user owns it as `<u>:www-data 0750` in SSH mode.
   Compatible; just want to confirm we don't double-flip on mode toggle.
5. **First-connect latency for nspawn.** `systemd-nspawn --ephemeral`
   typically <1s on warm cache, 3-5s cold. Acceptable for SSH? Document
   in runbook.

---

## 5. Wave A dispatch criteria

Before dispatching:
- Confirm decision §0.1 (wrapper-as-shell) vs alternative (Match User
  ForceCommand). Trade: wrapper is one file + chsh; ForceCommand is
  per-user sshd_config drop-in + reload.
- Confirm decision §0.4 (sudo bridge for nspawn) vs alternative (drop
  nspawn from v1, ship bubblewrap-only, add nspawn in v2). Trade:
  bubblewrap-only is ~half the steps and removes the sudo surface.
- Confirm default `ssh_sandbox_mode = none`.

If §0.4 is dropped, Steps 4 + parts of 5/8 become "future work" and the
plan shrinks to ~6 steps.
