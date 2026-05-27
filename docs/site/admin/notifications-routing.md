# Notifications — Routing

`/jabali-admin/notifications/routing`. Per-event-source mapping to per-channel destinations, with severity thresholds and recipient filters.

## Rule shape

A routing rule consists of:

- **Event pattern** — exact match (`cert_renew`) or wildcard (`backup_*`).
- **Severity threshold** — emit only when the event's severity is at or above this level.
- **Channel** — one of the enabled [Channels](./notifications-channels.md).
- **Recipient filter** — admin only, specific admin user list, or "all admins".
- **Active window** — optional cron expression; suppresses outside the window (useful for skipping non-critical notifications during off-hours).

A single event may match multiple rules; each rule produces an independent dispatch attempt.

## Default rules

The installer creates a starter set:

| Event pattern | Channel | Recipients | Threshold |
|---|---|---|---|
| `cert_renew` (fail only) | In-app, Email | All admins | `warn` |
| `service_down` | In-app, Email | All admins | `warn` |
| `crowdsec_spike` | In-app | All admins | `notice` |
| `disk_full` | In-app, Email | Subject user + all admins | `notice` |
| `aide_diff` | In-app, Email | All admins | `warn` |
| `backup_failed` | In-app, Email | Subject user + all admins | `warn` |
| `malware_file_hit` | In-app, Email | All admins | `error` |

Override or extend freely.

## Adding a rule

Click **Add rule**, pick an event pattern, severity threshold, target channel, and recipient filter. Save persists to `notification_routing_rules` and takes effect immediately (no reconciler delay).

## Suppression

Within each user's profile, a tenant may opt out of any rule that targets them. Server-level rules cannot opt them out of `service_down` or `crowdsec_spike` (those are admin-only by convention).

## Per-channel delivery semantics

- **In-app**: retained 30 days, marked-read state persists per user.
- **Email**: best-effort; deferred messages are surfaced under [Email Queue](./email-queue.md).
- **Slack / Telegram / ntfy**: synchronous webhook; failures are logged but not retried (the next event will land if the destination recovers).
- **Web Push**: per-subscription; failed `gone` responses prune the subscription automatically.

## CLI

Routing is currently UI-only. The underlying table is `notification_routing_rules` and is included in `account_full` backups so a restore preserves operator intent.
