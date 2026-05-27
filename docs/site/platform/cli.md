# CLI Reference

The `jabali` binary is one cobra root with subcommands. Run `jabali --help` or `jabali <cmd> --help` for live flags.

This page lists every shipped subcommand grouped by area, with the one-line `Short` description from the binary and the exact verbs/flags. (Generated from `panel-api/cmd/server/*.go`.)

## Global

```bash
jabali --help
jabali --version
jabali serve            # start the panel-api process (systemd does this for you)
jabali update [--auto]  # pull latest, rebuild, migrate, restart
jabali repair [--diagnose | --auto | --all --yes]
```

## Admin

Operator-only subtree.

```bash
jabali admin one-time-login                     # mint an admin one-time login URL
jabali admin diag bundle                        # encrypted support tarball
jabali admin db root-password rotate            # rotate MariaDB root password (M46)
jabali admin db config apply                    # apply curated config tune (M46)
jabali admin db maintenance run                 # OPTIMIZE/ANALYZE/CHECK/REPAIR
jabali admin db processlist                     # live processlist (admin shadow account)
jabali admin db pma admin ensure                # ensure phpMyAdmin admin shadow account exists
jabali admin sso rotate-key                     # rotate SSO file signing key
jabali admin sso reap                           # sweep orphan jabali-sso-*.php files
jabali admin slice cutover                      # M9 per-user PHP pool slice cutover (one-time)
jabali admin panel primary                      # set / show panel primary user
jabali admin malware purge                      # purge quarantine
```

## Users

```bash
jabali user list                                # all panel users (direct DB — M20-safe)
jabali user create --username --email --package --primary-domain [--password]
jabali user password <email|username|user-id>   # reset password (generates if --password omitted)
jabali user 2fa-reset <email|username|user-id>  # strip TOTP + recovery codes
jabali user delete <email|username|user-id>     # destructive
```

## Domains

```bash
jabali domain list                              # direct DB, M20-safe
jabali domain create --user <id> --name <fqdn> [--php <ver>]
jabali domain enable  <name|id>
jabali domain disable <name|id>
jabali domain delete  <name|id>                 # reconciler tears down nginx
jabali domain extras …                          # per-domain redirects + aliases
jabali domain orphan-prune [--dry-run | --apply]
```

## SSL

```bash
jabali ssl list [--user <id>]
jabali ssl enable  <domain>      # toggle on; reconciler issues cert ≤60s
jabali ssl disable <domain>      # toggle off; reconciler revokes
jabali ssl renew   <domain>      # synchronous renew via agent
```

## DNS

```bash
jabali pdns dnssec enable  <domain>     # KSK+ZSK + rectify + persist
jabali pdns dnssec disable <domain>
jabali pdns dnssec status  <domain>     # cached DNSSEC keys
jabali pdns dnssec ds      <domain>     # print DS to publish at parent
jabali pdns backfill                    # converge /etc/powerdns/recursor.forwards with DB
```

## Mail

```bash
jabali mailbox list --domain <fqdn>
jabali mailbox create  user@domain --quota-mib N [--password]
jabali mailbox passwd  user@domain [--password]
jabali mailbox set-quota user@domain <mib>
jabali mailbox delete  user@domain
jabali mailbox shares  …                # shared folder management
jabali domain email enable  <domain>    # turn on mail for the domain
jabali domain email disable <domain>
jabali domain email dkim-rotate <domain>
```

## Databases

```bash
jabali db list [--user <id>]
jabali db create --user <id> --name <suffix>
jabali db delete <id>
jabali db-user create --db <id> --username <name>
```

## Cron

```bash
jabali cron list   --user <id>
jabali cron add    --user <id> --schedule "0 3 * * *" --command "..."
jabali cron update <job-id>     --schedule "*/15 * * * *"
jabali cron delete <job-id>
jabali cron run-now <job-id>    # fire now, ignore schedule
```

## SSH Keys

```bash
jabali sshkey list   --user <id>
jabali sshkey add    --user <id> --label "laptop" --pubkey "ssh-ed25519 …"
jabali sshkey delete <key-id>
```

## PHP

```bash
jabali php list                          # installed versions + pool counts
jabali php install <version>             # apt-install PHP <version>
jabali php enable-ext  <version> <ext>
jabali php disable-ext <version> <ext>
```

## Packages

```bash
jabali package list
jabali package create --name <slug> --disk-quota-mib N --max-mailboxes N …
jabali package update <id> …
jabali package delete <id>
```

## Limits

```bash
jabali limits apply --user <id>   # re-apply per-user resource limits (idempotent)
jabali limits clear --user <id>   # remove drop-ins (user deletion path)
```

## Backups

```bash
jabali destination list
jabali destination get <id-or-name>
jabali destination create --type <local|sftp|s3|b2|azure|gcs|rest> --name <n> …
jabali destination update <id-or-name>
jabali destination delete <id-or-name>
jabali destination test   <id-or-name>      # auto-inits restic repo if missing

jabali backup schedule list
jabali backup schedule create --kind <account_full|system_backup> --user <id> --destination <id> --cron "..." --keep-daily N --keep-weekly N --keep-monthly N
jabali backup schedule delete <id>
jabali backup scheduler tick                 # for cron / one-shot manual run

jabali backup retention apply
jabali backup copy retired                   # migrate retired-tenant backups to long-term store

jabali account restore --user <new-id> --snapshot <id> --destination <id>
jabali system restore  --snapshot <id> --destination <id>
```

## Apps

```bash
jabali app list [--user <id>]
jabali app install --user <id> --domain <fqdn> --app <wordpress|moodle|drupal|…> [--admin-user X --admin-email X --admin-password X]
jabali app delete  <install-id>
```

## CLI (low-level direct create)

For automation / first-boot scripts:

```bash
jabali cli create user …               # bypass HTTP auth, talk straight to DB (M20-safe)
jabali cli create domain …
jabali cli ops app install …           # equivalent of /jabali-admin/applications install
jabali cli ops …
```

## Migration

```bash
jabali migrate list                    # incoming archives
jabali migrate run <id>                # restore phase
jabali migrate pull <id>               # fetch archive from a remote source
jabali migrate restore <id>            # legacy 1-shot path
jabali migrate reap                    # clean stale jobs

jabali nspawn …                        # systemd-nspawn helper (used by some migrations)
```

## Firewall

```bash
jabali ufw migrate-ip-bans             # lift `ufw deny from <ip>` → CrowdSec decisions
```

## Per-user egress

```bash
jabali per-user egress list
jabali per-user egress allow --user <id> --port <port> --proto <tcp|udp> --dest <cidr|fqdn>
jabali per-user egress revoke <rule-id>
```

## System / OS

```bash
jabali system swap create --mib N      # create + enable swap file
jabali system swap remove
jabali system listener test            # validate /run/jabali-agent.sock is reachable
```

## DB migrations

```bash
jabali migrate up                      # run pending DB migrations
jabali migrate down N                  # roll back N (dev only)
jabali migrate status                  # what's pending
```

## Kratos

```bash
jabali kratos ready                    # health check
jabali kratos rebuild                  # rebuild kratos config from panel DB
```

## Audit

```bash
jabali audit list [--since 24h] [--action <pattern>] [--user <id>] [--limit N]
```

## Exit codes

| Code | Meaning |
|---|---|
| 0 | Success |
| 1 | Generic failure (see stderr) |
| 2 | Misuse (bad flag / missing arg) |
| 3 | Agent unreachable (`/run/jabali-agent.sock` missing or permission denied) |
| 4 | DB unreachable |
| 5 | Validation error (e.g. invalid cron schedule) |
| 6 | Authorization error (CLI requires root) |

## Common flags

- `--user <id>` — operate on / filter by a user (UUID, username, or email).
- `--json` — machine-readable output.
- `--yes` — skip confirmation prompts (destructive commands).
- `--dry-run` — show what would change, don't apply.

---

The CLI is a thin layer over the same business logic the HTTP API uses. Anything you can do in the panel UI you can do here. The CLI is mandatory for first-boot bootstrap (user create, admin one-time-login) and for unattended automation.
