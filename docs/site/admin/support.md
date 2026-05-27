# Support

`/jabali-admin/support`. M29. Produce an encrypted diagnostic bundle for the upstream maintainers, then open a `mailto:` ticket.

## Bundle contents

Hard-coded in `panel-agent/internal/diagnostic/diagnostic.go` so a malicious request cannot widen the scope.

| Entry | Source |
|---|---|
| `00-uname.txt` | `uname -a` |
| `01-os-release.txt` | `/etc/os-release` |
| `02-uptime.txt` | `uptime` |
| `03-free.txt` | `free -h` |
| `04-df.txt` | `df -h` |
| `05-git-head.txt`, `06-git-status.txt` | panel repo HEAD + working-tree status |
| `07-ss-tnlp.txt` | listening sockets with owning processes |
| `08-iptables-input.txt` | INPUT chain dump |
| `09-dpkg-list.txt` | installed package versions |
| `10-letsencrypt.log` | last 500 lines of `/var/log/letsencrypt/letsencrypt.log` |

Plus per-service triple (`is-active`, `status`, `journalctl -n 200`) for:

- `jabali-panel`, `jabali-agent`
- `jabali-stalwart`, `jabali-webmail`, `jabali-kratos`
- `pdns`, `pdns-recursor`
- `mariadb`, `redis-server`, `nginx`
- `certbot.service`, `certbot.timer`

## Mandatory redaction

Every line passes through a redactor before tarring (`panel-agent/internal/diagnostic/redact.go`):

- Non-RFC1918 IPv4 / IPv6 truncated to `/24` / `/64`.
- Email addresses replaced with `<redacted-email>`.
- Bearer tokens, API tokens, passwords, DB connection strings stripped.

`Bundle.RedactionCount` reports how many substitutions occurred. The redactor cannot be disabled from the UI.

## Encryption

The tar is encrypted in the agent with the recipient's public key. Default recipient: the maintainers' public key baked into the panel image; override via [Server Settings](./server-settings.md) → Support → Recipient Public Key.

Plaintext bundle never touches disk; tar lives in memory until encrypted.

## Delivery

The page opens `mailto:<recipient>` (default `webmaster@jabali-panel.com`) with the encrypted bundle attached. The admin sends the email from their own mail client. No HTTP upload, no third-party SaaS — works on air-gapped hosts.

## What is *not* in the bundle

The collector is intentionally narrow. The following are **not** included; request a feature add if your incident triage needs them:

- `nginx -T` (full rendered config)
- `jabali repair --diagnose` output
- AIDE last-diff report
- DB schema dump (`mysqldump --no-data`) or row counts
- Mail queue depth
- CrowdSec decisions list
- AppSec block log

The bundle is sized to be emailable; deep dives happen against a live host once the operator and maintainers are in contact.

## Why no auto-upload

Operator policy varies; some require encryption against a specific key, some require no outbound from the host at all. The operator-in-the-loop flow satisfies both. The bundle is encrypted before any HTTP egress could happen.

## CLI

```bash
jabali admin diag bundle                                # write to /var/lib/jabali/support/
jabali admin diag bundle --recipient-key /path/to/pubkey.asc
```
