# ADR-0109 — Per-domain MTA-STS (M47 Wave 7)

**Status:** Accepted (foundation; UI+reconciler in follow-up Wave 7b)
**Date:** 2026-05-20

## Context

M47 raises jabali's outbound deliverability and inbound trust posture.
**MTA-STS** (RFC 8461) is the standardized way a domain tells the
world's mail servers "I require TLS for inbound mail, and here are the
hostnames I expect mail for." Without it, an attacker who can MITM SMTP
between two MTAs can downgrade to plaintext and intercept mail
silently. Adoption is now the table-stakes baseline for any serious
hoster (Gmail, Microsoft, Outlook all publish + enforce).

A real-host spike on `.150` (mx.jabali-panel.local, Stalwart 1.0.0)
revealed that Stalwart already:

1. Implements MTA-STS server-side via its `MtaSts` singleton config.
2. Serves `/.well-known/mta-sts.txt` for any `Host: mta-sts.<domain>`
   header on its admin HTTP listener (verified: `curl -H "Host:
   mta-sts.123123.com" http://127.0.0.1:8446/.well-known/mta-sts.txt`
   returns the policy).

So the question for jabali was: **proxy nginx to Stalwart at request
time, or generate the policy file locally and serve it as a static
file?**

## Decision

**Per-domain opt-in MTA-STS** with the panel as the single source of
truth (mirroring the DNSSEC / SSL / cache toggles):

1. **Two new columns on `domains`** (migration 000141): `mta_sts_enabled
   tinyint(1)` (the on/off switch) and `mta_sts_id bigint unsigned`
   (the policy version cookie embedded in the `_mta-sts.<domain>` TXT
   record — bumped on every enable so receivers invalidate caches).
2. **Agent serves the policy file locally**, not via a proxy to
   Stalwart. The panel writes
   `/var/www/jabali-mta-sts/<domain>/.well-known/mta-sts.txt`
   containing the `version: STSv1` policy and a dedicated nginx vhost
   at `/etc/nginx/sites-available/<domain>-mta-sts.conf` listening on
   `mta-sts.<domain>:443` and serving that file. Trade-off:
   - **+** Zero dependency on Stalwart being up at request time. A
     stalwart restart won't blank MTA-STS policy fetches for every
     hosted domain (Stalwart proxy would).
   - **+** Independent reload — `mail.mtasts.apply` reloads only after
     a successful `nginx -t`, fail-safe.
   - **+** No coupling to the Stalwart admin port (which we want to
     keep loopback-only per M25 socket lockdown).
   - **−** Two sources of MTA-STS policy on the host (Stalwart's
     singleton + jabali's per-domain files). For jabali single-tenant
     mail, Stalwart's singleton serves the same policy via its own
     port (8446) and is reachable internally for testing but isn't
     wired to a public hostname; jabali's nginx vhost is the
     internet-facing path. **Documented divergence acceptable**; a
     future revision can sync Stalwart's singleton via the planned
     `internal/stalwartadmin` client.
3. **Agent commands** `mail.mtasts.apply` and `mail.mtasts.disable`
   are idempotent — re-running on every reconciler tick resets any
   operator hand-edits, which is the DB-as-truth point. Strict input
   validation: FQDN regex on `domain` and `mx_host`, enum
   restriction on `mode` (enforce/testing/none), RFC-8461 range
   on `max_age` (86400..31557600), absolute-path guard on
   `ssl_cert`/`ssl_key`.
4. **HTTPS-only vhost** — no `:80` listener on the mta-sts vhost. RFC
   8461 §3.3 mandates TLS with a CA-signed cert, so serving plain
   HTTP at all is a configuration smell. A receiver that hits HTTP
   gets a connection refused, which is the correct signal.
5. **SAN inclusion via `sanHostnamesForDomain`** — when
   `mta_sts_enabled` flips on, the existing SSL reconciler picks up
   `mta-sts.<domain>` as a new SAN and ACME re-issues. No new cert
   pipeline.

## Wire shapes

```go
// mail.mtasts.apply params
type mtaStsApplyParams struct {
    Domain  string `json:"domain"`
    MXHost  string `json:"mx_host"`   // published mx: value
    Mode    string `json:"mode"`      // enforce | testing | none
    MaxAge  int    `json:"max_age"`   // seconds, 86400..31557600
    SSLCert string `json:"ssl_cert"`  // absolute path
    SSLKey  string `json:"ssl_key"`
}

// mail.mtasts.disable params
type mtaStsDisableParams struct {
    Domain string `json:"domain"`
}
```

Policy file body (RFC 8461 §3.2):
```
version: STSv1
mode: testing
max_age: 604800
mx: mx.example.com
```

## Out of scope (Wave 7b follow-up)

- **API handler** `POST /domains/:id/mta-sts` + `GET` — added in
  Wave 7b alongside the reconciler step that calls `mail.mtasts.apply`
  on toggle change.
- **Auto DNS records** — `_mta-sts.<domain> TXT "v=STSv1; id=<id>"`
  and `mta-sts.<domain> A <ip>` records added via dnscompile in
  Wave 7b (managed_by="mta-sts" marker so disable can clean up).
- **UI section** `DomainMTASTSSection.tsx` mounts in `DomainEdit`
  in Wave 7b (single Switch + status row, mirrors `DomainSSLSection`).
- **TLS-RPT** (`_smtp._tls.<domain>` reporting) is a separate feature
  (Wave 8) that pipes into the planned Stalwart-admin report-poll
  ingest source.
- **Stalwart `MtaSts` singleton sync** — defer to when the
  `internal/stalwartadmin` client lands so we don't fork a one-off
  HTTP-Basic client just for this one POST.

## Companion findings

- `project_stalwart_mtasts_well_known_native` — pinned that Stalwart
  serves the policy natively for any matching `Host` header.
- `project_stalwart_native_report_storage` — the parent architectural
  re-think driving the Wave 4/6/8 stalwartadmin client.
- `project_stalwart_mtaouthound_throttle_pin` — the Wave 3 wire pin
  from the same `.150` spike.

## Verification

- `validateMTAStsApply` unit test covers 9 reject cases + the happy
  path (domain regex, mx host regex, mode enum, max_age range, cert
  path absolutism, traversal guard).
- `renderMTAStsPolicy` test pins the exact wire-shape RFC 8461 §3.2
  expects.
- `renderMTAStsVhost` test asserts the vhost contains `server_name
  mta-sts.<domain>`, both `listen 443 ssl http2` lines, the cert
  paths, and the `location = /.well-known/mta-sts.txt` block; AND
  refuses to listen on `:80`.
- Live verification deferred to Wave 7b's reconciler-driven end-to-end
  flow on `.150` (mx.jabali-panel.local).
