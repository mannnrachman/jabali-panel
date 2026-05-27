# Migrating From Another Panel

`/jabali-admin/migrations`. M15 (in part) + ongoing pipeline work.

## Supported sources

| Source | Format | Status |
|---|---|---|
| **cPanel** | `cpmove-<user>.tar.gz` | ✅ — preserves MySQL users + bcrypt password hashes (so migrated apps keep working); see "preserve cpanel MySQL users + password hashes" commit. |
| **DirectAdmin** | DA backup tarball | ✅ — see `docs/user/directadmin-migration.astro` (legacy) for source-side prep notes. |
| **Hestia** | Hestia backup | 🟡 — partial. Files + DBs + DNS; mail subset (Stalwart vs. Exim model mismatch). |
| **WHM** | WHM-level dump (multiple `cpmove`s in one) | 🟡 — same caveats as cPanel per-user. |

## Workflow

1. Upload archive to `/jabali-admin/migrations` (or `scp` to `/var/lib/jabali/migrations/incoming/`).
2. The pipeline runs four phases:
   - **Analyze** — inspect the archive, list users / domains / DBs / mailboxes / DNS zones / cron jobs.
   - **Fix-perms** — apply chown / chmod normalisations expected by Jabali's per-user pool layout.
   - **Validate** — DB password hashes parseable, DNS zone files valid, mail accounts consistent.
   - **Restore** — create the panel user, ingest each domain / DB / mailbox / cron, hand off to the reconciler.
3. Watch progress at `/jabali-admin/migrations/<id>`.

## Per-source notes

### cPanel

- MySQL passwords are bcrypt in cPanel ≥ 11.96; Jabali stores the bcrypt hash directly in MariaDB so user apps keep authenticating without password reset.
- Email accounts: cPanel uses Dovecot+Exim, Jabali uses Stalwart. Passwords reset to a generated value (printed in the migration report) — operator must communicate the new passwords to mailbox owners, or set "force first-login password reset" so users self-serve.
- DKIM keys: imported.
- DNSSEC: not migrated automatically (key formats differ); re-enable per-domain in Jabali.

### DirectAdmin

- See `docs/user/directadmin-migration.astro` for the source-side prep (run `da backup-all` etc.) before upload.

### Hestia

- Bind zones translated to PowerDNS schema rows.
- Exim → Stalwart routing rules: forwards + autoresponders ported; complex Exim acl rules need manual re-implementation.

### WHM

- Splits into per-`cpmove` jobs internally; each runs through the cPanel pipeline.

## Limitations

- **No live migration**. Each pipeline is "stop-the-world" for the destination user.
- **No backup-restore from Plesk** (Plesk's backup format isn't supported yet).
- **No CSF/LFS rule translation**. CrowdSec is the IP-trust source on Jabali; carry over allowlists manually.
