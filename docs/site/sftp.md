# SFTP & SSH Keys

M12.

## What's exposed

- SFTP over the system `sshd` on port **22**, restricted to panel users via `Match Group jabali-sftp` in `/etc/ssh/sshd_config.d/jabali.conf`.
- Interactive shell login is **disabled** for panel users (`ForceCommand internal-sftp`, `X11Forwarding no`, `AllowTcpForwarding no`).
- Chrooted to `/home/<user>` (read-write inside, no escape).

## SSH key management

Each user manages their own keys at `/jabali-panel/ssh-keys`:

- Paste a public key (`ssh-ed25519 …` or `ssh-rsa …`); the panel validates the format.
- Name the key (e.g. "Laptop", "CI").
- Revoke any key from the same page.

The agent writes `~/.ssh/authorized_keys` with `0600` permissions owned by the user. A reconciler tick re-syncs the file on demand and on schedule (15 min, hash-cached so a no-op tick is free).

## Password auth

Disabled by default for panel users. SSH keys are required. Admin can enable password auth per-user via Users → Edit → SSH section, but it is not the default.

## Connection example

```bash
sftp -i ~/.ssh/jabali-key alice@example.com
```

Or with `lftp`, FileZilla, Cyberduck — anything that speaks SFTP.

## What about FTP?

Not shipped. FTP is plaintext; recommending it would undermine the panel's TLS-everywhere stance. Use SFTP.

## What about root SSH?

The host's root account is unchanged; this matters only for the operator, not for panel users. The Jabali installer does not enforce a password-vs-key policy for root — that's your call.
