# Platform — Agent

`jabali-agent.service`. Root-privileged process; the only thing that performs privileged host operations. Callers (the panel API, the CLI, the reconciler) reach it over `/run/jabali-agent.sock`.

## Why a separate process

- **One privilege boundary** — the panel runs as the unprivileged `jabali` user; only the agent has root.
- **One auditable surface** — every privileged op goes through one of N handlers under `panel-agent/internal/commands/`. The audit log records the agent's call, not the panel's intent.
- **Restart-safe** — restarting the panel doesn't kill in-flight host ops; restarting the agent doesn't lose panel state.

## Wire protocol

Length-prefixed JSON over UDS. Each request:

```json
{ "action": "domain.create", "params": { … }, "request_id": "01J..." }
```

Each response:

```json
{ "request_id": "01J...", "ok": true,  "result": { … } }
{ "request_id": "01J...", "ok": false, "error": "human-readable", "code": "STRUCTURED_CODE" }
```

## Handler catalogue (representative; see `panel-agent/internal/commands/`)

**Domains / nginx**
- `domain.create`, `domain.update`, `domain.delete`
- `nginx.reload`, `nginx.cache.purge`, `nginx.ratelimits.apply`

**SSL**
- `ssl.issue`, `ssl.renew`, `ssl.revoke`
- `ssl.panel.issue`, `ssl.panel.renew`

**DNS**
- `dns.zone.upsert`, `dns.zone.delete`
- `dns.dnssec.enable`, `dns.dnssec.disable`

**Mail (Stalwart)**
- `mail.mailbox.create`, `mail.mailbox.passwd`, `mail.mailbox.set_quota`, `mail.mailbox.delete`
- `mail.forwarder.upsert`, `mail.forwarder.delete`
- `mail.autoresponder.upsert`, `mail.autoresponder.delete`
- `mail.catchall.set`
- `mail.disclaimer.set`
- `mail.shared_folder.*`
- `mail.mtasts.*`

**DB**
- `db.create`, `db.delete`
- `db.user.create`, `db.user.delete`, `db.user.passwd`
- `db.root.password.rotate`
- `db.config.apply`
- `db.maintenance.run` (OPTIMIZE / ANALYZE / CHECK / REPAIR)
- `db.processlist`, `db.kill`
- `db.pma.admin.ensure`

**System / OS**
- `user.create`, `user.delete`
- `user.limits.apply`, `user.limits.clear`
- `user.egress.apply`, `user.egress.clear`
- `sshkey.write`
- `quota.set`

**PHP**
- `php.pool.write`, `php.pool.delete`
- `php.ext.enable`, `php.ext.disable`
- `php.reload`

**Backups**
- `backup.destination.test`
- `backup.run` (kind=`account_full` / `system_backup`)
- `backup.restore`

**Apps**
- `app.install`, `app.delete`, `app.clone`, `app.update`
- `app.sso.write` (writes the self-deleting magic SSO file)

**Security**
- `crowdsec.allowlist.*`
- `appsec.policy.apply`
- `aide.scan`
- `malware.scan.run`
- `quarantine.move`

**Misc**
- `nginx.cache.purge`, `nspawn.*`, `systemd.restart`, `systemd.start`, `systemd.stop`

## What the agent never does

- Receive HTTP. The agent does not have an HTTP listener. The panel API is the HTTP entry point.
- Talk to Kratos directly. Identity lives in the panel.
- Make outbound calls to third parties without the panel telling it to. (Even certbot is invoked with `--standalone-only=false` against the existing nginx vhost; no rogue outbound.)

## Hardening

- `AppArmor` profile (`/etc/apparmor.d/jabali-agent`) restricts which files it can touch.
- `SupplementaryGroups=jabali-sockets` (M25.1) so the socket is reachable from the panel without giving the panel root.
- Logs everything with structured fields (request_id, action, subject_user, target_user, result).

## Adding a new handler

1. New file `panel-agent/internal/commands/foo_bar.go` with the action name and param struct.
2. Wire-contract golden test (mirror `security_crowdsec_geoblock_golden_test.go`).
3. Call site on the panel side (an API handler or a reconciler).
4. ADR if the decision is load-bearing.
