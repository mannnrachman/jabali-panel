# Server Status

`/jabali-admin/server-status`. M31. Live status of every watched service plus host vitals.

## What is rendered

Errgroup-aggregated polling every 5 seconds. Each card returns its own status; one slow card does not block the others.

### Per-service cards

For each of: nginx, php-fpm (per version), mariadb, postgresql, pdns-server, pdns-recursor, stalwart-mail, kratos, bulwark, redis, crowdsec, jabali-panel, jabali-agent:

- Active state (active, inactive, failed)
- Time since the unit entered the current state
- Restart count in the last hour
- Last journal line (truncated to 200 chars)
- **Start / Stop / Restart** buttons (only visible when the operator off-toggle in Server Settings → General → "Allow service controls from UI" is on)

### Host vitals

- CPU usage and 1-minute load average
- Memory: used / free / cache, swap usage
- Disk: per-mount used / free, including `/var/lib/mysql`, `/var/lib/stalwart`, `/home`
- Network: in / out per interface

### Queues card

Placeholder pending M31.1. Will surface mail queue depth, backup queue depth, reconciler tick lag, and notification dispatch lag.

### Recent panel requests

Top 10 panel-api requests in the last 60 seconds by latency. Drill-in shows the full route, status code, and request-id (correlate with `journalctl -u jabali-panel`).

## Service controls

Disabled by default. To enable: Server Settings → General → toggle on. Once enabled, the per-card buttons fire `systemctl start|stop|restart <unit>` via the agent. Every action is audited.

The rationale for the off-default is to prevent a casual click from taking the panel itself down (Stop on `jabali-panel.service` self-destructs the UI). Operators who want the convenience may opt in.

## Polling, not WebSocket

The page polls at 5 seconds via a single endpoint that returns the aggregated state JSON. WebSocket was considered and rejected: polling works behind every reverse-proxy and corporate-firewall combination tested, and the page does not require millisecond-scale updates.

## Live-verified

The page surface was live-verified on 192.168.100.150 as the system's primary vitals view.

## Related

- [Services](./services.md) — the per-service control surface, expanded.
- [Notifications](./notifications-events.md) — the `service_down` event source feeds notifications when the operator is not watching the page.
- [Updates](./server-updates.md) — running `jabali update` is the most common reason a service briefly disappears from this page.
