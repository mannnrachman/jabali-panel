# ADR-0064: Diagnostic report — age-encrypted, redacted

**Status:** ACCEPTED (2026-04-25)
**Related:** M29 admin updates + support page.

## Context

Operators reporting bugs need to share host state (service status, journal
tails, package versions, network listeners) but must not paste raw logs in
public. We want a one-click flow on `/jabali-admin/support` that produces a
ciphertext blob the operator copies into a GitHub issue. Only the Jabali
team can decrypt it.

## Decisions

### 1. Encrypt with `age`, not PGP

- Single self-contained Go dependency (`filippo.io/age` v1.3.1).
- No GnuPG keyring, agent, or web-of-trust on the operator host.
- Text-armoured output is paste-friendly.
- The recipient is a static team-owned X25519 pubkey baked into the agent
  binary at build time. No per-install keys, no enrollment dance.

PGP rejected: GnuPG ships with too many sharp edges (keyserver lookups,
trust DB, agent-socket leakage) and CI tooling for it is opaque.

### 2. Redact BEFORE encrypting

This is the load-bearing decision and the reason this ADR exists.

Encryption-to-team is not a license to ship raw secrets. The ciphertext
lives forever in a GitHub issue: anyone who scrapes the public timeline
keeps a copy. If the team's private key ever leaks (laptop loss, password
manager compromise, employee turnover with key extraction), every past
diagnostic report retroactively becomes a credential dump — DSN
passwords, Kratos session tokens, Bearer headers, all back-readable.

`internal/diagnostic/redact.go` runs every collected file through a
deny-list of regexes BEFORE the bytes ever reach `age.Encrypt`. Patterns
covered:

- `password=…`, `--password=…`
- `mysql|postgres|redis://user:PASSWORD@…` (keep user, strip pass)
- `Authorization: …`, `Bearer …`, `Cookie: …`
- `ory_kratos_session=…`
- `(?i)token|secret|api_key|authorization` key-value forms

The wire response includes a `redaction_count` so the operator sees how
many secrets were stripped. If the count is zero on a populated install,
that's a signal a new secret format slipped past — file a follow-up to
extend the deny-list.

Tradeoff: the redactor is a deny-list, not an allow-list. Unknown secret
formats can leak. The alternative (allow-list) loses too much debug
signal — most lines of journalctl are not credentials, and over-stripping
makes reports useless. Lean toward "log enough to debug + know the deny
list catches the common cases" and extend on demand.

### 3. Hard-coded recipient public key, recompile to rotate

The pubkey lives in `internal/diagnostic/recipient.go` as a `const`.
Rotation requires:

1. Generate new X25519 identity (`age-keygen`) off-host. Store the new
   private key in the team's password manager alongside any prior keys
   (we keep all old privates so old reports stay decryptable).
2. Bump the `RecipientPublicKey` constant.
3. Cut a release; operators get the new pubkey via `jabali update`.

The hot-fix window between "key compromised" and "all installs updated"
is roughly the time it takes ops to run `jabali update` everywhere. For
managed hosts that's hours, not days.

### 4. Off-host private key custody

The matching private key NEVER appears on:

- Any managed Jabali host
- The build machine that compiles `jabali-agent`
- Any CI runner

It lives only in the team's password manager. To decrypt a reported
ciphertext: paste it into `age -d -i ~/.jabali-team-priv.txt > bundle.tar`
on a team laptop, then `tar -xf bundle.tar`.

### 5. Fixed service collection list

`servicesToCollect` in `diagnostic.go` enumerates the systemd units we
journal-tail. Hard-coded so a malicious caller can't request `journalctl
-u arbitrary.service` through this path. To add a service: edit the
list, ship via release.

### 6. tar-then-encrypt, not file-by-file

Bundle is a single in-memory tar archive of redacted text files;
encrypted as one stream. Operator gets one ciphertext, decrypts to one
tar, untars to view. Simpler than N ciphertexts; smaller than N envelopes.

## Consequences

- **Pro:** simple operator UX (click button, copy output, paste in GitHub).
- **Pro:** no host-side credentials for diagnostics path.
- **Pro:** redaction count visible to the operator builds trust.
- **Con:** deny-list redactor will miss novel secret formats. Mitigation:
  log every redaction count; investigate when a known-noisy install
  reports zero.
- **Con:** rotating the recipient requires a release. Acceptable — key
  compromise is the only forcing function and an emergency release is
  hours.

## Verification

- `go test ./panel-agent/internal/diagnostic/...` covers redactor
  patterns + age encrypt/decrypt round-trip.
- Agent boot-time validation: `age.ParseX25519Recipient` is called on
  every `system.diagnostic_report` invocation; a malformed constant
  surfaces as a 502 immediately rather than silently producing
  un-decryptable bundles.

## Open

- The placeholder recipient `age13trnrev8dmdva5tsjnhnmdrlpnukl5d47rjerv72jem90ucqtyasdzwts0`
  is a freshly-generated test key. Before public release the team must
  generate a long-lived recipient, custody the private half, and swap
  the constant. Tracked in plans/m29-admin-updates-support-runbook.md.
