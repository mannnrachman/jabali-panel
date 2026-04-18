# M12 — SFTP access via openssh (integration plan)

**Status:** Dispatched (Wave A in flight).
**Goal:** Users upload SSH public keys in the panel and SFTP into their homedir. No shell. No chroot filesystem restructure.

## 0. Key design decisions

1. **No `ChrootDirectory`.** It would require `/home/<user>/` to be root-owned, which breaks FPM pools, filebrowser, WordPress docroots, and every other "user owns their homedir" assumption. Instead: UID permissions enforce isolation. `/home/<user>/` is already mode 0750, so users can't `ls` each other. Users can see `/etc`, `/usr`, etc. as read-only — standard Linux, no leak beyond what any shell user would see.
2. **Single sshd_config drop-in** `/etc/ssh/sshd_config.d/jabali-sftp.conf` with a `Match Group jabali-sftp` block. No per-user config files. Users in the `jabali-sftp` group are restricted to `internal-sftp`; no TCP forwarding, no X11, no tunnels. Adding/removing users is a group-membership change, no sshd reload needed (groups are checked per-session).
3. **SSH keys live in `/home/<user>/.ssh/authorized_keys`** (standard). Reconciler writes them with correct mode/ownership; panel stores them in DB as the source of truth.
4. **One table `ssh_keys`** (id, user_id, name, public_key, fingerprint, created_at). Public-key format validated server-side (ssh-rsa, ssh-ed25519, ecdsa-sha2-*; 2048-bit minimum for RSA).
5. **Admin view is list-only.** Revoking is done by impersonating the user — no separate admin CRUD path. Matches M11/M10 pattern.

## 1. Steps / waves

| Step | Wave | Parallel? | Summary | Outputs |
|------|------|-----------|---------|---------|
| 1 — ADR-0028 | A | w/ 2 | Record "no chroot, group-based Match, shared sshd drop-in" decision. | `docs/adr/0028-m12-sftp-integration.md` |
| 2 — install.sh + sshd drop-in | A | w/ 1 | Create `jabali-sftp` group; ship `install/ssh/jabali-sftp.conf`; install.sh copies to `/etc/ssh/sshd_config.d/jabali-sftp.conf`; reload sshd. | `install.sh`, `install/ssh/jabali-sftp.conf`, `update.go` sync |
| 3 — migration + model + repo | B | w/ 4 | `ssh_keys` table + CRUD repo. Fingerprint computed server-side with `crypto/ssh.FingerprintSHA256`. | migration, model, repo |
| 4 — agent commands | B | w/ 3 | `ssh.authorized_keys.write` (given user + array of key strings, writes `~/.ssh/authorized_keys` atomically), `ssh.authorized_keys.delete`, `ssh.user.join_sftp_group` (`usermod -aG jabali-sftp <user>`). | `panel-agent/internal/commands/ssh_*.go` |
| 5 — API + reconciler | C | — | CRUD handlers at `/api/v1/ssh-keys`. Reconciler: ensures every jabali Linux user is in `jabali-sftp` group; writes authorized_keys on change. | `panel-api/internal/api/ssh_keys.go`, reconciler func |
| 6 — UI page | D | — | User-shell "SSH Keys" page: list + add + delete. Fingerprint displayed. Add modal parses the pasted public key client-side for quick validation. | `panel-ui/src/shells/user/ssh-keys/UserSSHKeysPage.tsx` + refine resource |
| 7 — E2E + runbook + blueprint flip | E | — | Playwright happy path: add key → SFTP connect → list files → upload → disconnect. Runbook. Status flip. Memory pointer. | `tests/e2e/sftp.spec.ts`, `plans/m12-sftp-runbook.md`, BLUEPRINT update |

## 2. Out of scope

- **Password auth.** SFTP is key-only. `PasswordAuthentication no` in the group block.
- **SSH shell access.** That's M13.
- **SFTP logging / audit.** sshd's default logging is enough for v1.
- **Quotas.** M14+.
- **Host key rotation.** System admin concern, not user-facing.

## 3. Security invariants

- sshd_config drop-in: `ForceCommand internal-sftp`, `AllowTcpForwarding no`, `X11Forwarding no`, `PermitTunnel no`, `PasswordAuthentication no` — apply to `Match Group jabali-sftp`.
- authorized_keys file: mode 0600, owner `<user>:<user>`. `.ssh/` dir: mode 0700, same owner. Reconciler enforces both on every write.
- Public keys validated via `golang.org/x/crypto/ssh.ParseAuthorizedKey` before DB insert; reject unparseable, reject RSA <2048-bit.
- Never return private keys. Panel only ever stores the public half.

## 4. Wave A dispatch (NOW)

- Agent 1 → Step 1 (ADR-0028) — worktree isolation.
- Agent 2 → Step 2 (install.sh + sshd drop-in + update.go sync) — worktree isolation.
