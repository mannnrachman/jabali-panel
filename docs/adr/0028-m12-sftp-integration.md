# ADR-0028: M12 SFTP access via openssh group-based Match (no chroot)

**Date:** 2026-04-18
**Status:** Accepted
**Deciders:** Shuki

## Context

Users need file upload capability beyond the web-based file manager (M11). SFTP
(SSH File Transfer Protocol) is the industry standard for remote file access on
hosting platforms. The question is how to integrate SFTP without breaking existing
assumptions about user home directory ownership and without requiring complex
filesystem restructuring.

Two primary challenges emerge:

1. **Filesystem isolation:** Traditional SFTP integration via `ChrootDirectory`
   requires `/home/<user>/` to be root-owned with restricted permissions. This
   conflicts with the existing Jabali architecture where users own their homedirs,
   FPM pools run as the user, file browser scopes are user-relative, and WordPress
   docroots live in user-writable space (e.g., `/home/<user>/public_html`).
   Restructuring to a chroot-compatible layout (e.g., `/home/<user>/home/` inside
   a root-owned `/home/<user>/`) would require ~2 weeks of migration effort with
   zero user-facing benefit beyond what Unix UID/GID permissions already provide.

2. **User isolation pattern:** Should SFTP isolation rely on filesystem structures
   (chroot), system-level containers (nspawn), or Unix permissions (group-based)?
   The per-user filesystem isolation goal was explored in M11 with nspawn (see
   spike memo at `plans/m11-nspawn-spike-memo.md`), but that approach was rejected
   as over-engineered for MVP. Industry-standard openssh SFTP-via-Match works
   without containers.

## Decision

We are integrating SFTP using **openssh native `Match Group jabali-sftp` blocks**
with **`ForceCommand internal-sftp`** and **NO `ChrootDirectory`**. The key design
decisions are:

1. **No chroot filesystem restructure.** UID/GID permissions enforce isolation.
   `/home/<user>/` is already mode 0750 (user rwx, group rx), so users cannot `ls`
   each other's homes. Users CAN see `/etc`, `/usr/share`, etc. as read-only — this
   is standard on any Linux host and acceptable for v1 (matches cPanel-style hosting).

2. **Single sshd_config drop-in** `/etc/ssh/sshd_config.d/jabali-sftp.conf` with
   `Match Group jabali-sftp` block. No per-user config files. Users in the
   `jabali-sftp` group are restricted to `internal-sftp`; no TCP forwarding, no X11,
   no tunnels. Group membership is checked per-session; adding/removing users is
   simply a group-membership change (no sshd reload needed).

3. **SSH keys live in `/home/<user>/.ssh/authorized_keys`** (standard Unix location).
   The reconciler writes them with correct mode/ownership; the panel stores them in
   the DB as source of truth.

4. **One table `ssh_keys`** (id, user_id, name, public_key, fingerprint, created_at).
   Public-key format is validated server-side via `golang.org/x/crypto/ssh.ParseAuthorizedKey`;
   RSA keys <2048-bit are rejected. Fingerprints are computed server-side with
   `crypto/ssh.FingerprintSHA256`.

5. **Admin list-only view.** Revoking keys is done by impersonating the user — no
   separate admin CRUD path. Matches the M11/M10 admin pattern (see ADR-0027, step 5).

6. **Key-only authentication.** `PasswordAuthentication no` in the group block.
   No password fallback.

## Design Decisions

### 1. No ChrootDirectory — UID permissions instead

**Decision:** Do not use `ChrootDirectory /home/<user>` isolation. Rely on standard
Unix UID/GID permissions (mode 0750 on homedir) and openssh's `ForceCommand` to
restrict users to SFTP-only access.

**Rationale:**

`ChrootDirectory` requires `/home/<user>/` to be root-owned with `drwx------` or
`drwx--x---` permissions. This breaks every existing assumption in Jabali:
- FPM pools (M9) expect to run as the user and write to `~/php-pool/` files.
- Filebrowser (M11) expects to read user files as a member of the user's group.
- WordPress docroots (M10) are in `~/public_html` and must be writable by the user.
- Custom application logic expects users to own their homes.

Restructuring to a chroot-compatible layout (e.g., `~/.sftp-root/home/<user>/` with
symlinks, or moving everything into a subdirectory) would require:
- Rewriting all domain docroot paths (nginx, PHP, WordPress imports).
- Migrating all user homedir content to new paths.
- Updating FPM pool configs to point to new paths.
- Updating filebrowser scopes.
- Estimated effort: ~2 weeks of migration work for zero user-facing benefit.

Since `/home/<user>/` is already mode 0750 (enforced by `install.sh useradd`), users
cannot `ls` each other's homes anyway. Users can see `/etc`, `/usr/share`, etc. as
any Linux user would — accepted information leak, standard on shared hosting.

**Consequences:**
- Isolation is weaker than full-filesystem chroot but sufficient for v1.
- Users can observe read-only system files (e.g., `/etc/ssl/certs/`, `/usr/share/doc/`).
  This is acceptable; it matches cPanel-style hosts.
- If a hypothetical openssh bug allows breaking out of `ForceCommand internal-sftp`,
  the user lands in a restricted shell (not full shell), still unable to write outside
  their home.

### 2. Group-based Match block in single drop-in config

**Decision:** Create `/etc/ssh/sshd_config.d/jabali-sftp.conf` with a single
`Match Group jabali-sftp` block applied to all SFTP-enabled users. No per-user
config files.

**Rationale:**
- Centralized configuration is easier to audit and maintain.
- sshd checks group membership per-session; adding/removing users from the group
  takes effect on the next login without reloading sshd.
- Single drop-in file scales to 100+ users without config explosion.

**SFTP group block (example):**
```
Match Group jabali-sftp
    ForceCommand internal-sftp
    AllowTcpForwarding no
    X11Forwarding no
    PermitTunnel no
    PasswordAuthentication no
    GatewayPorts no
    StreamLocalBindUnlink no
```

**Consequences:**
- All SFTP users are restricted identically (no per-user tweaks).
- Group membership is the only lever for enabling/disabling SFTP.
- The reconciler (Step 4) runs `usermod -aG jabali-sftp <user>` to enable SFTP.

### 3. SSH keys in standard location, reconciler-managed

**Decision:** Store public keys in the standard Unix location `/home/<user>/.ssh/authorized_keys`
(mode 0600, owned by the user). The reconciler writes this file atomically based on
the DB (`ssh_keys` table).

**Rationale:**
- Standard Unix convention; works with any SSH client without special config.
- Panel is the source of truth (DB); reconciler converges the filesystem.
- No custom sshd configuration needed for key paths.

**Reconciler logic (Step 4):**
```
For each jabali user:
  Read ssh_keys rows WHERE user_id = user.id AND deleted_at IS NULL
  Write /home/<user>/.ssh/authorized_keys atomically with those keys
  Set mode 0600, owner <user>:<user>
```

**Consequences:**
- Key revocation is write-free: just mark the row `deleted_at` in the DB, and the
  reconciler removes it on next tick.
- Users cannot manually edit `authorized_keys` (reconciler overwrites it); all
  key management is via the panel UI.
- Lost DB entries are lost keys (no recovery except restore-from-backup).

### 4. Public key validation and fingerprinting

**Decision:** Validate all public keys server-side via `golang.org/x/crypto/ssh.ParseAuthorizedKey`.
Reject keys that cannot be parsed or are RSA <2048-bit. Compute SHA256 fingerprint
server-side with `crypto/ssh.FingerprintSHA256`.

**Rationale:**
- Parsing at insertion time prevents invalid keys from being stored.
- Fingerprint is displayed in the UI for user verification (helps users find their
  own key if they upload multiple).
- Client-side validation is convenient but can be spoofed; server-side validation
  is the source of truth.

**Validation rules:**
- Supported key types: `ssh-rsa` (2048+ bits), `ssh-ed25519`, `ecdsa-sha2-nistp256`,
  `ecdsa-sha2-nistp384`, `ecdsa-sha2-nistp521`.
- Reject RSA keys <2048-bit (old, weak).
- Reject unparseable keys (malformed, corrupted).

**Consequences:**
- Users attempting to upload RSA-1024 keys will get a clear error message (backward
  compatible with their existing SSH setups is not a goal for v1).
- Fingerprints are deterministic; same key uploaded twice produces the same fingerprint.

### 5. Admin list-only, revoke via impersonation

**Decision:** Admin UI for SSH keys is **list-only**. Revocation is done by
impersonating the user (same pattern as M11/M10).

**Rationale:**
- Simplifies admin logic (no separate delete endpoint).
- Reuses the existing impersonation flow (ADR-0015).
- Ensures admins see the same UI as users; consistency.

**Admin flow:**
1. Admin navigates to the user's SSH keys page (via impersonation).
2. Admin clicks delete on a key.
3. Panel marks the key `deleted_at = now()`.
4. Reconciler removes it from `authorized_keys` on next tick.

**Consequences:**
- Admins must impersonate to revoke keys (no direct admin delete).
- Revocation is eventually consistent (~few seconds until reconciler runs).

## Consequences

### Positive

- **No filesystem restructure:** Avoids ~2 weeks of migration work; reuses existing
  user-owns-homedir assumption (FPM, filebrowser, WordPress).
- **Industry standard:** openssh SFTP-via-Match is used by cPanel, Plesk, and other
  hosters; proven, well-understood.
- **Simple group-based control:** Adding/removing SFTP access is a single `usermod`
  call; no sshd reconfig needed.
- **Reconciler-driven:** Public keys are DB-sourced; panel is source of truth.
- **Consistent with existing patterns:** Admin list-only + impersonation mirrors
  M11/M10 (filebrowser, WordPress).
- **No password complexity:** Key-only auth eliminates password management overhead.

### Negative

- **Weaker isolation than chroot:** Users can observe read-only system files
  (`/etc`, `/usr/share`). Acceptable for v1 (matches industry standard).
- **Cross-service reconciliation:** If reconciler fails to write `authorized_keys`,
  keys are in the DB but not accessible. Reconciler logs errors; manual intervention
  required.
- **Group-membership dependency:** sshd checks group membership; if AD/LDAP is
  introduced later, group sync must be reliable.
- **No per-user tuning:** All SFTP users have identical restrictions. Fine-grained
  per-user quotas or rate limits require additional infrastructure (deferred).

### Risks

- **Malicious sshd bugs:** A hypothetical critical openssh vulnerability in `ForceCommand`
  or `Match` could break isolation. Mitigation: monitor openssh CVEs closely; consider
  nspawn sandboxing in M13+ if needed.
- **Filesystem permission bypass:** If a user-writable `~/.ssh/` is exploited to
  inject unauthorized_keys, SFTP access is compromised. Mitigation: reconciler
  enforces `authorized_keys` mode 0600 on every write; standard umask enforcement
  prevents user edits.
- **DB drift:** If the panel crashes during key update, DB and filesystem may diverge
  until reconciler next runs. Mitigation: reconciler runs frequently (every 5–10 sec);
  any divergence is short-lived.

## Alternatives Considered

### Alternative 1: ChrootDirectory with filesystem restructure

**Option:** Use `ChrootDirectory /home/<user>` and restructure all user homedirs to
a chroot-compatible layout.

**Pros:**
- Strongest isolation: users cannot see any files outside their chroot.
- No need to trust openssh or sshd group logic; filesystem alone enforces boundaries.

**Cons:**
- ~2 weeks of migration work to restructure user homedirs (M9, M10, M11).
- Breaking change for existing customers (if any).
- Requires rewriting all domain docroot paths, FPM pool configs, filebrowser scopes.
- Zero user-facing benefit (UID permissions already prevent cross-user access).

**Decision:** Rejected for MVP. If customers demand stricter isolation post-launch,
revisit in M13+.

### Alternative 2: Per-user nspawn containers

**Option:** Run each user's SFTP session in a systemd-nspawn container with a
chroot filesystem.

**Pros:**
- Strong isolation: full OS-level sandbox (filesystem, network, IPC).
- Solves the "universal per-user isolation primitive" problem (matches M11 spike).

**Cons:**
- Per-session container overhead (fork/exec cost for each SSH login).
- Requires container image and init system (complexity).
- Deferred in M11 spike (`plans/m11-nspawn-spike-memo.md`) for good reason:
  over-engineered for MVP; openssh group-based Match is industry standard.
- Would require revisiting M11 decision (filebrowser is not containerized).

**Decision:** Rejected for consistency with M11. nspawn is deferred as a universal
isolation layer (M13+).

### Alternative 3: Per-user `Match User` blocks instead of group

**Option:** Create a separate `Match User <username>` block for each SFTP-enabled user
in the sshd config drop-in.

**Pros:**
- Per-user configuration possible (fine-grained control).
- Users who should not have SFTP are omitted.

**Cons:**
- Config explosion: N users = N `Match User` blocks (~500 lines for 100 users).
- Requires sshd config reload when users are added/removed (not seamless).
- Harder to audit and maintain.
- Group-based `Match` is cleaner: single block applies to all group members.

**Decision:** Rejected. Group-based is simpler and scales better.

## Open Questions [RESOLVED]

None. The plan (`plans/m12-sftp.md`) addresses:
- Fingerprinting (server-side SHA256).
- Admin view pattern (list-only + impersonation).
- Key validation (RSA 2048+, ParseAuthorizedKey).
- Scope (no shell, SFTP-only, no forwarding).

All decisions are finalized and ready for implementation.

## Schema & Configuration

### Panel side: New table

```sql
CREATE TABLE ssh_keys (
  id BIGINT PRIMARY KEY AUTO_INCREMENT,
  user_id BIGINT NOT NULL UNIQUE KEY (user_id),
  name VARCHAR(255) NOT NULL,
  public_key TEXT NOT NULL,
  fingerprint VARCHAR(255) NOT NULL UNIQUE,
  created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
  deleted_at TIMESTAMP NULL,
  FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE
);
```

### System side: sshd config drop-in

File: `/etc/ssh/sshd_config.d/jabali-sftp.conf`

```
Match Group jabali-sftp
    ForceCommand internal-sftp
    AllowTcpForwarding no
    X11Forwarding no
    PermitTunnel no
    PasswordAuthentication no
    GatewayPorts no
    StreamLocalBindUnlink no
```

Installed and enabled by `install.sh` Step 2 (Wave A).

### System side: Group creation

`install.sh` creates the `jabali-sftp` group at install time. Reconciler adds users
to the group as they enable SFTP (implicit via user creation).

---

## Cross-References

- **`plans/m12-sftp.md`** — Full implementation blueprint with 7 steps and wave assignments.
- **ADR-0027** — M11 File manager via filebrowser (admin list-only pattern, per-user scoping).
- **ADR-0023** — M9 PHP-FPM pool manager (user owns homedir assumption).
- **ADR-0026** — M10 WordPress installs (docroot in user homedir).
- **ADR-0015** — Admin impersonation with JWT claim (used for key revocation).
- **`docs/BLUEPRINT.md` §6.12** — M12 scope and dependencies.

---

## Related Artifacts

*(TBD after implementation starts)*
- `install.sh` — Add `jabali-sftp` group creation (Step 2)
- `install/ssh/jabali-sftp.conf` — sshd config drop-in (Step 2)
- `panel-api/internal/db/migrations/` — `ssh_keys` table migration (Step 3)
- `panel-api/internal/models/ssh_key.go` — `SSHKey` model (Step 3)
- `panel-api/internal/repo/ssh_key_repo.go` — CRUD repo (Step 3)
- `panel-agent/internal/commands/ssh_*.go` — Agent commands for key management (Step 4)
- `panel-api/internal/api/ssh_keys.go` — REST API handlers (Step 5)
- `panel-api/internal/reconciler/reconciler.go` — Reconciliation logic for authorized_keys (Step 5)
- `panel-ui/src/shells/user/ssh-keys/UserSSHKeysPage.tsx` — User SSH keys UI (Step 6)
- `tests/e2e/sftp.spec.ts` — E2E tests for SFTP happy path (Step 7)
- `docs/runbooks/sftp.md` — Operational runbook (Step 7)
