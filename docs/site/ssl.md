# SSL / TLS

Let's Encrypt-only. HTTP-01 challenge over the domain's existing port-80 nginx vhost; no DNS-01.

## Per-domain certificates

Toggle SSL on a domain (admin: Domains → Edit → SSL; user: Domains → Edit → SSL). The reconciler:

1. Verifies the domain resolves to one of the server's IPs (or to the configured listen IP for that domain).
2. Calls certbot via the agent with `--webroot` against the live nginx vhost.
3. Stores the cert under `/etc/letsencrypt/live/<domain>/`.
4. Reloads nginx.

Issuance latency: ≤60 s typical. Failures retry every 3 hours (reconciler interval).

## Renewal

certbot's own systemd timer renews. The agent's deploy-hook reloads nginx automatically. No human action.

## Panel-hostname certificate

The panel itself runs on a Let's Encrypt cert for the **Panel Hostname** (Server Settings → General). Cert lives in the singleton `panel_certificate` table; the agent's `ssl.panel.issue` action handles issuance via the same HTTP-01 path (with a `/.well-known/acme-challenge/` location on the panel-hostname nginx vhost). Deploy hook reloads **nginx → panel-api → Bulwark** in that order.

If issuance fails, the panel falls back to a self-signed cert so the UI stays reachable; the SSL card in Server Settings shows the failure reason. Hit "Retry" or wait for the next reconciler tick.

## Cross-user view (admin)

`/jabali-admin/ssl` lists every cert known to the panel, expiry, last issuance result. Per-row "Retry" forces a fresh attempt.

## Common failure modes

| Symptom | Cause | Fix |
|---|---|---|
| `Failed authorization procedure` | Domain does not resolve to this server | Update DNS at the registrar; wait propagation; retry. |
| `urn:ietf:params:acme:error:rateLimited` | Hit LE rate limit | Wait the window (typically 1 hour). |
| Cert issued but browser shows old cert | nginx not reloaded | `systemctl reload nginx` or trigger reconciler. |
| Panel cert never issues | Bulwark serving on `:80` | Reconciler should fix; check `journalctl -u jabali-agent -f`. |

## CLI

```bash
jabali ssl list [--user <id>]
jabali ssl enable <domain>          # toggle on; reconciler does the rest
jabali ssl disable <domain>
jabali ssl renew <domain>           # synchronous renew via agent
```
