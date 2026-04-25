# ADR-0064: Diagnostic report — enclosed.cc upload + email delivery

**Status:** ACCEPTED (2026-04-25). Amended same-day twice:
1. Replaced age-encryption-to-static-recipient with enclosed.cc E2E upload.
2. Replaced ntfy push delivery with mailto: hand-off
   (ntfy required server-side Bearer-token plumbing; email is universal
   and the operator-to-team channel everyone already has).

**Related:** M29 admin updates + support page.

## Context

Operators reporting a bug need to share host state (service status,
journal tails, package versions, network listeners) without pasting raw
logs in public. We want a one-click flow on `/jabali-admin/support`
that produces a shareable encrypted note + a one-click "send to support
team" notification.

## Decisions

### 1. Upload to a Jabali-team-controlled `enclosed.cc` instance

Self-hosted at `https://enclosed.jabali-panel.com`. enclosed is end-to-
end encrypted: the server only sees ciphertext, the password lives in
the URL fragment, and the server never knows whether decryption
succeeds. This sidesteps the trust model trap of the original ADR-0064
plan (single static `age` recipient that, if compromised, retroactively
exposes every past report).

URL form (from `packages/lib/src/notes/notes.models.ts`):

```
https://enclosed.jabali-panel.com/<noteId>#pw:<base64url(baseKey)>
```

The `pw:` prefix tells the JS client this note is password-protected; a
human-typed password (returned to the operator alongside the URL) joins
the baseKey in PBKDF2 to derive the AES-GCM master key.

### 2. Re-implement the enclosed crypto pipeline in Go

`panel-agent/internal/enclosed/client.go` implements the same protocol
the JS lib uses, end to end:

- `baseKey` = 32 random bytes
- `password` = 20 random bytes, base64url-encoded
- `masterKey` = `pbkdf2(baseKey || password_utf8, salt=baseKey, 100_000, sha256, 32)`
- `iv` = 12 random bytes
- `ciphertext` = `AES-256-GCM(masterKey, iv, plaintext)` (auth tag concatenated)
- `payload` = `base64url(iv) + ":" + base64url(ciphertext_with_tag)`
- POST `{payload, deleteAfterReading: false, encryptionAlgorithm: "aes-256-gcm", serializationFormat: "cbor-array", isPublic: true, ttlInSeconds: 7*86400}` to `/api/notes`
- response `{noteId}` → URL constructed as above

The `cbor-array` serialization wraps the diagnostic tar as:

```
encode([content_string, [[metadata_map, asset_bytes]]])
```

Single asset: the redacted tar with metadata `{type: "file", name:
<host>-<ts>.tar, fileType: "application/x-tar", size: N}`.

Source reference: `github.com/CorentinTh/enclosed/packages/{lib,crypto}`.

### 3. Redaction is still mandatory

Even though enclosed encrypts end-to-end, the team's static
team-shared private key (well, the password the operator generates +
team retrieves via email) lives in chat logs and password managers
forever. Defense in depth: strip credentials BEFORE encryption.

`internal/diagnostic/redact.go` runs the bundle through a regex
deny-list (passwords, Bearer tokens, Cookie headers, ory_kratos_session,
DSN passwords, api_key/secret/token forms). The wire response includes
a `redaction_count` so the operator sees how many secrets were stripped.

### 4. mailto: delivery for the operator → team hand-off

The UI builds a `mailto:webmaster@jabali-panel.com` link client-side
when the operator clicks "Send via email" in the modal. The mail body
contains hostname, the enclosed link, the password, generation
metadata, and any free-text note the operator typed. The operator's
own mail client takes over the send.

Why mailto over ntfy / a server-side webhook: every operator already
has a configured mail client. ntfy gates publishing on a Bearer token
that would have to be provisioned per-install via systemd drop-ins
(operational overhead with no upside vs the universal "click button,
review email, hit send" flow).

Why two-step (mint URL, then explicit click to send): the operator
decides whether the case warrants a hand-off. Auto-sending on mint would
spam the team for every "let me try the diagnostic button" moment.

### 5. 7-day TTL on enclosed notes

`ttlInSeconds: 7 * 86400`. Long enough for the team to read at their
leisure; short enough that stale ciphertext (with stale credentials in
the redacted-but-still-might-leak-something log lines) rotates out
without manual cleanup.

### 6. Hard-coded URL, env override for dev

`defaultEnclosedBaseURL = "https://enclosed.jabali-panel.com"` is baked
into the agent binary. Override via `JABALI_ENCLOSED_URL` for dev. The
mail recipient lives on the UI side as
`DIAGNOSTIC_EMAIL_RECIPIENT = "webmaster@jabali-panel.com"` in
`panel-ui/src/config/support-links.ts`.

## Consequences

- **Pro:** no shared static private key for the agent to hold. Every
  diagnostic gets a fresh password; compromise of one report doesn't
  compromise the others.
- **Pro:** standard protocol means we can read reports via the
  enclosed web UI in any browser — no `age` CLI required.
- **Pro:** mail hand-off uses a channel every operator already has
  configured. No tokens, no per-host plumbing.
- **Con:** end-to-end-encryption depends on the operator faithfully
  forwarding the password. If they paste only the URL, decryption
  fails. Modal copy buttons + the prefilled mailto: body include both
  fields to reduce friction.
- **Con:** trust shifts to the enclosed deployment. Server compromise
  could replace the JS client with a key-stealer; at that point any
  user pasting the URL+password into the page would leak both. Mitigated
  by hosting enclosed on a controlled VPS with auto-updates + read-only
  rootfs (out of scope for this ADR; tracked separately).

## Verification

- `go test ./panel-agent/internal/enclosed/...` covers AES-GCM round-trip,
  PBKDF2 derivation matching the JS formula, and CBOR shape.
- `go test ./panel-agent/internal/diagnostic/...` covers redaction
  patterns + tar packing.
- Live VM verification: agent uploads a real bundle to
  `https://enclosed.jabali-panel.com`, returns a URL that opens cleanly
  in a browser when password is entered.

## Superseded

The original ADR-0064 (age-encrypted bundle to a static team recipient)
is superseded. Code that referenced `RecipientPublicKey` or
`filippo.io/age` was removed in the same commit that introduced the
enclosed client.
