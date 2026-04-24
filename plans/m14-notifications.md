# M14 — Notifications (channels + in-app bell + Web Push)

> 9-step construction plan. Cold-start executable per step. Feature branches → FF merge to main.

## Objective

Admins configure out-of-band channels (email, Slack, Discord, ntfy.sh, generic webhook) AND an in-app notification bell dropdown that also subscribes the current admin to Web Push (VAPID) for pages delivered when the panel tab is closed.

System events that fan out to notifications:
- Domain expiry (7d / 1d warning)
- Certificate renewal (success + failure)
- Disk full (85% + 95% thresholds)
- Service down (reconciler-detected)
- Backup failure
- CrowdSec ban-rate spike (> N bans in 5 min window)

## Constraints

- **ADR-0002 (DB as truth).** All channel configs, subscriptions, and history rows live in the DB. Reconciler never talks to channel APIs directly — goes through panel-api's notification dispatcher.
- **ADR-0001 (Go agent NDJSON).** Agent-originated system alerts (cert renew failure from certbot, disk-full from df-watcher) go through a new agent command `notifications.send` that POSTs to the panel-api internal socket, never directly to external APIs.
- **ADR-0050 (M25 unix sockets).** Any outbound HTTPS call (Slack, Discord, ntfy, generic webhook, Web Push) originates from panel-api; agent never reaches the public internet for notifications.
- **M6 Email (shipped).** Email channel reuses the existing Stalwart submission socket / outbound relay path.
- **No PII in URLs.** Webhook bodies are JSON POST; never embed tokens or recipient IDs in query strings.
- **HMAC for generic webhooks.** Generic webhook channel signs every body with `X-Jabali-Signature: sha256=<hex>` using a per-channel secret; receivers verify.
- **VAPID keys server-global, not per-user.** One keypair per installation, regenerated only on explicit operator reset.

## Out of scope

- User-side (hosting customer) notifications (WP update available, quota warning) — separate milestone.
- SMS / WhatsApp channels.
- Digest / batching (first cut sends one notification per event; no rollup).
- Rich media in Web Push (icon + title + body only; no images, no actions yet).
- Editable message templates per event type (first cut uses hard-coded templates; per-channel prefix/suffix only).
- Multi-tenant channel scoping (every channel fires for every system event in scope).

## ADR targets

| ADR  | Title                                                      | Decision                                                                                                   |
| ---- | ---------------------------------------------------------- | ---------------------------------------------------------------------------------------------------------- |
| 0056 | Notification dispatcher: in-process goroutine + BoltDB queue | Single-process queue survives restarts; no Redis/AMQP dep                                                  |
| 0057 | Web Push via VAPID, keys in server_settings                | W3C Push API + VAPID; SherClockHolmes/webpush-go as signing lib; keys regenerate only on explicit reset    |
| 0058 | ntfy.sh: POST topic URL, optional bearer + priority + tags | No vendor SDK; plain HTTP POST; self-hosted or public; channel config stores full topic URL + optional bearer |

---

## Step 1 — DB schema + VAPID bootstrap + ADR 0056/0057/0058

### Context brief
Four new tables; ServerSetting rows for VAPID keypair seeded on first boot by repository code (not migration — per `feedback_migration_data_seed_ordering`).

Tables:

```sql
-- notification_channels
id               CHAR(26) PK (ULID)
name             VARCHAR(120) NOT NULL
kind             ENUM('email','slack','discord','ntfy','webhook','webpush') NOT NULL
config_json      JSON NOT NULL           -- {url, bearer?, hmac_secret?, priority?, tags?[], to_email?, from_email?}
enabled          TINYINT(1) NOT NULL DEFAULT 1
created_at, updated_at

-- webhook_endpoints (legacy name retained from blueprint; holds per-channel retry + last-error state)
channel_id       CHAR(26) PK+FK
last_success_at  DATETIME NULL
last_error       TEXT NULL
consecutive_failures INT NOT NULL DEFAULT 0
backoff_until    DATETIME NULL

-- notification_history
id               CHAR(26) PK
channel_id       CHAR(26) FK (null for in-app-bell-only)
event_kind       VARCHAR(60) NOT NULL     -- e.g. domain.expiry.7d
severity         ENUM('info','warning','error','critical') NOT NULL
title            VARCHAR(200) NOT NULL
body             TEXT NOT NULL
deeplink         VARCHAR(500) NULL        -- SPA route or full URL
outcome          ENUM('pending','sent','failed','skipped') NOT NULL DEFAULT 'pending'
retry_count      INT NOT NULL DEFAULT 0
error_message    TEXT NULL
read_at          DATETIME NULL            -- for in-app bell mark-as-read
user_id          CHAR(26) NULL            -- admin who read it / recipient for per-user Web Push
created_at, updated_at
INDEX (event_kind, created_at)
INDEX (user_id, read_at)

-- webpush_subscriptions
id               CHAR(26) PK
user_id          CHAR(26) NOT NULL FK
endpoint         VARCHAR(500) NOT NULL UNIQUE    -- browser push service URL
p256dh           VARCHAR(200) NOT NULL           -- client public key
auth             VARCHAR(50) NOT NULL            -- client auth secret
user_agent       VARCHAR(300) NULL
created_at, last_used_at
INDEX (user_id)
```

ServerSettings seeded at boot:
- `vapid_public_key` (base64-url)
- `vapid_private_key` (base64-url) — generated via `github.com/SherClockHolmes/webpush-go`
- `vapid_subject` — `mailto:admin@<panel_hostname>` from existing config

### Tasks
1. Write ADRs 0056 / 0057 / 0058, accepted status. Update `docs/adr/README.md`.
2. Migration `000059_create_notifications.up.sql` + `.down.sql`. Schema only.
3. Go models in `panel-api/internal/models/` — NotificationChannel, WebhookEndpoint (extend or fold), NotificationHistory, WebPushSubscription.
4. Repository interfaces + GORM impls in `panel-api/internal/repository/`.
5. Seed VAPID keypair in `ServerSettingRepository.EnsureDefault` called from `serve.go` first-boot init (mirror how ManagedIP default row seeds).
6. `go.mod`: add `github.com/SherClockHolmes/webpush-go`.

### Verify
```bash
go test ./panel-api/internal/repository/... -run Notification -count=1
mariadb jabali_panel -e "DESCRIBE notification_channels; DESCRIBE notification_history; DESCRIBE webpush_subscriptions"
mariadb jabali_panel -e "SELECT key_name FROM server_settings WHERE key_name LIKE 'vapid%'"  # 3 rows
```

### Branch
`feat/m14-db-notifications`

---

## Step 2 — Dispatcher + channel sender registry

### Context brief
In-process notification dispatcher: single goroutine per panel-api process. Receives `NotificationEnvelope{EventKind, Severity, Title, Body, Deeplink}` on a buffered channel, fans out to every enabled matching channel, writes `notification_history` row per channel with outcome.

Retry: exponential backoff (1m, 5m, 30m, 2h, 8h), max 5 attempts. On every attempt fill `error_message`, increment `retry_count`, update `notification_history.outcome`. Final failure flips channel's `consecutive_failures += 1`, sets `backoff_until`. Three consecutive failures → channel auto-disabled + one in-app-bell critical notification.

Channel sender registry (go interface):

```go
type ChannelSender interface {
    Kind() string
    Send(ctx context.Context, cfg ChannelConfig, envelope Envelope) error
}
```

One sender per kind. Registered at startup. Dispatcher looks up by `channel.kind`.

### Tasks
1. Create `panel-api/internal/notifications/` package with:
   - `dispatcher.go` — goroutine, retry, history writes
   - `sender.go` — interface + registry
   - `envelope.go` — struct types
2. Unit tests with fake sender — assert retry count, backoff timing (use injected clock), auto-disable after N failures.
3. Queue buffer size 256; on full → drop oldest + log WARN. Document in ADR-0056.
4. Start dispatcher in `serve.go` after repos + agent init; `context.Background()` with a cancel on shutdown.

### Verify
```bash
go test ./panel-api/internal/notifications/... -count=1 -race
```

### Branch
`feat/m14-dispatcher`

---

## Step 3 — Channel senders (email, Slack, Discord, ntfy, webhook, webpush)

### Context brief
One file per sender under `panel-api/internal/notifications/senders/`:

| File            | Config shape                                                  | HTTP verb | Notes                                                      |
| --------------- | ------------------------------------------------------------- | --------- | ---------------------------------------------------------- |
| `email.go`      | `{to: [string], from: string}`                                | —         | Uses existing Stalwart submission; bypass dispatcher's HTTP client |
| `slack.go`      | `{webhook_url: string}`                                       | POST      | `{text, blocks: [...]}` Slack Incoming Webhook format      |
| `discord.go`    | `{webhook_url: string}`                                       | POST      | `{content, embeds: [...]}` Discord Webhook format          |
| `ntfy.go`       | `{topic_url: string, bearer?: string, priority?: int, tags?: []string}` | POST | Body = plain-text title + body; headers: `Title`, `Priority`, `Tags`, optional `Authorization: Bearer ...` |
| `webhook.go`    | `{url: string, hmac_secret: string}`                          | POST      | JSON body `{event_kind, severity, title, body, deeplink, ts}`; `X-Jabali-Signature: sha256=<hex>` |
| `webpush.go`    | — (reads `webpush_subscriptions` directly)                    | —         | Uses `webpush-go`; VAPID keys from ServerSettings; iterates all subs for `user_id`; 410 Gone → delete subscription |

Shared `httpClient` with 10s timeout, TLS pinning OFF (channels are admin-configured third-party URLs), retries disabled (dispatcher handles retry).

### Tasks
1. Implement the 6 files + unit tests (mock HTTP via httptest.NewServer).
2. Register all senders in `serve.go` dispatcher init.
3. Template rendering: hard-coded per-event templates in `internal/notifications/templates.go` (function `RenderForChannel(envelope, kind) (title, body string)`). Slack + Discord get markdown; ntfy + webpush get plain text; email gets simple HTML.
4. HMAC for generic webhook: `hex.EncodeToString(hmac.New(sha256.New, secret).Sum(body))`.
5. Web Push 410-Gone handling: delete `webpush_subscriptions` row, log INFO.

### Verify
```bash
go test ./panel-api/internal/notifications/senders/... -count=1 -race
# smoke: create a channel via direct DB insert, fire test envelope:
curl -X POST .../admin/notifications/test-send  # requires Step 6
```

### Branch
`feat/m14-channel-senders`

---

## Step 4 — Agent command `notifications.send` + event sources

### Context brief
Agent-originated alerts (cert renew failures from certbot hook, disk-full from a periodic df check, service-down from systemd watchdog) POST to panel-api's internal localhost socket endpoint instead of calling external channels directly — keeps all outbound HTTPS in panel-api (ADR-0050).

Agent command: `notifications.send` with params `{event_kind, severity, title, body, deeplink?}`. Agent handler opens a HTTP client bound to `/run/jabali-panel/api.sock` (M25) and POSTs to `/api/v1/internal/notifications/enqueue` behind `middleware.RequireLocalhost()`.

Panel-api event sources (in `panel-api/internal/eventsources/`):

| Source         | Trigger                                         | Envelope                                                  |
| -------------- | ----------------------------------------------- | --------------------------------------------------------- |
| `domain_expiry.go` | Daily timer; query `domains WHERE expires_at <= NOW + 7d` and NOW + 1d | `domain.expiry.7d` / `domain.expiry.1d`, severity warning/error |
| `cert_renew.go`    | Hook on certbot success/failure (read state from reconciler) | `cert.renew.ok` info / `cert.renew.fail` error |
| `disk_full.go`     | Every 10 min; df on `/`, `/var/www`, `/var/lib/mysql` thresholds 85/95 | `disk.full.warn` / `disk.full.crit` |
| `service_down.go`  | Reconciler detects `systemctl is-active != active` for jabali-* | `service.down`, error |
| `backup_fail.go`   | (stub — wires in when M15 backups lands) | — |
| `crowdsec_spike.go` | Poll `cscli decisions count` every 5 min; > threshold → fire | `crowdsec.ban.spike`, warning |

Each source is a tiny goroutine started by `serve.go`, enqueues envelopes on dispatcher. No direct channel calls.

### Tasks
1. Implement agent command `notifications.send` with argument validation (event_kind ∈ allowlist, title ≤ 200 chars, body ≤ 2000 chars).
2. Wire panel-api `/api/v1/internal/notifications/enqueue` handler behind `RequireLocalhost`. Same envelope shape.
3. Implement the 5 event-source goroutines (skip `backup_fail` — empty file with TODO).
4. Unit tests per source with fake clock + fake query results.

### Verify
```bash
# seed a domain with expires_at = tomorrow, wait 10s past the next daily-timer tick → history row appears.
# disk_full: mount tmpfs at 95% usage → history row fires within 10 min.
echo '{"id":"1","command":"notifications.send","params":{"event_kind":"service.down","severity":"error","title":"Test","body":"hi"}}' \
  | socat -t5 - UNIX-CONNECT:/run/jabali/agent.sock
# then: curl .../admin/notifications/inbox  → see the envelope
```

### Branch
`feat/m14-event-sources`

---

## Step 5 — REST API

### Context brief

All behind `middleware.RequireAdmin()` except `/inbox` + `/webpush/*` which also accept user sessions (regular users get an empty inbox for now but the endpoint is wired for user-side future work).

```
GET    /api/v1/admin/notifications/channels               ({data, total, page, page_size})
POST   /api/v1/admin/notifications/channels               {name, kind, config_json, enabled}
PATCH  /api/v1/admin/notifications/channels/:id           partial update
DELETE /api/v1/admin/notifications/channels/:id
POST   /api/v1/admin/notifications/channels/:id/test      fires a synthetic "test" envelope to one channel

POST   /api/v1/admin/notifications/broadcast              {title, body, severity, deeplink?} → fans out to every enabled channel + bell

GET    /api/v1/notifications/inbox?unread_only=           bell dropdown data (current user)
POST   /api/v1/notifications/inbox/:id/read
POST   /api/v1/notifications/inbox/read-all

GET    /api/v1/notifications/webpush/vapid-public-key     public key for browser subscribe
POST   /api/v1/notifications/webpush/subscribe            {endpoint, keys:{p256dh, auth}, user_agent}
DELETE /api/v1/notifications/webpush/subscribe            unsubscribe current browser

POST   /api/v1/internal/notifications/enqueue             from agent — RequireLocalhost
```

Response envelopes use the project standard `{data, total, page, page_size}` per `feedback_verify_wire_contract`.

### Tasks
1. Three handler files: `notifications_channels.go`, `notifications_inbox.go`, `notifications_webpush.go`.
2. Tests alongside each.
3. Register routes in `app.go` mirroring `RegisterIPRoutes` pattern.
4. Rate-limit `/broadcast` + `/channels/:id/test` — max 10 req/min per admin (existing middleware or a small new one).
5. Config validation on POST/PATCH: kind enum match; per-kind required fields (e.g. slack requires `webhook_url`); URL parses via `url.Parse`; HMAC secret min length 16 for generic webhook.

### Verify
```bash
go test ./panel-api/internal/api/... -run Notification -count=1
# integration:
curl -X POST .../admin/notifications/channels -d '{"name":"ops-ntfy","kind":"ntfy","config_json":{"topic_url":"https://ntfy.sh/jabali-ops-XXX"}}'
curl -X POST .../admin/notifications/channels/<id>/test
```

### Branch
`feat/m14-api-notifications`

---

## Step 6 — UI: admin Channels page

### Context brief
New route `/jabali-admin/notifications/channels`. Same patterns as `AdminIPList.tsx`:
- SearchableTable with columns: name, kind (colored tag), enabled (Switch), last_success_at, consecutive_failures, actions (edit / test / delete)
- AdminLayout wrapper enforces admin-only (reuse existing guard)
- Add / Edit Drawer with dynamic form that swaps fields based on `kind` select (e.g. ntfy shows topic_url + bearer + priority + tags; slack shows webhook_url only)
- Test button fires POST `/channels/:id/test`, shows AntD `notification.success` / `.error` based on outcome
- Delete gated by `Popconfirm`

### Tasks
1. Create `panel-ui/src/shells/admin/notifications/` with `AdminChannelsList.tsx`, `AdminChannelDrawer.tsx` (create + edit reuse), `channelKindConfig.tsx` (per-kind form field definitions).
2. Nav item in admin sider using Lucide `Bell` icon; route registered inside AdminLayout.
3. `useMutation` hooks for create/update/delete/test via existing `useQueries` helpers.
4. Responsive: Drawer width 520 desktop, fullscreen on mobile (ADR-0046).

### Verify
- `npm run build` + `npx tsc --noEmit` green.
- Manual: add ntfy channel pointing at `https://ntfy.sh/jabali-test-<random>`, click Test, observe push on phone app.

### Branch
`feat/m14-ui-channels`

---

## Step 7 — UI: bell dropdown + Web Push enrolment + service worker

### Context brief
Topbar bell (Lucide `Bell`) with unread badge. Dropdown content:
- Header: "Notifications" + "Mark all read" link
- List: up to 10 recent items with severity icon + title + relative time + deeplink
- Footer: "View all" link → `/jabali-admin/notifications/inbox` (full-page inbox view)
- Toggle row: "Enable browser push" — shows browser permission state + subscribe/unsubscribe button

Service worker at `panel-ui/public/sw-push.js`:
```js
self.addEventListener('push', (event) => {
  const data = event.data?.json() ?? {};
  event.waitUntil(
    self.registration.showNotification(data.title || 'Jabali Panel', {
      body: data.body,
      icon: '/favicon.svg',
      data: { deeplink: data.deeplink },
    })
  );
});
self.addEventListener('notificationclick', (event) => {
  event.notification.close();
  event.waitUntil(clients.openWindow(event.notification.data?.deeplink || '/'));
});
```

Registered once at admin shell mount. Polling fallback for inbox: `useQuery` with `refetchInterval: 30_000` regardless of push subscription state (belt + braces).

VAPID subscribe flow:
1. `navigator.serviceWorker.register('/sw-push.js')`
2. `registration.pushManager.subscribe({userVisibleOnly: true, applicationServerKey: base64UrlToUint8Array(vapidPublicKey)})`
3. POST `{endpoint, keys, user_agent}` to `/notifications/webpush/subscribe`
4. On unsubscribe: `subscription.unsubscribe()` + DELETE endpoint.

### Tasks
1. Create `NotificationBell.tsx` + `NotificationBellDropdown.tsx` + `useWebPushSubscription.ts` hook.
2. Mount bell in admin topbar (find existing Layout.Header — grep for `AdminLayout` or sider header).
3. Create `panel-ui/public/sw-push.js`.
4. Handle permission states: `default` (show enable button), `granted` (show subscribed indicator + unsubscribe), `denied` (show disabled message pointing at browser settings).
5. Full-page inbox at `/jabali-admin/notifications/inbox` — SearchableTable with filters by severity + event_kind + date range.

### Verify
- Load admin shell in Chrome/Firefox, click Enable push, accept prompt, confirm `webpush_subscriptions` row appears in DB.
- Fire test envelope via `/admin/notifications/broadcast`, confirm OS-level notification appears even with panel tab in background.
- Click notification → panel opens to the deeplink.

### Branch
`feat/m14-ui-bell-webpush`

---

## Step 8 — Cross-browser verification + rate limiting hardening

### Context brief
Web Push quirks across vendors. VAPID keys work cross-vendor but behaviours differ:
- Chrome/Edge: FCM endpoint; 410 Gone on revocation.
- Firefox: Mozilla autopush endpoint; 410 Gone on revocation.
- Safari 16+: APNs backend via VAPID (supported since macOS 13 / iOS 16.4); requires `userVisibleOnly: true` at subscribe time.
- Brave: blocks Google FCM by default; subscribe may fail silently — log and show UI warning.

### Tasks
1. Manual matrix smoke: Chrome, Firefox, Safari (macOS + iOS), Edge.
2. Log every push failure with the endpoint host prefix (FCM/Mozilla/APNs/other) for debugging.
3. Wire outbound HTTPS client to respect panel-wide timeout + per-channel concurrency cap: no more than 4 concurrent outbound HTTP channel calls (prevents dispatcher-goroutine stampede on broadcast to 10 channels simultaneously).
4. Broadcast rate limit: same admin cannot broadcast more than 5/min; anti-abuse on shared terminals.

### Verify
- 4-browser matrix in the runbook.
- Artillery / k6 smoke: POST broadcast 20× in 10s, observe only 5 succeed, 15 get 429.

### Branch
`feat/m14-browser-matrix`

---

## Step 9 — E2E + runbook + memory

### Context brief
Playwright covers the in-app bell + channel CRUD; Web Push permission prompt is browser-native and cannot be scripted cross-vendor, so that part is documented in the runbook as manual smoke.

### Tasks
1. Playwright `panel-ui/e2e/admin-notifications.spec.ts`:
   - Create ntfy channel with mock server (nock or Playwright request interception); test-send → 200.
   - Create slack channel; test-send with intercept → body contains the envelope title.
   - Broadcast; bell unread badge appears; click bell; list shows the item; mark-all-read; badge hides.
   - Inbox page filter by severity=error returns only error rows.
2. Runbook `plans/m14-notifications-runbook.md`:
   - Rotate VAPID keys (every subscription becomes stale — document mass re-subscribe flow).
   - Channel troubleshooting (Slack 404, ntfy 401, Discord rate-limit, webpush 410).
   - How to add a new event source (reference: the 5 existing sources).
   - Browser matrix for Web Push.
3. Update memory: write `project_plan_m14_notifications.md` + link from `MEMORY.md`. After merge, write `project_m14_shipped.md`.
4. Update `docs/BLUEPRINT.md` "What's shipped" section.

### Verify
- Gitea CI green on the final PR.
- VM smoke: fire every event source (domain expiry via seeded row, cert renew via certbot dry-run, disk full via tmpfs mount, service down via `systemctl stop` a non-critical service, crowdsec spike via mass `cscli decisions add`).

### Branch
`feat/m14-e2e-runbook`

---

## Dependency DAG

```
Step 1 (DB + VAPID + ADRs)
  └── Step 2 (dispatcher)
        └── Step 3 (senders, 6 of them — can parallelize sub-tasks)
              └── Step 4 (agent cmd + event sources)
                    └── Step 5 (REST API)
                          ├── Step 6 (UI channels page)
                          └── Step 7 (UI bell + Web Push + sw)
                                └── Step 8 (browser matrix + rate limit)
                                      └── Step 9 (E2E + runbook)

Parallel windows:
- Inside Step 3: the 6 senders can be written in parallel.
- Step 6 and Step 7 can run in parallel once Step 5 ships.
```

## Risk register

| Risk | Severity | Mitigation |
|------|----------|------------|
| Web Push fails silently on some browsers | HIGH | Step 8 matrix smoke; log endpoint-host prefix; show UI warning when subscribe rejects |
| VAPID key rotation invalidates all subscriptions | HIGH | Document in runbook; add a "Rotate keys" button that also truncates `webpush_subscriptions`; warn before confirm |
| Dispatcher goroutine deadlock on full queue | MEDIUM | Buffer 256; drop-oldest + WARN log; health endpoint exposes queue depth |
| Generic-webhook HMAC secret leaks via env var | MEDIUM | Stored in DB only, never logged; test-send response does not echo secret |
| Broadcast floods channels on bug | MEDIUM | Rate limit per admin; circuit-breaker (channel auto-disables after 3 consecutive failures) |
| Domain-expiry daily timer drifts on reboot | LOW | Uses `time.AfterFunc` anchored to midnight UTC; re-arms on panel start |
| Certbot hook runs before panel-api is ready | LOW | Agent `notifications.send` tolerates 503 from panel-api with retry (agent-side buffer, flushed on success) |
| Disk-full check false positive on tmpfs / bind-mounts | LOW | Skip filesystems with `tmpfs`, `devtmpfs`, `overlay` type |

## Cold-start cheat sheet

```bash
cd /home/shuki/projects/jabali2
git fetch origin main && git checkout -b feat/m14-db-notifications origin/main
# read plans/m14-notifications.md Step 1, then implement. Commit on branch.
# merge to main after each step is green + tested on VM.

# Before declaring any step done:
go test ./... -count=1
cd panel-ui && npm run build && npx tsc --noEmit
```
