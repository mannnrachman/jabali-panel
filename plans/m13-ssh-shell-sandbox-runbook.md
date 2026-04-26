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

The `jabali nspawn-build` CLI is not yet implemented (M13 plan §4).
Manual build instructions until it ships:

```bash
# Pin to a snapshot.debian.org timestamp (mandatory for determinism).
SNAPSHOT=20260426T000000Z
VERSION=v1
CODENAME=debian-12

IMG_DIR=/var/lib/jabali-nspawn/images/${CODENAME}-${VERSION}
mkdir -p "$IMG_DIR"

debootstrap --variant=minbase \
  --include=bash,coreutils,procps,findutils,grep,sed,gawk,less,nano,git,curl,ca-certificates \
  bookworm "$IMG_DIR" \
  https://snapshot.debian.org/archive/debian/${SNAPSHOT}/

# Seal: read-only after build.
chmod -R a-w "$IMG_DIR"
chmod 0555 "$IMG_DIR"

# Manifest for reproducibility.
{
  echo "{\"snapshot\": \"$SNAPSHOT\", \"codename\": \"$CODENAME\", \"version\": \"$VERSION\","
  echo "  \"rootfs_sha256\": \"$(find "$IMG_DIR" -type f -exec sha256sum {} \\; | sort | sha256sum | cut -d' ' -f1)\"}"
} > "$IMG_DIR/MANIFEST.json"
```

Once built and `default-nspawn-image` matches, switching mode to
`nspawn` lets users connect via the helper.

## Add a user to the nspawn sudoers group

Required for nspawn mode (bubblewrap mode doesn't need it). The
reconciler will manage this group automatically once the M13 step 8
extension lands; manual today:

```bash
usermod -aG jabali-ssh-sandbox <username>
```

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
User isn't in `jabali-ssh-sandbox` group. `usermod -aG
jabali-ssh-sandbox <user>` (until reconciler manages it).

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

## What's not yet shipped

- `jabali nspawn-build` CLI (manual build above is the workaround)
- Admin UI for mode + default image (edit `/etc/jabali/` files instead)
- Per-user pin UI (future)
- Reconciler-managed `jabali-ssh-sandbox` group membership (manual
  `usermod` for now)
- E2E specs (manual smoke above)
