# ADR-0048: Panel hostname as primary mail domain

**Date**: 2026-04-22
**Status**: accepted
**Deciders**: shuki + Claude
**Related**: ADR-0041 (M6 mail storage), ADR-0042 (SQL directory), ADR-0047 (pdns-recursor local self-resolution — prerequisite for this ADR's `/webmail` UX), ADR-0002 (DB as source of truth)

## Context

M6 shipped Stalwart + Bulwark as the panel's mail stack. M6.1 extended per-tenant certs to cover `mail.<domain>`. M6.2 bridged the panel SSO into Bulwark. M6.3 made the panel resolve its own zones locally so `mail.<domain>` stops round-tripping to public resolvers.

With all four shipped, a fresh install still has one conspicuous UX hole: the **panel hostname** (the hostname the operator sets at install time, reached at `https://<panel-hostname>:8443/jabali-admin/`) is **not a mail domain**. That means:

- `mail.<panel-hostname>` doesn't resolve locally (no `mail` A record in the self-zone).
- The panel's self-signed cert has no `mail.<panel-hostname>` SAN (install.sh issues it for the apex only).
- No nginx mail vhost exists for `mail.<panel-hostname>` (the per-domain webmail vhost only fires for tenant domains with `email_enabled=1`).
- No `domains` row with `name = <panel-hostname>` and `email_enabled=1`, so the reconciler never provisions DKIM, MX, SPF, or DMARC records for it.
- Consequence: `admin@<panel-hostname>` cannot receive mail. The operator has no mailbox on day one. `{panel-hostname}/webmail` either 404s or redirects to a tenant domain's webmail — confusing at best, broken at worst.

The obvious fix — "ask the operator to register the panel hostname as a tenant domain during install and enable email on it" — is a documentation burden nobody reads. The ergonomic fix is to auto-register the panel hostname as a first-class email domain, leaning on the full M6/M6.1/M6.3 code paths that already exist.

That reframes the work as a set of **four design decisions**, each with its own rejected alternatives. ADR-0048 records those decisions; M6.4's plan (`plans/m6.4-panel-hostname-mail-domain.md`) implements them.

## Decisions

### 1. Auto-register the panel hostname as an email-enabled domain at install time

**Decision**: `install.sh` inserts a `domains` row for `$JABALI_SRV_HOSTNAME` with `email_enabled=1, is_panel_primary=1` on every fresh install (and updates in place on hostname change). The reconciler then takes over via the existing M6 email-enable code path — DKIM keypair generation, Stalwart domain add, nginx mail vhost, MX/SPF/DKIM/DMARC TXT rows in the M6.3 self-zone.

**Why not "require the operator to register the panel hostname as a tenant domain and enable email manually"?**

- Documentation-based UX requires the operator to (a) know they need to do this, (b) do it correctly, (c) not forget. All three fail in the real world. The product impression "webmail doesn't work on fresh install" erodes trust faster than any other ergonomic issue — users draw the conclusion that the mail feature is unfinished, even when every backend piece is working.
- The phpMyAdmin precedent argues the other way: phpMyAdmin is automatically reachable at `https://<panel-hostname>/phpmyadmin/` on every install with zero operator action. Databases are "batteries included"; mail should be too.
- The marker column (Decision 2) makes the auto-registered row trivially distinguishable from operator-created tenant domains, so the "surprise" concern (operator sees an unexpected domain in their list) is mitigated with a "System" tag in the UI.

**Why not "install.sh prompts the operator at interactive install and skips on `--yes` flag"?**

- Adds install-flow branching for every fresh install to handle a choice that 95% of operators will answer the same way. Non-interactive installs (CI, CloudInit, bulk deployment) would get a different default than interactive, which is a surprise nobody wants.

### 2. Mark the auto-registered row with a boolean column `is_panel_primary`, not a file pointer or enum

**Decision**: new column `domains.is_panel_primary TINYINT(1) NOT NULL DEFAULT 0`. At-most-one enforced in the Go repository layer (`MarkPanelPrimary` transaction clearing any other `=1` row to 0 before setting target), NOT via a SQL `UNIQUE` constraint.

**Why not "enum `domain_type` with values `tenant` / `panel_primary` / possibly future kinds"?**

- Enums in MariaDB are rigid — adding a new value requires a migration. The bet that we'll need more categories (shared-hosting-plan domain? system-internal domain?) is speculative. When that need actually arises, adding another boolean column (or promoting to an enum then) is trivial; committing to an enum today is premature abstraction.
- Boolean filters trivially in SQL (`WHERE is_panel_primary = 1`); enum filtering adds a string comparison layer for no current gain.

**Why not "a file pointer at `/etc/jabali-panel/primary-domain.id` holding the domain ULID"?**

- Creates two sources of truth — the DB and a file on disk — that can drift. ADR-0002 (DB as source of truth) explicitly rejects this pattern.
- Backup/restore flows have to remember to include the file alongside the DB dump.
- The file adds no information the DB doesn't already have; it only duplicates it for a presumed lookup-speed win that's irrelevant at panel-scale.

**Why not a SQL `UNIQUE` constraint on `is_panel_primary`?**

- MariaDB's UNIQUE constraint on a `TINYINT(1)` column with `DEFAULT 0` considers every `0`-valued row distinct — but the uniqueness check fires against BOTH `0` and `1` values. So two `is_panel_primary=0` rows would NOT conflict, but nor does the constraint enforce at-most-one `=1`. To actually enforce "at most one =1 row" at the SQL layer we'd need a partial index (`UNIQUE (is_panel_primary) WHERE is_panel_primary = 1`) which MariaDB doesn't support as of 11.x.
- The Go repository layer enforces the invariant in one well-tested place (`MarkPanelPrimary`) inside a transaction. Simpler than a MariaDB-version-sensitive schema dance.

### 3. Handle hostname changes by updating the existing panel-primary row in place; never orphan + re-register

**Decision**: if `$JABALI_SRV_HOSTNAME` changes between `install.sh` runs, `install_panel_primary_domain()` updates the existing `is_panel_primary=1` row's `name` field rather than inserting a second row. The reconciler sees the new hostname on next tick and re-converges (new DKIM keypair, new Stalwart domain, new nginx mail vhost, new self-zone records). The **old** self-zone records and DKIM key material are **orphaned** — not deleted automatically.

**Why not "insert a new row tagged `is_panel_primary=1`, clear the old row's flag, mark it for soft-delete"?**

- Doubles the row count on every hostname change. In practice this would accumulate stale rows nobody ever cleans up.
- The reconciler would still have to decide what to do with the old row's email state — tearing down Stalwart + nginx + DNS for it. Adding a soft-delete lifecycle for a rare event is overbuilding.
- Mailboxes attached to the old domain would need migration or abandonment. That's an operator decision, not an install.sh decision.

**Why not "refuse hostname change and require full reinstall"?**

- Hostile to operators who legitimately rename machines (migration from staging to production, acquisition rename, TLD change from `.test` to `.com`). The default should bend, not break.

**Why is "orphaned old records" acceptable?**

- The old self-zone for the old hostname is still in the `jabali_pdns.domains` table; `bootstrap_pdns_self_zone` only creates a new zone on fresh install, so the old zone sits there until the operator manually cleans it up. Same for the old DKIM key at `/etc/jabali-panel/dkim/<old-hostname>.key`. The runbook (Step 6 deliverable) documents the manual cleanup procedure. Not automating it avoids surprising the operator by deleting their mail history.

**Security note on orphaned DKIM keys**: the old DKIM key on disk is a private key for a domain that no longer receives mail. It's not directly exploitable (no outbound mail traffic uses it), but leaving it is sloppy. Runbook TODO: rm the `.key` file after confirming the old domain's zone no longer exists in pdns. Future M6.4.5 could automate cleanup conditional on "no mailboxes attached to the old domain exist".

### 4. No graceful fallback when the reconciler hasn't converged yet; `/webmail` just 301s and the downstream TLS handshake fails if mail.<hostname> isn't ready

**Decision**: `/webmail` is a plain `return 301 https://mail.<hostname>/` in the default nginx vhost. No sentinel file, no API health check, no static 503 page. If the browser lands on `mail.<hostname>` before the reconciler has converged (~30 seconds post-install), TLS handshake fails (no cert for that SAN yet) or nginx returns 444 (no vhost). The operator waits it out.

**Why not "static 503 'Webmail initializing' HTML served by the /webmail location until a sentinel file appears"?**

- Requires a sentinel file (e.g. `/run/jabali/webmail-ready`) the reconciler touches on first successful converge. That's another piece of state to manage, clean up on service stop, handle in the uninstall path. Complexity.
- Requires a static HTML asset committed to the repo, served via nginx `error_page`. More surface area.
- The window is ~30 seconds on a fresh install, happens once per install lifecycle. The complexity/UX-gain ratio is backwards.

**Why not "nginx auth_request polls the panel API, returns 200 when ready and 503 when not"?**

- Panel API polling from nginx is an anti-pattern in this codebase — we've deliberately avoided nginx-to-API coupling everywhere else, and breaking the pattern for a 30-second edge case sets a precedent.
- Adds runtime cost to every `/webmail` request even after the reconciler has converged.

**What about M6.4.4 — a future "graceful 503" implementation?**

Docketed in the post-merge follow-up list. If an operator files a complaint that "I hit `/webmail` during install and it looked broken", the tradeoff flips and we implement the sentinel file. Until then, the simpler path wins.

## Consequences

- **Positive**: Fresh installs have a working `/webmail` within ~30 seconds of install completion. `admin@<panel-hostname>` is a viable mailbox the operator can create via the existing UI. The cert, DNS, nginx, Stalwart, and DKIM plumbing all converge through the M6 code paths without duplication.
- **Positive**: One new column + one new repo method + one new install.sh function + one nginx location block. Minimal surface area.
- **Negative**: Panel-primary row is visible in the admin Domains list. Mitigated with a "System" tag and hidden Delete button — operator still sees it, but understands it's system-managed.
- **Negative**: Hostname changes leave orphaned records and DKIM keys. Documented in the runbook as a manual cleanup step. Acceptable for now; automation docketed as M6.4.5.
- **Negative**: `.local` hostnames (lab/dev installs) cannot receive mail from the public internet — no MX delegation, no Let's Encrypt cert. Same constraint as M6 itself; M6.4 doesn't make it worse.

## Security posture

- Delete-protect enforced at two layers: repository (typed `ErrCannotDeletePanelPrimary`) and API (HTTP 403 with `panel_primary_protected` code). Operator CANNOT accidentally delete via the UI or a normal API call. A malicious operator with direct DB access can still bypass via raw SQL — that's a higher-privilege attack surface than M6.4 is scoped to defend.
- DKIM private key at `/etc/jabali-panel/dkim/<hostname>.key` is `0600 jabali:jabali` per M6 convention. No change in M6.4.
- The self-signed cert's new `mail.<hostname>` SAN doesn't expand attack surface — same cert, same key, one more hostname it vouches for.
- Cert regeneration triggers a panel-api process restart (Go HTTP servers don't SIGHUP-reload their cert). Restart window is ~100ms. Cert rotation during install is the only scheduled trigger.

## Failure modes

| Failure | Detection | Remediation |
|---|---|---|
| `JABALI_SRV_HOSTNAME` is unset when install.sh runs | `install_panel_primary_domain()` `_die`s at entry | Operator sets `JABALI_SRV_HOSTNAME` in env and re-runs install.sh |
| Bootstrap order wrong — `install_panel_primary_domain` fires before `bootstrap_pdns_self_zone` | FK assertion in `install_panel_primary_domain` `_die`s with an ordering message | Code fix to install.sh `main()` — this is a dev-facing failure, not operator-facing |
| No admin user exists yet when `install_panel_primary_domain` fires | `_log` + return 0; function retries on next install.sh run | Operator completes admin bootstrap, re-runs install.sh |
| Cert regeneration fails (OpenSSL error, disk full) | `_die` from `provision_tls_cert` | Operator inspects OpenSSL error; usually disk space or permissions |
| Reconciler fails to converge (Stalwart down, agent UDS broken) | Settings → Email card stays "Initializing" indefinitely | Operator checks `journalctl -u jabali-panel-api` and `jabali-agent`; follow M6 runbook |
| Hostname change leaves orphaned self-zone/DKIM | No automated detection | Runbook documents the cleanup SQL + `rm` command |

## Relationship to M6.3

M6.4 assumes the self-zone for `<panel-hostname>` is resolvable from the panel host itself. Without M6.3, `dig @127.0.0.1 mail.<panel-hostname>` would recurse out to the public internet, get NXDOMAIN (or worse — a real-world answer for a clashing public hostname), and `/webmail` would redirect to a non-local target.

M6.3 made local resolution work. M6.4 makes the panel hostname produce something worth resolving. Both are no-ops without the other — they ship together as the "mail works out of the box" bundle.

## Rollback

Reverting M6.4 is additive-undoable:
1. `ALTER TABLE domains DROP COLUMN is_panel_primary`
2. `DELETE FROM domains WHERE name = <panel-hostname>` (optional — leaves the domain operational as a tenant row)
3. Revert install.sh changes via `git revert`
4. Revert panel-api + panel-ui changes

No data loss risk. Mailboxes attached to the panel-primary domain survive the column drop (their `domain_id` still references a valid row, just without the marker).
