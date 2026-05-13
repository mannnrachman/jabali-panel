# ADR-0073 — Stalwart alias resolution + apply-plan schema evolution

Status: Accepted
Date: 2026-04-27
Supersedes: nothing
Amends: ADR-0045 (Stalwart bootstrap), ADR-0051 (M6.5 DB-as-truth)

## Context

VM smoke on `mx.jabali-panel.com` ( 2026-04-27) confirmed
the M6.5 forwarder/alias feature ships through to the database
(`email_forwarders` row created on `jabali mailbox forwarder add`) but
is never observed by Stalwart. Sending to an alias returns
`550 5.1.2 Mailbox does not exist`.

Two findings drove this ADR:

1. **Two SqlDirectory queries needed patching, not one.**
   `queryEmailAliases` only populates an Account's `aliases` list
   (visible in `stalwart-cli get Account`); SMTP RCPT acceptance is
   independently gated by `queryRecipient`. Even with aliases on the
   account, `queryRecipient` rejected the alias address because it
   only matched `email_cached`. Both queries need alias-aware SQL.

2. **`@type: create` is not idempotent.** Re-running an apply-plan
   that creates `x:Directory` produces a *duplicate* Directory each
   run because Stalwart auto-generates an id and there's no
   name-based dedup. The previous `skip_apply` guard in install.sh
   silently relied on this — but it also meant any change to the
   directory's query fields could never reach existing hosts.

## Decision

1. **`queryRecipient` resolves aliases inline.** A derived-table
   pattern keeps the prepared statement to a single `?` while
   accepting either the canonical email or any alias address:

   ```sql
   SELECT m.email_cached, m.password_hash
   FROM (SELECT ? AS lookup) input
   JOIN mailboxes m
     ON m.is_disabled = 0
    AND (m.email_cached = input.lookup
         OR m.email_cached = (
           SELECT f.target FROM email_forwarders f
           JOIN domains d ON d.id = f.domain_id
           WHERE f.enabled = 1 AND f.type = 'alias'
             AND CONCAT(f.local_part, '@', d.name) = input.lookup
           LIMIT 1
         ))
   ```

2. **`queryEmailAliases` returns aliases owned by the canonical
   principal.** Input is the canonical email, output is each alias
   address. This is the direction Stalwart's `synchronize_account`
   expects:

   ```sql
   SELECT CONCAT(f.local_part, '@', d.name) AS alias
   FROM email_forwarders f
   JOIN domains   d ON d.id = f.domain_id
   JOIN mailboxes m ON m.id = f.mailbox_id
   WHERE f.enabled = 1 AND f.type = 'alias'
     AND m.email_cached = ?
   ```

3. **Schema evolution converges via post-apply `stalwart-cli update`,
   not via in-plan `update` steps.** install.sh:
   - keeps the `skip_apply` guard (re-running `apply` would create a
     duplicate Directory)
   - adds a separate convergence step that runs unconditionally:
     queries the live SQL Directory id, then patches both query fields
     via `stalwart-cli update Directory <id> --json '{...}'`. The id is
     resolved each run; no template-baked id is needed.

4. **`SELECT` grant on `email_forwarders`** added to the `jabali-stalwart-ro`
   user alongside the existing `mailboxes` + `domains` grants.

## Consequences

- Forwarders + aliases land in Stalwart on the next `jabali update` of
  every existing host — no manual stalwart-cli intervention.
- Future schema evolution of any apply-plan field (Stalwart-side
  Directory, Authentication, etc.) follows the same recipe: convergence
  in install.sh post-apply, not in the plan itself, because the plan
  has no name-based upsert.
- Disclaimer remains broken per ADR-0052 (deferred outbound transform
  mechanism, separate fix).

## Verification

VM smoke transcript on mx post-fix:

```
jabali mailbox forwarder add bob@123123.com --type alias --local sales
python3 sendmail.py alice@... sales@123123.com "alias retry 4" "..." Secret-Alice-1
→ OK
python3 imap.py bob@123123.com Secret-Bob-1
→ INBOX msgs: 1
→ Subject: alias retry 4
```

## References

- ADR-0045 — Stalwart v0.16 RocksDB + apply-plan bootstrap
- ADR-0051 — M6.5 DB-as-truth for mail features
- ADR-0052 — Disclaimer HTML deferred (still open)
