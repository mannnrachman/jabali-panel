# Support

`/jabali-admin/support`. M29.

Produces an encrypted diagnostic bundle suitable for emailing to the upstream maintainers without leaking secrets or end-user data.

## What the bundle contains

Collected verbatim by `panel-agent/internal/diagnostic/diagnostic.go` (hard-coded list — no operator-tunable surface, so a malicious request cannot widen the scope):

| Entry | Source |
|---|---|
| `00-uname.txt` | `uname -a` |
| `01-os-release.txt` | `/etc/os-release` |
| `02-uptime.txt` | `uptime` |
| `03-free.txt` | `free -h` |
| `04-df.txt` | `df -h` |
| `05-git-head.txt` | panel repo `git rev-parse HEAD` |
| `06-git-status.txt` | panel repo `git status --porcelain` |
| `07-ss-tnlp.txt` | `ss -tnlp` (listening sockets + owning processes) |
| `08-iptables-input.txt` | `iptables -L INPUT -n` |
| `09-dpkg-list.txt` | installed package versions |
| `10-letsencrypt.log` | `tail -n 500 /var/log/letsencrypt/letsencrypt.log` |
| `svc/<unit>.is-active.txt` | per-unit `systemctl is-active` |
| `svc/<unit>.status.txt` | per-unit `systemctl status --no-pager` |
| `svc/<unit>.journal.txt` | per-unit `journalctl -n 200 --no-pager` |

Services included (`servicesToCollect`):

- `jabali-panel.service`, `jabali-agent.service`
- `jabali-stalwart.service`, `jabali-webmail.service`, `jabali-kratos.service`
- `pdns.service`, `pdns-recursor.service`
- `mariadb.service`, `redis-server.service`, `nginx.service`
- `certbot.service`, `certbot.timer`

## Mandatory redaction

Every collected file passes through the redactor (`panel-agent/internal/diagnostic/redact.go`) before it lands in the tar. The redactor strips:

- IPv4 / IPv6 addresses outside RFC1918 down to a `/24` (or `/64`) prefix.
- Email addresses to `<redacted-email>`.
- Bearer tokens, API tokens, passwords, database connection strings.

Redactor cannot be disabled from the UI. The `RedactionCount` field on the bundle reports how many substitutions were made.

## Encryption

The tarball is encrypted in the agent with the recipient's public key (default: the upstream maintainers' key baked into the panel image; override under Server Settings → Support → Recipient Public Key).

The plaintext bundle is never written to disk — the tarball is produced in memory and encrypted before any filesystem write.

## Delivery

The Support page opens a `mailto:` to the configured recipient address (default `webmaster@jabali-panel.com`) with the encrypted bundle attached. No HTTP upload to a third-party service, so the panel remains installable on air-gapped or restricted-egress hosts.

## What the bundle does *not* contain

To keep this list current with the actual collector, the following are **not** collected — request a feature add if you need them:

- `nginx -T` (full rendered config)
- `jabali repair --diagnose` output
- AIDE last-diff report
- DB schema dump or row counts
- Mail queue depth
- CrowdSec decisions list
- AppSec block log

The intent is to keep the bundle small enough to email and to limit the surface to host-level state needed for incident triage. Operator-specific deep dives (a full `nginx -T`, a `mysqldump --no-data`) are run on demand against a live host once the maintainers and operator are in contact.

## Why no auto-upload

Operator policy varies. Some organisations require that no diagnostic data leaves the host without explicit operator action; some require encryption against a specific key. Putting the operator in the loop on every bundle satisfies both requirements.

## CLI

```bash
jabali admin diag bundle                                # write to /var/lib/jabali/support/
jabali admin diag bundle --recipient-key /path/to/pubkey.asc
```

The CLI variant produces the encrypted file but does not attempt to email it; the operator handles delivery.
