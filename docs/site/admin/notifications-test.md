# Notifications — Test

`/jabali-admin/notifications/test`. Fire a synthetic event through the dispatcher to verify channel and routing configuration without waiting for a real event.

## Inputs

- **Event source** — pick from the registered event sources (see [Events](./notifications-events.md)).
- **Severity** — choose the severity level the synthetic event will carry.
- **Subject user** — optional; defaults to the actor admin. Used when a routing rule includes "subject user" in the recipient filter.
- **Payload** — auto-generated representative payload; editable as JSON if you want to exercise specific routing branches.

## Flow

On submit:

1. The panel writes a `test=true` row into the Redis Stream the dispatcher consumes.
2. The dispatcher reads the row, applies [Routing](./notifications-routing.md), and calls each matched sender.
3. Each sender returns a per-attempt result captured in the right-hand pane.

The right pane updates within seconds with per-channel deliverability outcome: HTTP status (for webhooks), SMTP response (for email), Web Push response, in-app insert count.

## Reading the results

- **Channel ok** — credentials and connectivity verified.
- **Channel fail, code visible** — credentials wrong or the destination returned a non-2xx; check the configured token, URL, or chat ID.
- **No channels invoked** — no routing rule matched the synthetic event with the chosen severity. Adjust the rule or the threshold.

## When to use this page

- After adding a new channel.
- After editing a routing rule and wanting to confirm a particular event-source path matches as intended.
- When investigating a reported notification gap: fire a test event of the same source and severity, observe whether the expected channel triggers.

## Audit

Test events are tagged in the audit log as `notification.test`. The dispatcher's structured logs include `test=true` so they are easy to filter out of production reporting.
