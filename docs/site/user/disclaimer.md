# Disclaimer

`/jabali-panel/mail/disclaimer`. A server-side disclaimer appended to outbound mail per domain.

## Configuration

Per-domain:

- **Active** — on / off.
- **Plain-text disclaimer** — the text to append to plain-text messages and to the text part of multipart messages.
- **HTML disclaimer** — the HTML fragment to append to HTML messages and to the HTML part of multipart messages.
- **Apply to** — all outbound, or only outbound to external recipients (not to other mailboxes on the same panel).

## Placement

Stalwart appends the disclaimer at the bottom of the message body. The header and the signature line (if your client uses a standard `-- \n` separator) are preserved; the disclaimer follows.

For HTML messages, the disclaimer is wrapped in a `<div class="jabali-disclaimer">` so you can target it with the receiving client's CSS, or so that bridging tools can strip it.

## Templating variables

The disclaimer body may include:

- `${sender-name}` — the display name of the sender.
- `${sender-address}` — the sender's email.
- `${domain}` — the sending domain.
- `${date}` — the message timestamp in the recipient's locale (best effort; defaults to UTC).

## Common patterns

- **Legal notice** — confidentiality clause, recipient-error instructions.
- **Marketing footer** — small "Powered by ..." attribution with a logo.
- **Compliance** — required disclosures for regulated industries (financial advice, medical).

## Caveats

- HTML disclaimer rendering depends on the receiving client. Outlook, Gmail, Apple Mail, and Thunderbird all render the disclaimer as expected; older clients or text-only clients see only the plain-text version.
- DKIM signatures are computed *after* the disclaimer is appended, so the signature stays valid.
- The disclaimer is not added to messages sent by automated systems on the host (notifications from the panel itself, cron output) — only to outbound mail sent through SMTP submission.

## HTML coverage

HTML rendering coverage was deferred pending a live spike on a test VM (see ADR-0052). Test against your primary recipient base before relying on the HTML output for critical compliance.
