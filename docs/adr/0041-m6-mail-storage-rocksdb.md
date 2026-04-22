# ADR-0041: M6 mail storage — RocksDB for mail, Bulwark for webmail

**Date**: 2026-04-21
**Status**: accepted (amended 2026-04-21 by ADR-0045 — on-disk config moved from TOML to `config.json` + `stalwart-cli apply` plan)
**Deciders**: shuki + Claude
**Related**: ADR-0002 (DB as source of truth), ADR-0008 (sibling repos out of scope — amended here for Bulwark), ADR-0042, ADR-0043, ADR-0044, ADR-0045

## Context

M6 turns the panel into a mail host. Every operator-facing unit (mailbox, quota, DKIM key, DNS record) already has a home in MariaDB + PowerDNS + the panel's filesystem. What does *not* have a home is the actual mail — messages, mailbox indices, full-text search, bayes state. A separate, Stalwart-owned layer has to sit alongside the existing control plane.

Two choices have to be made up front because every later step depends on them:

1. **Mail data backend.** Where do the bytes of every inbound/outbound message live?
2. **Webmail client.** What do end users click on when they want to read their mail?

Both live in ADR-0041 because they're the two "what is the *product* shape" decisions. The schema for account identity (ADR-0042), the DKIM rotation policy (ADR-0043), and the imap-migrate deferral (ADR-0044) follow from these two.

### Mail data — options considered

| Option | Pros | Cons |
|---|---|---|
| **RocksDB** (Stalwart default) | Embedded, zero external deps, deduped blob store, FTS indices + bayes spam state built in, single-node perf > Maildir at scale | Opaque to panel SQL — usage bytes have to be asked for via JMAP `Quota/get`, not `SELECT SUM(size)` |
| Maildir on local FS | Portable, dead-simple to back up, every mail admin alive has debugged a Maildir | Directory-listing-all-the-things perf collapse past ~100k messages; Stalwart v0.16.0 dropped Maildir as a first-class backend |
| MariaDB-everywhere (messages as BLOB) | Single backup surface, SQL query access to content | Stalwart v0.16.0 **does not support SQL as a content store** — MySQL/MariaDB is a *directory* backend only; FTS extension added in v0.15 is an auxiliary index, not a primary store. Blocker. |
| FoundationDB (cluster mode) | Multi-node replication | Out of scope (plan §1 explicitly excludes cluster mode); operational cost not justified for single-node hosting |

### Webmail — options considered

| Option | Pros | Cons |
|---|---|---|
| **Bulwark Webmail 1.4.14** (`github.com/bulwarkmail/webmail`) | Purpose-built for Stalwart; JMAP-native (no IMAP from the browser); native `STALWART_FEATURES=true` integration for password change + sieve + 2FA; OAuth2/OIDC native so Kratos-as-IdP is a future drop-in; single Node service on loopback | AGPL-v3 license (trade-off below); second runtime (Node 20) alongside Go + PHP; Next.js operational surface |
| Roundcube 1.6.x | Mature, ubiquitous, PHP — no new runtime | IMAP-from-browser shape would need a per-user FPM pool mount or a shared pool; `password` plugin drives mailbox rotation via SQL directly (works, but requires the panel's own AES-GCM envelope pattern to be maintained alongside — adversarial review found this leaks plaintext on SSO) |
| SnappyMail / Rainloop | Modern UI, smaller than Roundcube | Still IMAP-from-browser; not Stalwart-aware; would need a custom SSO plugin |
| Stalwart webadmin | Ships in the Stalwart binary | Operator-facing in v0.16.0 (JMAP management UI) — not an end-user inbox |

### Why Bulwark's AGPL-v3 is acceptable

`jabali2` runs Bulwark as an **unmodified network service**. AGPL-v3's source-provision obligation applies to modifications offered to the public over a network. We don't modify Bulwark's source, and we don't combine it with panel-api at compile or link time — Bulwark is a separate process reached over HTTP. The obligation is satisfied by Bulwark's own public repo.

**If we ever fork Bulwark** (to patch a bug, to tweak a theme string in a way that requires rebuilding), the obligation to publish the forked source becomes ours. ADR-0041 calls this out so a future operator or sub-agent doesn't patch the Bulwark tree in-place without understanding the license consequence. The panel's own code is unaffected — panel-api is a separate binary talking to Bulwark over HTTP.

## Decision

### 1. Mail storage = RocksDB

All mail data — blobs, mailbox indices, FTS, bayes state, JMAP push state — lives in `/var/lib/stalwart/` (RocksDB, Stalwart's default). MariaDB stores **account identity only** (see ADR-0042). Panel never reads RocksDB directly; usage figures come from Stalwart's JMAP `Quota/get` sampled by the reconciler every 5 minutes and reflected into `mailboxes.last_usage_bytes` for the UI progress bar.

### 2. Webmail = Bulwark 1.4.14

Installed from source (`git clone --branch v1.4.14 --depth 1`, pinned via `install/bulwark.commit-sha`), built on the host with `npm ci && npm run build`, run as the `jabali-webmail` service user bound to `127.0.0.1:3000` under `jabali.slice`. nginx terminates TLS at `mail.<domain>` and proxies `/` → Bulwark, `/jmap` + `/api` + `/.well-known/jmap` → Stalwart at `127.0.0.1:8446`.

Password change flows Bulwark → Stalwart admin API directly (Bulwark's `STALWART_FEATURES=true` + `STALWART_API_URL=http://127.0.0.1:8446`). Panel is not in the loop — no callback, no shared AES-GCM envelope, no plaintext in transit through panel code.

**No panel-mediated SSO in v1.** Users sign into Bulwark with the mailbox password the panel returned once at create time. The original Kratos-cookie → signon-shim design (mirroring M7 phpMyAdmin) leaked plaintext through the shim — ADR-0042 covers why that shape doesn't work for user-owned credentials. SSO revisit lives in a future M6.1 once Bulwark's OIDC flow can be pointed at Kratos as the IdP.

### 3. License posture on Bulwark

We deploy unmodified. If anyone needs to fork, the fork lives in a public panel-owned repo with the AGPL-v3 `LICENSE` preserved and the `CONTRIBUTING` guide amended to call out the distinct license. No silent in-place patches to `/opt/jabali-webmail/` — the `install.sh install_bulwark()` idempotency check (`git rev-parse HEAD` matches `install/bulwark.commit-sha`) will catch a drifted tree and `_err` loudly.

## Consequences

### Positive

- One process per concern: Stalwart owns mail, Bulwark owns webmail UI, panel owns provisioning + DNS. No interleaved responsibilities.
- Bulwark's JMAP-native shape means the browser never speaks IMAP — cleaner TLS story, simpler nginx, fewer `Upgrade: websocket` surface area than Roundcube.
- AGPL-v3 as-deployed is a non-event; the burden only materialises if we fork.
- No panel/RocksDB coupling — Stalwart's storage format can evolve upstream without a migration wave on our side.

### Negative

- Mail backup surface is now a binary directory. The runbook must teach RocksDB checkpoint (`stalwart backup` + scheduled snapshots) as a first-class operator skill. A sloppy `rsync /var/lib/stalwart` during active writes will corrupt.
- Adding Node 20 to the runtime set means `install.sh install_node20()` enters the idempotency checklist, and panel-ui's own node requirement now has a second consumer (less stable pinning).
- Bulwark's JMAP requires `mail.<domain>` (not `<domain>`) to be CORS-accessible with an ACME cert — raises the cert count per domain from 1 to 2. Plan step 8 handles this via the existing M5 SSL path (SAN-list expansion); confirm no ACME rate-limit surprises during domain burst.
- If upstream Bulwark goes dormant, we inherit it (fork + maintain). Risk flagged; no mitigation in v1 beyond keeping the install path vendorable.

### Rejected alternatives (logged for future regret)

- **Maildir**: not supported by Stalwart v0.16.0 as a primary backend.
- **MariaDB-everywhere**: Stalwart v0.16.0 does not support SQL as a content store; the MySQL support in v0.15 added FTS as a secondary index, not a primary store. Wire-level blocker.
- **Roundcube**: post-review analysis (see plan §6) showed the SSO shim leaks plaintext. Dropping Roundcube simultaneously dropped the need for a per-user FPM mount for webmail and a separate `jabali_roundcube` DB.
- **SnappyMail / Rainloop**: not Stalwart-aware; would have required a custom SSO plugin equivalent to the (rejected) Roundcube one.
- **Stalwart webadmin as user webmail**: webadmin is operator UI, not inbox UI.

## Related

- ADR-0042 — SQL directory wiring for account identity (mailboxes table).
- ADR-0043 — Ed25519 DKIM rotation policy.
- ADR-0044 — imap-migrate deferred to M15.
- Plan: `plans/m6-email-stalwart.md` §1 (scope), §2 Step 1 (this ADR's commit).
- Bulwark upstream: `https://github.com/bulwarkmail/webmail` (pinned at `v1.4.14`).
- Stalwart upstream: `https://github.com/stalwartlabs/stalwart` (pinned at `v0.16.0`).
