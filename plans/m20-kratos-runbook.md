# M20 Kratos runbook

Operational reference for the Kratos identity integration shipped across M20
Waves A–E. This stub covers the Wave B identity-migration tool; Wave D + E
will flesh out the cutover and day-2 sections.

## Migrate existing users (Wave B, Step 4)

The panel ships a one-shot command, `jabali kratos-migrate`, that backfills
Kratos identities for every row in `users` whose `kratos_identity_id` is
`NULL`. Passwords survive the move via bcrypt passthrough — no user has to
re-enrol.

### Prerequisites

1. Kratos is running and reachable at `auth.kratos.public_url` +
   `auth.kratos.admin_url` (defaults: `127.0.0.1:4433` public,
   `127.0.0.1:4434` admin — same-host loopback). Verify:
   ```sh
   curl -sf http://127.0.0.1:4433/health/ready && echo OK
   curl -sf http://127.0.0.1:4434/admin/health/ready && echo OK
   ```
2. The 000047 migration has been applied. Verify:
   ```sh
   mariadb -e "DESCRIBE users" jabali_panel | grep kratos_identity_id
   ```
3. `auth.provider` may be left at `legacy` during the backfill — identities
   are created ahead of cutover. Step 9 flips the flag.

### Dry run (always do this first)

```sh
sudo -u jabali /opt/jabali-panel/jabali kratos-migrate --dry-run
```

The dry-run still exercises the bcrypt passthrough canary (creates a
disposable identity, logs in with a known plaintext via the API-mode flow,
deletes it). If that fails, fix Kratos config (`hashers.bcrypt.enabled`) and
re-run — the real batch refuses to start without a green canary.

The dry-run log lines to expect:
- `running bcrypt passthrough canary` → `canary passed`
- `scanned existing kratos identities` with a count
- `fetched unmigrated users` with a count
- Per-row `would create kratos identity` or `would link existing kratos identity`
- Final `kratos-migrate complete` summary

### Real run

```sh
sudo -u jabali /opt/jabali-panel/jabali kratos-migrate
```

Exit code:
- `0` → every user migrated or already linked.
- non-zero → at least one row failed; the log above the summary names the
  affected `user_id` + `email`. Re-running the command retries only the
  still-NULL rows (idempotent — already-linked rows are skipped by the
  email-to-identity map pre-scan).

### Flags

| Flag | Default | Purpose |
|---|---|---|
| `--dry-run` | off | Plan only. Canary still runs. |
| `--batch-size` | 50 | Rows per logged-progress tick. |
| `--skip-canary` | off | Dangerous — skip bcrypt-passthrough verification. Only use when the canary has already been manually reproduced against the target Kratos. |
| `--totp-only` | off | Reserved for M20 step 8. Currently errors out so password-only runs don't silently skip TOTP. |

### Common failures

- **`canary failed: create identity (kratos rejected the bcrypt hash format ...)`** — `hashers.bcrypt.enabled` is false in `/etc/jabali-panel/kratos.yml`, or `hashers.argon2` is listed first. Wave A's kratos.yml.tmpl pins bcrypt; check the rendered file.
- **`canary failed: login failed (kratos stored the hash but could not verify it ...)`** — Kratos accepted the hash shape but can't verify it at login time. Usually a `hashers.bcrypt.cost` mismatch or a wrong `credentials.password.identifier_similarity_check` blocking the canary's email+password combination. Log snippet from `journalctl -u jabali-kratos` will show the exact rejection.
- **`auth.kratos.public_url is empty`** — you're running the migrate tool with `auth.provider = "kratos"` but no Kratos URLs set. Either set both URLs in `/etc/jabali/config.toml` + `/etc/jabali/panel.env`, or run step 4 BEFORE flipping the flag.

### Safety — what this tool will NOT do

- **Never truncates the panel `users` table** — `kratos_identity_id` is permanent; the legacy column set stays in place for the 30-day rollback window documented in the M20 blueprint §1 step 9.
- **Never overwrites a non-NULL `kratos_identity_id`** — the `UPDATE … WHERE kratos_identity_id IS NULL` guard is by design. If you need to re-link a user to a different identity, do it manually via `kratos identities delete <id>` + column `UPDATE users SET kratos_identity_id = NULL`.
- **Never deletes a Kratos identity that wasn't just-created by this run** — cleanup paths only call `DeleteIdentity` on IDs the tool itself minted seconds earlier.

## Day-2 operations (stub — filled in by M20 Wave D/E)

- `kratos identities list|get|update|delete`
- `kratos sessions revoke <id>`
- `mysqldump jabali_kratos` / restore
- MFA reset (after Wave D step 8)
- Recovery-code generation (replaces M5a/M5b)
