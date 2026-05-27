# DNSSEC (User)

Per-domain DNSSEC signing. Toggle under Domain Edit → DNSSEC.

## What DNSSEC does

DNSSEC ("Domain Name System Security Extensions") signs every record in your zone with cryptographic keys so that a resolver can detect whether an answer was tampered with in flight. The most common attack DNSSEC defeats is response forgery (an attacker between you and the resolver injecting a false IP address for your domain).

DNSSEC is opt-in per domain. The default is off; it costs a small amount of CPU on the panel host and requires a one-time action at the parent registrar.

## Turning it on

1. Open your domain in [Domains](./domains.md) → Edit.
2. Toggle **DNSSEC** to on.
3. Within a couple of seconds the page displays a **DS record** — a short text string containing the algorithm, key id, and digest.
4. Publish the DS record at the parent registrar (where you registered the domain). Every registrar has a "DNSSEC" form somewhere; paste the DS values in.
5. Wait for the parent to push the DS into the parent zone — typically minutes, up to one day depending on the registrar's update cadence.
6. Verify with an online DNSSEC analyser (Verisign Labs, DNSViz) or with `dig +dnssec @8.8.8.8 example.com SOA`; the answer should carry the `ad` (Authenticated Data) flag.

## What happens if you skip step 4

Your zone is signed but the chain of trust is broken — validating resolvers treat the zone as if unsigned. There is no negative consequence beyond not getting the protection benefit. Non-validating resolvers (most public ones, plus stub resolvers on home routers) continue to work normally.

## Turning it off

Toggle off. The agent removes the keys and re-publishes an unsigned zone. **Remove the DS from the parent registrar at the same time** — leaving the DS in place after turning off signing creates a validation failure for resolvers and may make the domain unreachable.

## Key rotation

Manual. Key rotation is not automated yet. When you eventually need to roll the keys, contact the operator; they handle the `pdnsutil activate-zone-key` and `deactivate-zone-key` dance.

## Algorithm

Default: ECDSA P-256 SHA-256 (algorithm 13). Modern, fast, supported by every major registrar. If your registrar does not support algorithm 13, ask the operator to roll the key to algorithm 8 (RSA-SHA256). This is uncommon in 2026.
