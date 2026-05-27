# Platform — Health Monitor

The same surface as [Server Status](../server-status.md) but exposed at machine-readable endpoints for external monitoring.

## Endpoints

| Endpoint | Auth | Returns |
|---|---|---|
| `GET /api/v1/health` | none | `{ "status": "ok" \| "degraded" \| "down", "version": "...", "uptime_s": N }` — used as the basic liveness probe. |
| `GET /api/v1/health/detailed` | admin Bearer / cookie | Per-service status (same as `/jabali-admin/server-status`) as JSON. |
| `GET /metrics` | admin Bearer | Prometheus-format metrics (request counts, latencies, reconciler tick durations, queue depths). |

## Status semantics

| status | Meaning |
|---|---|
| `ok` | All watched services healthy. |
| `degraded` | A non-critical service is failed/degraded (e.g. ClamAV freshclam stale > 7 d). Panel still serves. |
| `down` | A critical service is failed (panel-api itself returning health, so MariaDB / nginx / Stalwart down counts here). |

## Watched services

- `jabali-panel.service`
- `jabali-agent.service`
- `nginx.service`
- `mariadb.service`
- `postgresql.service` (only if at least one user has a Postgres DB; otherwise ignored)
- `pdns.service`
- `pdns-recursor.service`
- `stalwart-mail.service`
- `kratos.service`
- `bulwark.service`
- `redis.service`
- `crowdsec.service`

The watched set is computed at startup; services that aren't installed don't count against `degraded`.

## Use with an external monitor

UptimeRobot / Pingdom / a self-hosted Uptime-Kuma:

- Point at `https://<panel-hostname>/api/v1/health` — anonymous, fast.
- Set expected response: HTTP 200 + body contains `"status":"ok"`.

For Prometheus scraping, point at `/metrics` with a bearer token (mint under `/jabali-admin/automation`).

## Notifications integration

The `service_down` event source (M14) reads the same internal state — so you don't *need* an external monitor for in-house alerting. The external monitor is useful for "the panel-api itself is down, who tells me?" cases.
