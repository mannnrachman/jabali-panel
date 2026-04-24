# ADR-0058: M14 — ntfy.sh channel: plain HTTP POST + optional bearer + priority + tags

**Status:** Accepted (2026-04-24)
**Driven by:** Plan `plans/m14-notifications.md` Step 1 (channel table schema) + Step 3 (sender implementation).

## Context

ntfy.sh is one of the six notification channels M14 ships (alongside email, Slack, Discord, generic webhook, and Web Push). Admins may be self-hosting their own ntfy instance (`https://ntfy.mycompany.com`) or using the public `https://ntfy.sh` service. The protocol is dead simple: an HTTP POST to a *topic URL* with the message in the body, plus a handful of optional headers for priority/tags/attachments.

The question: do we integrate via a vendor SDK (there isn't a real one for Go — ntfy ships a CLI and a Go library mostly for the server side, not clients), a community wrapper, or just hit the HTTP endpoint directly?

## Decision

**Plain HTTP POST, no SDK.** The channel config JSON stores the full topic URL (operator copy-pastes from their ntfy instance), optionally a bearer token for auth, optional priority, optional tags. Sender is ~40 lines of Go.

### Channel config shape (stored in `notification_channels.config_json`)

```json
{
  "url": "https://ntfy.example.com/alerts-panel",
  "bearer": "tk_abc123...",      // optional; omitted for public topics
  "priority": 3,                 // optional; 1..5, ntfy default 3
  "tags": ["warning", "panel"]   // optional; emoji tags rendered by ntfy client
}
```

Everything beyond `url` is optional. The sender skips a field if the corresponding key is absent — the ntfy server applies its own defaults (priority 3, no tags).

### Request shape

```
POST <url>
Content-Type: text/plain
Title: <NotificationEnvelope.Title>           # if non-empty
Priority: <config.priority>                   # if set
Tags: <comma-join(config.tags)>               # if set
Authorization: Bearer <config.bearer>         # if set
Icon: https://<panel_hostname>/icon.png       # hard-coded for brand recognition
Click: <NotificationEnvelope.Deeplink>        # if set — makes the mobile push tappable

<NotificationEnvelope.Body>
```

Body is plain text (not JSON) because that's what ntfy expects; priority/tags/title all ride as headers.

### Retry + failure handling

- Transient errors (connection refused, 5xx, `Retry-After` header): sender backs off per its retry loop (ADR-0056, sender-owned retry).
- 401 (bad bearer): `senders.ErrPermanent` — operator has to fix config. Routed to DLQ.
- 403 (topic forbidden): same — permanent.
- 404 (topic doesn't exist on self-hosted ntfy, or a typo in the URL): permanent.
- 413 (payload too large): permanent; operator should shorten body templates. A real event body that trips this is a bug on our side.

### Why the hard-coded `Icon` header

The panel hostname is known at boot (from existing config) and `icon.png` is already served from the public path for the login page. Including it in every ntfy POST means the mobile app shows the panel's favicon on lock-screen notifications — operators asked for this during the plan review. One line of code; no new asset path.

## Alternatives considered

**Use the `heckel.io/ntfy` Go library.** Rejected: it's the server library; it doesn't ship a client SDK, and vendoring the whole server for `net/http.Post` wrappers is upside-down.

**Community wrapper `github.com/hmkio/ntfy-go` (and similar).** Rejected: small, one-maintainer, no release cadence; the protocol is genuinely 40 lines.

**Webhook channel (the generic one) for ntfy.** Rejected: generic webhook sends JSON with our schema; ntfy wants plain-text body + headers. Mapping would still need an ntfy-specific transform layer, so the "it's just a webhook" claim is cosmetic. A dedicated channel type with a small transform is cleaner than a special-case branch inside the generic webhook sender.

## Consequences

**Positive:**
- Zero-dep. The sender is a `net/http` POST; failure modes map cleanly to the dispatcher's permanent / transient split.
- Self-hosted vs public ntfy is a config difference (the URL), not a code path. Both work from day one.
- Matches how every other small monitoring tool integrates ntfy (the protocol is deliberately HTTP).

**Negative:**
- No support for attachments, auto-delete, email forwarding, or the other ntfy features exposed via `ntfy`'s richer API. If any of those become required, we extend the sender — they're all additional headers, not a different protocol.
- Priority/tags schema validation is minimal: sender drops the headers if they're malformed rather than failing the send. Operator feedback is an admin-UI concern (Step 6 — channel test button shows the rendered headers).
- If ntfy the service changes its body/header contract (unlikely — it's been stable for years and is an open protocol), we have to update the sender. That's the cost of no SDK layer.

## Related

- Plan: `plans/m14-notifications.md` — Step 1 (schema), Step 3 (sender)
- Code (Step 3): `panel-api/internal/notif/senders/ntfy.go`
- ADR-0056: Dispatcher + sender contract (ErrPermanent semantics, retry loop)
- ntfy protocol docs: `https://docs.ntfy.sh/publish/`
