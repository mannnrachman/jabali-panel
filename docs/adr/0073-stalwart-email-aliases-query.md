# ADR-0073 — Stalwart `queryEmailAliases` + apply-plan schema evolution

Status: Accepted
Date: 2026-04-27
Supersedes: nothing
Amends: ADR-0045 (Stalwart bootstrap), ADR-0051 (M6.5 DB-as-truth)

## Context

VM smoke on `192.168.100.150` (2026-04-27, ssh root@…) confirmed the
M6.5 forwarder/alias feature ships through to the database (`email_forwarders`
row created on `jabali mailbox forwarder add`) but is never observed by
Stalwart. Sending to an alias address returns `550 5.1.2 Mailbox does
not exist`.

Root cause: `install/stalwart/apply-plan.json.tmpl` shipped with
`queryEmailAliases: null`. Stalwart's `SqlDirectory` only consults the
field when non-null; null means "no alias resolution at all". The
`email_forwarders` table has rows nobody reads.

Secondary issue: `install.sh` skipped re-applying the plan when Stalwart
was already up on `:8446` (post-bootstrap port). The skip is a perf opt
predating any schema evolution, and meant existing installs would never
pick up template changes — even if we corrected the SQL, no operator
running `jabali update` would see the fix.

## Decision

1. **Add `queryEmailAliases`** to the `x:Directory` create step in
   `apply-plan.json.tmpl`. The SQL handles both M6.5 forwarder types
   (`alias` and `external`) in one statement using `COALESCE` so the
   prepared statement takes a single parameter (the lookup email):

   ```sql
   SELECT f.target FROM email_forwarders f
   LEFT JOIN domains   d ON d.id = f.domain_id
   LEFT JOIN mailboxes m ON m.id = f.mailbox_id
   WHERE f.enabled = 1
     AND COALESCE(
       CASE WHEN f.type = 'alias'    THEN CONCAT(f.local_part, '@', d.name) END,
       CASE WHEN f.type = 'external' THEN m.email_cached END
     ) = ?
   ```

   Single `?` matches Stalwart's `MySql` store binding semantics (one
   bound parameter per `?`).

2. **Add an explicit `update` step** for `x:Directory id="sql-directory"`
   that re-applies `queryEmailAliases` on every plan run. Reason: the
   `create` step `primaryKeyViolation`s on existing installs (the
   directory already exists), so its newly-corrected fields would be
   silently dropped. The `update` step converges the field on every
   run regardless of bootstrap state. This is the canonical pattern
   for any future `apply-plan` schema evolution: add fields to `create`
   for fresh installs, add a paired `update` for existing installs.

3. **Lift the `skip_apply` guard** in `install.sh`. Apply runs on every
   install/update pass; `--continue-on-error` + the
   `primaryKeyViolation` filter handles the no-op create steps as
   before. Trade-off accepted: every `jabali update` spends a few
   seconds running a mostly-no-op apply instead of skipping — worth it
   for the hands-off schema-evolution path.

4. **Grant `SELECT` on `email_forwarders`** to the read-only Stalwart
   MariaDB user `jabali-stalwart-ro`. The grant block already covers
   `mailboxes` + `domains`; add `email_forwarders` to the same `GRANT`
   statement so the new query has access.

## Consequences

- Forwarders + aliases land in Stalwart on the next `jabali update` of
  every existing host. No manual stalwart-cli intervention needed.
- The pattern (`create` field + paired `update` step) is now the
  template for every M6.5+ feature whose Stalwart-side wiring evolves
  over time.
- **Does NOT cover the disclaimer feature** — disclaimer remains
  deferred per ADR-0052 (the SMTP-side outbound transform is a separate
  Stalwart milter / Sieve mechanism, not a SqlDirectory query). The VM
  smoke also confirmed disclaimer text never reaches outbound; that's
  expected pre-ADR-0052-resolution behavior, not a regression.

## Verification

VM smoke on `192.168.100.150` after `jabali update`:

```bash
jabali mailbox forwarder add bob@123123.com --type alias --local sales
python3 /tmp/sendmail.py alice@123123.com sales@123123.com "smoke" "..." Secret-Alice-1
python3 /tmp/imapcheck.py bob@123123.com Secret-Bob-1   # should show alias message in INBOX
```

## References

- Smoke transcript: this session 2026-04-27
- ADR-0045 — Stalwart v0.16 RocksDB + apply-plan bootstrap
- ADR-0051 — M6.5 DB-as-truth for mail features
- ADR-0052 — Disclaimer HTML deferred (still open)
