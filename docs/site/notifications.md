# Notifications

M14. Redis Streams dispatcher → 6 channels → in-app + admin event sources.

## Channels

Admin configures channels at `/jabali-admin/notifications/channels`:

| Channel | Config |
|---|---|
| **In-app** | Always-on. Bell dropdown top-right. |
| **Email** | SMTP submission via the panel's own Stalwart. |
| **Slack** | Webhook URL. |
| **Telegram** | Bot token + chat ID. |
| **ntfy.sh** | Topic URL (works against `ntfy.sh` or a self-hosted ntfy server). |
| **Web Push** | VAPID keys auto-generated on first install; users opt-in per browser via the bell. |

## Event sources

Built-in (M14 Step 4 and later):

- `cert_renew` — Let's Encrypt issuance / renewal success or failure.
- `disk_full` — quota high-water-mark hit per user.
- `service_down` — any of the watched services failed to start, restarted unexpectedly, or is in `failed` state.
- `crowdsec_spike` — sudden spike in decisions or alerts.
- `domain_expiry` — re-interpreted as cert expiry (no WHOIS in scope).
- `aide_diff` — host-integrity drift detected.
- `cron_failed` — a systemd-user cron timer's service unit returned non-zero.
- `backup_succeeded` / `backup_failed`.
- `mail_quarantined` — Stalwart / async YARA quarantined a message.
- `malware_file_hit` — M33 detector hit.
- `db_root_rotated` — admin rotated DB root password.

Stub sources defined but not yet wired: `domain-registrar`, `backup-future-warnings`.

## Routing

`/jabali-admin/notifications/routing` — per-event-source → per-channel mapping with a severity threshold. Examples:

- `cert_renew` failures → Email + In-app (admins).
- `cert_renew` success → In-app only.
- `crowdsec_spike` → Slack #ops + ntfy.

## Test

`/jabali-admin/notifications/test` — fire a test event of any kind to verify routing.

## Architecture

- Producers emit a row into Redis Streams `jabali:notifications`.
- The dispatcher (in-process, single consumer per panel instance) reads the stream, looks up routing rules, calls each enabled sender.
- Senders are pure adapters; adding a new channel is one Go file under `panel-api/internal/notifications/senders/`.
- ADRs 0056-0059 cover the data model, sender interface, Web Push, and the bell dropdown.

## End-user opt-in

Users can opt **in** for `cron_failed`, `backup_succeeded`, `backup_failed`, `mail_quarantined` notifications to their own email — `/jabali-panel/profile` → Notifications.

Per-event subscription scope is bounded by ownership: a user cannot subscribe to `crowdsec_spike` for the whole server, only to events affecting their own account.
