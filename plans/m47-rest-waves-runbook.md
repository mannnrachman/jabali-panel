# M47 Waves 4 / 6-ingest / 8 / 9 — Runbook

ADR-0110.

## What this ships

| Wave | Source / endpoint                                       | Stalwart object       | Persists into          |
|------|---------------------------------------------------------|-----------------------|------------------------|
| 4    | `eventsources/mail_abuse_ingest.go`                     | `ArfExternalReport`   | `arf_report`           |
| 6    | `eventsources/mail_dmarc_ingest.go` (was: file-parser)  | `DmarcExternalReport` | `dmarc_aggregate`      |
| 8    | `eventsources/mail_tlsrpt_ingest.go`                    | `TlsExternalReport`   | `tlsrpt_aggregate`     |
| 9    | `GET /api/v1/admin/mail/deliverability`                 | aggregates all 4      | (read-only score)      |

Each ingest source polls Stalwart admin REST every 5 minutes via the
`stalwart-cli` subprocess (see `internal/stalwartadmin`). M14 event
dispatch fires on every imported report (severity rules in ADR-0110).

## Live-host smoke (recommended on `.150` first)

1. **Verify stalwart-cli is reachable from panel-api:**
   ```bash
   sudo -u jabali /usr/local/bin/stalwart-cli \
     --url http://127.0.0.1:8446 \
     --user admin \
     --password "$(awk -F: '/STALWART_RECOVERY_ADMIN=/{print $2$3}' /etc/jabali-panel/stalwart.env)" \
     query DmarcExternalReport --json | head -c 200
   ```
   Should print `[]` (no reports yet) OR an array.

2. **Watch panel-api logs for ingest passes:**
   ```bash
   journalctl -u jabali-panel -f | grep -E "dmarc-ingest|tlsrpt-ingest|abuse-ingest"
   ```
   First lines should appear within 5 min of panel-api start; each
   pass logs whether stalwart query succeeded and how many rows it
   imported.

3. **Seed a test DMARC report** (manual, since real DMARC takes 24h+
   to land via DNS publication). Drop a sample RUA XML into the
   operator's report mailbox (`postmaster@<panel-hostname>`) via IMAP
   APPEND; Stalwart will parse and expose it as a
   `DmarcExternalReport` within seconds; the next ingest pass picks
   it up.

4. **Hit the score endpoint:**
   ```bash
   curl -sk -H "Authorization: Bearer <admin-access-token>" \
     https://<panel-hostname>/api/v1/admin/mail/deliverability | jq
   ```
   Returns `{"score": 100, "severity": "ok", "components": [...]}`.

5. **Open the UI:** `/jabali-admin/mail/deliverability` — circular
   progress + per-component breakdown.

## Failure modes + recovery

| Symptom                                                                  | Likely cause                                                      | Fix                                                                                  |
|--------------------------------------------------------------------------|-------------------------------------------------------------------|--------------------------------------------------------------------------------------|
| Ingest logs `stalwart query failed: authentication failed (HTTP 401)`    | `STALWART_RECOVERY_ADMIN` rotated; panel-api didn't reload it    | `systemctl restart jabali-panel` (env file is read at boot).                          |
| Ingest logs `stalwart query failed: dial tcp 127.0.0.1:8446: refused`    | Stalwart down (`jabali-stalwart` failed)                          | `systemctl status jabali-stalwart`; `journalctl -u jabali-stalwart -n 50`.            |
| Score endpoint returns 100 forever despite real RBL listings              | `mail_rbl_state` empty; the RBL probe (Wave 5) hasn't run yet     | Wave 5 ticks every 4h — wait one tick or restart the panel to force an immediate pass. |
| `arf_report` rows growing without bound                                  | Retention pruner not yet wired (TODO: Wave 9b)                    | `mysql -e 'DELETE FROM arf_report WHERE received_at < NOW() - INTERVAL 90 DAY;'`     |
| Same DMARC report imported twice                                          | Cursor slack (1h) overlapping with a slow report                  | `ExistsForReport` gates on `(reporter, window_start, window_end)`; duplicates ignored. |

## Operational notes

- Each ingest goroutine self-disables when its repo + stalwart client
  aren't both wired. A panel running without Stalwart (admin-only
  install) sees `eventsources: mail_*_ingest disabled` at debug level
  and the goroutine never starts.
- The cursor is a `SELECT MAX(window_end)` (or `received_at`) on
  every pass — index-covered, sub-millisecond.
- Wave 9 score is server-wide; per-domain breakdown is a follow-up.
- TLS-RPT failure counts ignore the per-policy domain split in the
  current widget (deliberately simple); a per-domain UI is the
  obvious Wave 9b.
