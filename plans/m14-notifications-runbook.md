# M14 Notifications — Operator Runbook

Covers post-ship operations: VAPID rotation, per-channel troubleshooting, adding event sources, Web Push browser matrix, and rate-limit tuning.

---

## Architecture recap

```
publisher (API / agent / event source)
  → Redis Stream  jabali:notifications:queue
  → Dispatcher goroutine  (panel-api)
  → fanout (capped at MaxConcurrentSenders=4)
  → ChannelSender (slack | discord | ntfy | webhook | webpush | email)
  → history row + webhook_endpoints row
```

- DLQ: `jabali:notifications:dlq` — parse errors + `max_retries_exceeded`.
- Circuit breaker: 3 consecutive failures ⇒ `channels.enabled = 0` + auto_disabled alarm row.
- Per-admin broadcast rate limit: 5/min (window rolls).

---

## Rotating VAPID keys

**Impact:** every browser subscription is invalidated — users must re-enable push from the bell.

Steps:

1. `sudo systemctl stop jabali-panel`
2. Clear the server_settings columns:
   ```sql
   UPDATE server_settings
      SET vapid_public_key = NULL,
          vapid_private_key = NULL,
          vapid_subject = NULL
    WHERE id = 1;
   ```
3. Truncate existing subscriptions (they'll fail 410 on next send otherwise, cluttering logs):
   ```sql
   DELETE FROM webpush_subscriptions;
   ```
4. `sudo systemctl start jabali-panel`
5. First-boot seed (`EnsureVAPID`) writes a fresh keypair.
6. Announce to admins: re-visit the bell → "Enable browser push".

---

## Channel troubleshooting

### Slack / Discord
- 404 → webhook URL revoked. Mark channel permanent-failed via `/admin/notifications/channels/:id` edit; rotate URL from the Slack/Discord integration page.
- 429 → transient; dispatcher leaves the entry in the PEL for reclaim. Reduce broadcast frequency.

### ntfy
- 401/403 → bearer token wrong. Check `config.bearer` field; regenerate on the ntfy admin.
- 404 → topic URL mistyped. The full topic URL (e.g. `https://ntfy.sh/your-topic`) goes in `config.url`, not just the topic slug.

### Generic webhook
- `X-Jabali-Signature` mismatch on receiver side → HMAC secret drift. Confirm receiver's stored secret matches `config.hmac_secret` (stored server-side only, never echoed in API responses).
- Reminder: HMAC secret must be ≥16 chars (API validation).

### Web Push
- 410 Gone → browser retired the subscription; dispatcher deletes the row automatically. No operator action.
- Silent failure on Brave → Brave blocks FCM by default; see browser matrix below.

### Email (Stalwart submission)
- 5xx from SMTP → Stalwart down or misconfigured. `sudo systemctl status jabali-stalwart`; check `/var/log/jabali-stalwart/` for submission errors.
- CRLF in to/from fields → rejected as `ErrPermanent` by design (header-injection guard).

---

## Adding a new event source

1. Create `panel-api/internal/eventsources/<source>.go` with a `run<Source>(ctx, d)` function.
2. Wire into `Start()` at `sources.go` — spawn `go run<Source>(ctx, d)`.
3. Use `shouldFire(ctx, d, eventKind, tag, cooldown)` for dedupe.
4. Add event_kind to the allowlists in:
   - `panel-agent/internal/commands/notifications_send.go` (`allowedEventKinds`)
   - `panel-api/internal/api/notifications_internal.go` (`enqueueAllowedEventKinds`)
5. Unit test the pass function with a capturing publisher + fake clock (see `cert_renew_test.go`).

Existing sources as reference:
- `cert_renew.go` — hourly SSL cert scan
- `disk_full.go` — 10-minute `syscall.Statfs`
- `service_down.go` — 1-minute `systemctl is-active`
- `crowdsec_spike.go` — 5-minute `cscli decisions list`
- `domain_expiry.go` — stub (registrar WHOIS, M15)
- `backup_fail.go` — stub (M15 backups)

---

## Web Push browser matrix

Tested combinations (manual smoke — Playwright cannot script the native permission prompt):

| Browser | Platform | Endpoint host | Notes |
|---|---|---|---|
| Chrome 120+ | macOS / Linux / Windows | `fcm.googleapis.com` | Green path. 410 on revocation. |
| Edge 120+ | Windows | `wns*.notify.windows.com` | Uses FCM under the hood. |
| Firefox 120+ | macOS / Linux / Windows | `updates.push.services.mozilla.com` | Green path. |
| Safari 16.4+ | macOS 13+ | `*.push.apple.com` | Requires `userVisibleOnly: true` (enforced by the hook). |
| Safari 16.4+ | iOS (installed as PWA) | `*.push.apple.com` | Site must be added to Home Screen first. |
| Brave | All | FCM blocked by default | Subscribe may return a synthesized endpoint or silently fail. User must disable Shields for the panel. |

Endpoint host is logged on every send — `grep endpoint_host_prefix` in jabali-panel logs for vendor-specific failures.

---

## Rate limits

| Surface | Limit | Scope |
|---|---|---|
| `/admin/notifications/broadcast` | 5/min | per-admin (UserID bucket) |
| `/admin/notifications/channels/:id/test` | 5/min | per-admin (same bucket as broadcast) |
| Inbound IP (generic) | Default tier (10/s) | per-IP |

Over-budget responses: `429 Too Many Requests` with JSON `{"error": "rate_limited", "message": "broadcast rate limit exceeded (5/min per admin)"}`.

Concurrency cap: dispatcher fans out to at most `MaxConcurrentSenders` (default 4) outbound HTTP calls per envelope. A broadcast to 10 channels proceeds as 3 batches of roughly 4/4/2 rather than 10 simultaneous TLS handshakes. Tune via `Config.MaxConcurrentSenders` in `serve.go` if a specific deployment needs different parallelism.

---

## Troubleshooting the dispatcher itself

- `redis ping` fails in logs → `systemctl status redis-server`. Jabali-panel `Requires=redis-server.service`, so panel-api won't start without Redis.
- Stream entries not consumed → `redis-cli XINFO GROUPS jabali:notifications:queue` — check `consumer-group: dispatcher` exists and has entries in its PEL.
- DLQ growing → inspect with `redis-cli XRANGE jabali:notifications:dlq - + COUNT 50`. Parse errors surface as `reason: parse_error: ...` field; `max_retries_exceeded` for timeouts.
- Channel auto-disabled too aggressively → bump `Config.CircuitBreakerLimit` in `startNotificationDispatcher`.

---

## Related docs

- ADR-0056 — dispatcher architecture (Redis Streams + consumer group)
- ADR-0057 — VAPID keypair storage
- ADR-0058 — ntfy channel contract
- ADR-0059 — Redis as shared local cache/queue
