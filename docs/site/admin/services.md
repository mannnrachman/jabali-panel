# Services

The per-service control surface, an expanded view of the cards shown on [Server Status](./server-status.md).

## What you can do here

For each watched systemd unit:

- **Start / Stop / Restart** — via the agent, audited. Disabled unless the operator opted in under Server Settings → General → "Allow service controls from UI".
- **Enable / Disable** — change the unit's boot-time autostart state.
- **Reload** — for units that support it (nginx, php-fpm, stalwart-mail), reload without dropping in-flight connections.
- **View recent journal** — last 500 lines, paginated.
- **View dependency tree** — `systemctl list-dependencies <unit>` rendered as a collapsible tree.

## Watched units

| Unit | Role |
|---|---|
| `jabali-panel.service` | The HTTP panel. |
| `jabali-agent.service` | The privileged agent over UDS. |
| `nginx.service` | Reverse-proxy and per-vhost server. |
| `php8.1-fpm.service` … `php8.5-fpm.service` | Per-version FPM masters. |
| `mariadb.service` | MariaDB (panel DB + tenant DBs). |
| `postgresql.service` | PostgreSQL (tenant DBs only; absent if no tenants use it). |
| `pdns.service` | Authoritative PowerDNS. |
| `pdns-recursor.service` | Loopback recursor. |
| `stalwart-mail.service` | SMTP / IMAP / JMAP / mailbox store. |
| `bulwark.service` | SPA + autoconfig bridge. |
| `kratos.service` | Identity. |
| `redis.service` | Notifications dispatcher stream and panel cache. |
| `crowdsec.service` | IP-trust source. |
| `tetragon.service` | eBPF tripwires for malware detection. |
| `aide.timer` | Daily host-integrity scan. |
| `certbot.timer` | Let's Encrypt renewal. |
| `jabali-freshclam.timer` | ClamAV signature refresh. |

The watched set is computed at panel startup. Units that are not installed are simply absent from the page (no "missing" warnings).

## What to avoid

- **Do not stop `jabali-panel.service` from the UI** — you will lose the UI immediately. SSH back to the host and `systemctl start jabali-panel` to recover.
- **Do not stop `jabali-agent.service` while a reconciler tick is in progress** — in-flight operations will fail and require a manual retry.
- **Do not disable `kratos.service`** — login becomes impossible.

## CLI

Service controls are also available via the standard `systemctl` interface; the panel does not interpose. Use `systemctl status <unit>` from a shell when you want the unabridged output.
