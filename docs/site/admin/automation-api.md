# Automation API

`/jabali-admin/automation`. Scoped API tokens for the future Automation API.

## Current status

The UI for minting and revoking scoped tokens is shipped. The public Automation API surface itself is rolling out incrementally — read endpoints first, then write endpoints, then bulk operations. Until the surface is GA, prefer the CLI for unattended automation.

## Token shape

Each token has:

- **Name** — operator label.
- **Owner** — the panel user the token acts as (an admin token can target any user; a user token is scoped to the owner).
- **Scopes** — explicit list of API actions the token may call. Currently shipped scopes:
  - `domains:read`
  - `mailboxes:read`
  - `databases:read`
  - `audit:read`
  - `backups:trigger`
  - `ssl:renew`
- **Expiration** — optional; tokens without an expiry persist until revoked.
- **IP allowlist** — optional CIDR list; requests from outside the list are rejected.
- **Rate limit** — per-token requests per minute (default 60).

## Minting a token

Click **Create token**, fill in the fields, click **Generate**. The token value is displayed once. Store it; the panel does not retain the value (only a salted hash).

## Using a token

```http
GET /api/v1/admin/domains
Authorization: Bearer <token>
```

Tokens are sent as the `Authorization: Bearer …` header. The response includes `X-RateLimit-Remaining` and `X-RateLimit-Reset` headers.

## Revocation

Revoke from the same page. Revocation takes effect immediately; in-flight requests carrying the token complete but no further requests are accepted.

## Audit

Every action a token performs writes an audit row with the token id captured in the actor metadata. The token name and owner are visible; the token value is not.

## Roadmap

Planned scopes (not yet shipped):

- Mutating endpoints for domains, mailboxes, databases (write scopes).
- Webhook delivery for notification events (the inverse — letting external systems consume panel events).
- Per-app scopes for the [Applications](./applications.md) registry (e.g. `apps:wordpress:install`).

## CLI as a stable alternative

While the Automation API is rolling out, the CLI is the stable automation contract. Every UI action has a CLI equivalent; CLI exit codes are stable across releases (see [Platform CLI](../platform/cli.md)).
