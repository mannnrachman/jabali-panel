# ADR-0079 — Mail YARA Scanner (M33.2): async, JMAP-driven, in-process

**Status:** ACCEPTED  
**Date:** 2026-04-28  
**Supersedes/extends:** ADR-0072 (M33 malware detection stack)

## Context

Mail team flagged a gap in M33: the filesystem scanner (maldet + yara-x +
signature-base + Tetragon) catches webshells written into user docroots, but
not webshells **mailed as attachments to admin@ or tenant inboxes**. For a
hosting panel that's a real ingress vector — a sysadmin who downloads + uploads
a sample is the classic foothold.

Three integration paths were evaluated:

- **A. Stalwart MTA hook (HTTP/JSON, real-time at SMTP DATA)** — Stalwart's
  reqwest-based hook client refuses `unix:` URL schemes (verified upstream:
  `crates/smtp/src/inbound/hooks/client.rs`). Real-time blocking would force
  panel-api to bind a TCP loopback port for hooks-only traffic, violating
  M25's unix-socket-only posture. ~350 LOC + new daemon surface.
- **B. Stalwart milter (binary protocol)** — Same TCP-only constraint
  (`crates/smtp/src/inbound/milter/client.rs` uses `TcpStream`). Brittle
  binary protocol with one Go ecosystem implementation.
- **C. Sieve header-only filter** — Trivial to bypass via `.docx` rename.
  Doesn't deliver actual YARA scanning the mail team asked for.
- **D. Async post-delivery scan via JMAP** — Walks Stalwart mailboxes every
  5 min, scans attachments offline, moves hits to a per-account "Malware"
  mailbox. Zero SMTP-time latency, zero new daemon, zero TCP loopback. Threat
  model fit: webshell-in-mail requires human-in-loop (open + download + upload),
  so async tag-and-quarantine arrives BEFORE the operator can act.

**D wins.** ROI is highest, blast radius lowest.

## Decision

1. **Reconciler tick inside panel-api.** Pattern parallel to M5 SSL renewal
   reconciler + M6.5 vhost reconciler. Standalone goroutine started from
   `serve.go`; `mailscan.StartTicker` runs every 5 min, no-ops when
   `malware_settings.mail_scan_enabled = false`.

2. **Auth = stalwart admin token.** `/etc/jabali-panel/stalwart-admin.token`
   is `0640 jabali:jabali-mail`. panel-api runs as `jabali` (file owner),
   reads via owner-mode bit. **No group changes, no new user, no install.sh
   identity diff.**

3. **JMAP-only.** No direct RocksDB reads — Stalwart upgrades break the
   on-disk schema; JMAP is the contract.

4. **Multi-tenant via `Principal/query`.** Admin token sees its own account
   in `/jmap/session.accounts`; full tenant list comes from the
   `urn:ietf:params:jmap:principals` capability. Cross-account access is
   the standard JMAP `accountId` field on each method call (admin
   credentials grant cross-account read+write).

5. **Per-account quarantine mailbox = "Malware".** Lazy-created via
   `Mailbox/set` on first hit; cached id in `mail_scan_state.quarantine_mailbox`
   with `quarantine_mailbox_verified` revalidation timestamp (24h cache).
   On `Mailbox/get` notFound, re-create. **NOT reused with Spam/Junk** —
   spam-classification ≠ malware-detection; user-side semantics differ
   (Junk is auto-purged at 30d, malware quarantine should not be).

6. **YARA engine = yara-x (`yr`).** Same rule sources as the filesystem
   scanner: `rfxn.yara` + `signature-base/yara/` + `/etc/jabali/yara/`.
   Subprocess invocation with per-attachment timeout (default 10s).

7. **Reuses M33 ingest path.** Hits POST through
   `api.IngestMalwareEventInProcess` (extracted from the loopback `/event`
   handler) with `source=yara`, `event_type=file_hit`, `severity=warn`.
   Same M14 notification channels, same admin Quarantine table, same Recent
   Events filter. **Zero new EventKind.**

8. **DLQ + concurrency lock + per-tick budget.** `mail_scan_failures` table
   captures yr-exec failures, JMAP 5xx, blob-fetch errors. flock at
   `/run/jabali/mail-scan.lock` makes the ticker re-entrant-safe. Per-tick
   budget caps mailboxes scanned per cycle (default 200) — round-robin via
   `scanned_at ASC` ordering means a 1000-mailbox host converges across
   five ticks rather than starving newcomers.

9. **No per-mailbox blanket whitelist.** Earlier draft proposed
   `admin@<panel-host>` skip; rejected — admin@ is THE target. Sysadmin
   sample-analysis workflow uses `mail_scan_skip_addresses` (per-address
   skip list) so an explicit `samples@` mailbox stays unblocked while
   admin@ remains scanned.

## Consequences

- New tables: `mail_scan_state`, `mail_scan_failures` (migration 000092).
- `malware_settings` extends with 6 new `mail_scan_*` columns.
- New package `panel-api/internal/mailscan/` (~450 LOC: JMAP client copy,
  yr scanner, tick orchestrator).
- Two JMAP clients in the tree pending consolidation — one in
  `panel-agent/internal/commands/mailbox_jmap.go` (M6 mailbox CRUD), one
  in `mailscan/client.go` (this ADR). Both target Stalwart 0.16. Drift is
  caught by integration tests + the live-VM smoke. Consolidate post-M33.2
  into a top-level `internal/jmap` package.
- Threat-model coverage: webshell attachments, EICAR, known macro droppers
  in mail. Zero coverage of phishing URLs (Stalwart Bayes already covers).
- Performance budget: ~250ms per attachment (download + yr scan); 200
  mailboxes × 1 attachment avg = ~50s per tick on a slow host. Fits inside
  5-min cadence with margin.

## Out of scope

- Stalwart milter / MTA-hook integration (Path A/B above).
- Outbound mail scanning at submission stage.
- Header rewriting (JMAP doesn't expose post-delivery header mutation).
- Real-time eventsource push (we poll; future work could subscribe to
  `/jmap/eventsource/?types=Email`).
