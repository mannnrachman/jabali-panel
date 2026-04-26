# M13 — SSH shell sandbox runbook

Operator guide for the bubblewrap/nspawn sandbox shipped in
`feat/m14-api-notifications` (commits `ec58009`, `0067…`, `7ff8b42`).

## What's running

Every hosting user (SFTP and SSH) has `/usr/local/bin/jabali-ssh-shell`
as their login shell. Wrapper reads `/etc/jabali/ssh-sandbox-mode` on
each connect:

- `bubblewrap` (default) — exec's `/usr/bin/bwrap` with the v1 mount
  profile. SSH-shell users land in a namespaced bash with `/home/<u>`
  bind-mounted rw, host `/usr` ro, filtered `/etc`, fresh tmpfs `/tmp`,
  `/run`, `/var`. No host sockets bound.
- `nspawn` — sudo's into `/usr/local/bin/jabali-nspawn-enter <image>`
  which exec's `systemd-nspawn --ephemeral` against the pinned image.
  Requires the image to be built (see "Build a nspawn image" below).
- Anything else (file missing, unknown value, wrapper failure) — exec's
  `/usr/sbin/nologin`. SSH session terminates with no shell. SFTP path
  still works because `ForceCommand internal-sftp` from
  `jabali-sftp.conf` gates SFTP at the sshd level.

## Files

| Path | Purpose |
|------|---------|
| `/usr/local/bin/jabali-ssh-shell` | Login-shell wrapper |
| `/usr/local/bin/jabali-nspawn-enter` | sudo-bridged nspawn launcher |
| `/etc/sudoers.d/jabali-nspawn` | NOPASSWD for `jabali-ssh-sandbox` group |
| `/etc/jabali/ssh-sandbox-mode` | `bubblewrap` or `nspawn` |
| `/etc/jabali/default-nspawn-image` | Default image for new users |
| `/etc/jabali/users/<u>/nspawn-image` | Per-user image pin (optional) |
| `/var/lib/jabali-nspawn/images/<codename>-<version>/` | Image rootfs |

## Toggle sandbox mode

```bash
# Switch to nspawn for all SSH-shell users (next connect picks it up):
echo nspawn > /etc/jabali/ssh-sandbox-mode

# Switch back to bubblewrap:
echo bubblewrap > /etc/jabali/ssh-sandbox-mode
```

No sshd reload, no agent fan-out. Active sessions are not affected
(they stay on whatever they entered with).

Admin UI for the toggle is a follow-up step (M13 plan §9). Until then,
the file is the source of truth.

## Build a nspawn image

```bash
# --snapshot is mandatory. Pick a YYYYMMDDTHHMMSSZ timestamp from
# https://snapshot.debian.org/archive/debian/ — same triple of
# (codename, version, snapshot) produces a bit-identical rootfs.
jabali nspawn build --codename debian-12 --version v1 \
  --snapshot 20260426T000000Z

# List sealed images:
jabali nspawn list

# Remove images no user is pinned to (and that aren't the default):
jabali nspawn prune          # dry-run
jabali nspawn prune --yes    # actually delete
```

`jabali nspawn build` runs debootstrap against snapshot.debian.org,
strips apt cache + machine-id, computes a SHA-256 of the entire
rootfs, writes `MANIFEST.json` (codename, version, snapshot, package
list, sha256, built_at), then `chmod -R a-w` + `chmod 0555` to seal
the image. Refuses to overwrite an existing image — bump `--version`
to build a new one.

## Per-user image pin

Admin user-edit drawer (or `PATCH /admin/users/<id>` with body
`{"nspawn_image_version": "debian-12-v2"}`) sets the per-user pin.
Reconciler mirrors the pin to `/etc/jabali/users/<u>/nspawn-image`
on the next sweep. `nspawn_image_version: null` (or empty string)
clears the pin so the user falls back to the server-wide default.

Existing users with `NULL` pins get stamped with the server default
on the next reconciler sweep — they are then preserved across future
default-image bumps. Promoting a default-image change therefore does
NOT silently move pinned users.

## Test isolation (smoke)

```bash
# As an SSH-enabled hosting user:
ssh <user>@<panel-host>

# Inside the sandbox:
ls /etc/jabali/                    # → "No such file or directory"
ls /home/<other-tenant>/           # → "No such file or directory"
cat /etc/shadow                    # → "Permission denied"
ls /run/php/                       # → tmpfs is empty, FPM sockets hidden
cat /etc/passwd | head -3          # → host /etc/passwd visible
                                   #   (filtered /etc/passwd is v2)
systemd-detect-virt                # → "systemd-nspawn" if mode=nspawn
                                   #   "none" if mode=bubblewrap
```

## Common issues

**`This account is currently not available.`**
Wrapper exited to `/usr/sbin/nologin`. Causes:
- `/etc/jabali/ssh-sandbox-mode` missing or contains an unknown value
- Mode is `nspawn` but pinned image directory doesn't exist
- Mode is `bubblewrap` but `/usr/bin/bwrap` is not setuid
  (`dpkg-reconfigure bubblewrap` or reinstall the package)

Check journalctl for sshd's session log + the user's `~/.bash_history`
(only populated if a previous session got past the wrapper).

**`sudo: a password is required`** (nspawn mode)
User isn't in `jabali-ssh-sandbox` group. Reconciler manages this
on every sweep; if it hasn't run yet, `usermod -aG jabali-ssh-sandbox
<user>` works as a manual nudge.

**wp-cli: `Error establishing a database connection`**
Expected. No host sockets bound (ADR-0067 §0.14). Use the panel UI's
phpMyAdmin SSO or the Applications "Run command" feature for
DB-touching workflows. A scoped TCP proxy is on the v2 roadmap.

## Rollback to plain bash

If sandbox is causing operational issues and you need to revert the
shell change for a single user:

```bash
chsh -s /bin/bash <username>
```

For the whole host (revert sandbox to off):

```bash
# Stop the reconciler from re-flipping shells:
systemctl stop jabali-panel-api

# Mass revert:
for u in $(getent passwd | awk -F: '$3>=1000 && $3<60000 {print $1}'); do
  chsh -s /bin/bash "$u"
done

# Block the wrapper so it can't run even if a session tries:
chmod 000 /usr/local/bin/jabali-ssh-shell
```

Then root-cause whatever made the sandbox unusable before bringing
panel-api back up.

## Bulk image upgrade

To force every SSH-enabled user onto a new image (e.g. shipping a
security-fixed `debian-12-v2`):

```sql
-- Connect as root via socket: mariadb -uroot
USE jabali;
UPDATE users SET nspawn_image_version = 'debian-12-v2'
  WHERE username IS NOT NULL;
```

Then update the server-wide default in Server Settings → SSH Access →
"Default nspawn Image" so new users also pin to the new version.
Reconciler will materialize per-user pin files within 60 seconds.

To clear all pins (let the server default apply uniformly):

```sql
UPDATE users SET nspawn_image_version = NULL WHERE username IS NOT NULL;
```

Old image versions remain on disk until `jabali nspawn prune --yes`.

## v2 roadmap

- Scoped TCP proxy for in-sandbox MariaDB / FPM access (currently
  blocked by ADR-0067 §0.14).
- `--unshare-net` + per-user NAT bridge for outbound network isolation.
- Image content signing + supply-chain provenance (cosign).
