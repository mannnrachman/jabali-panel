# Catch-all

`/jabali-panel/mail/catch-all`. What happens to mail addressed to a local part that does not exist as a mailbox or forwarder.

## Options

- **Reject** (default) — Stalwart returns SMTP `550 5.1.1 User unknown` to the sending server. The sender's MTA generates a bounce. This is the recommended default because it prevents backscatter and signals to legitimate senders that the address is wrong.
- **Discard** — accept the mail and silently drop it. Useful only when reject is causing legitimate but mistyped mail to bounce (rare); generally discouraged.
- **Forward to mailbox** — accept and deliver to a designated mailbox. The destination mailbox sees the original `To:` header so you can route or filter on it.
- **Forward to address** — accept and forward to an external address. Subject to the same SRS / SPF considerations as ordinary [Forwarders](./forwarders.md).

## Per-domain configuration

Catch-all is per-domain. Each domain may have its own setting; the page lists one row per mail-enabled domain in your account.

## Spam implications

Catch-all "Forward to mailbox" makes the destination mailbox an easy target for dictionary spam (every typo of a real address lands here). The destination mailbox typically receives orders of magnitude more spam than a normal mailbox. Stalwart's spam filter applies, but consider the trade-off before enabling.

## Recommendation

Default to **Reject**. Switch to **Forward to mailbox** only when you have a specific business reason and you accept the spam load.

## What about "Reject" plus an autoresponder

Not supported. Reject and autoresponse are mutually exclusive — the SMTP transaction is rejected before the autoresponder could fire. To send a "this address is wrong, please use X" reply automatically, accept into a mailbox + set an autoresponder on that mailbox + redirect / discard the original.
