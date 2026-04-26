# 0071 — MariaDB loopback-only (amends M25.1 skip-networking)

- **Date:** 2026-04-27
- **Status:** Accepted (amends ADR-0050 M25.1)
- **Deciders:** shuki

## Context

ADR-0050 (M25 Unix Sockets) enabled `skip-networking` on MariaDB at M25.1
to close TCP `:3306` entirely. Every panel-managed consumer (panel-api,
Kratos, pdns, phpMyAdmin SSO) had been migrated to `/var/run/mysqld/mysqld.sock`
beforehand, so we believed nothing on the host still needed TCP.

That was wrong. Stalwart's `Directory` resource of variant `Sql` embeds a
`store` of `@type: MySql` whose schema only exposes
`host`, `port`, `database`, `authUsername`, `authSecret`, plus connection
pool/TLS/timeout fields. There is no `socket`/`socketPath` field in the
upstream `MySql` store struct, so Stalwart cannot dial unix sockets.

Symptom: `webmail login failed (status 502); try logging in manually` on
`/sso/webmail`. Stalwart returned `HTTP 500` on every authenticated JMAP
call because the SqlDirectory query couldn't reach MariaDB. Bulwark's
`/api/auth/session` translated the JMAP failure into a 502
`{"error":"Failed to verify JMAP session"}` and the panel-api bridge
surfaced that to the browser.

## Decision

Replace the `skip-networking` drop-in with `bind-address=127.0.0.1`.
MariaDB still LISTENs on `:3306`, but only on loopback. Every consumer
that already speaks unix-socket continues to do so; Stalwart (the only
TCP holdout) reaches MariaDB via `127.0.0.1:3306`.

The drop-in path is unchanged
(`/etc/mysql/mariadb.conf.d/99-jabali-skip-networking.cnf`) so existing
hosts converge on the next install.sh run without needing a separate
migration step. The filename retains the historical `skip-networking`
slug for git-blame continuity; the file's contents and comments
document the bind-address strategy.

## Alternatives considered

- **Patch Stalwart to support unix-socket DSN.** Would preserve full
  TCP closure but requires upstream change + version pin maintenance.
  The mysql_async crate it uses does support `socket=...`, so the patch
  is feasible — but not in scope for an unblock. Revisit if the upstream
  schema gains the field.
- **Keep `skip-networking`, run a TCP→UDS proxy for Stalwart only.** Adds
  a new component (haproxy/socat) for one consumer. Higher operational
  cost than just binding loopback.
- **Move Stalwart's auth backend off SqlDirectory** (e.g. JSON-file
  directory rebuilt by reconciler on every mailbox change). Conflicts
  with ADR-0042 (SqlDirectory mailboxes is the truth). Rejected.

## Consequences

- TCP `:3306` is open on `127.0.0.1` only. UFW + jabali.slice keep
  external access closed; the attack surface is the loopback interface
  on a host where panel-api already runs as root-equivalent for
  reconciler ops.
- ADR-0050's "M25.1 closes 3306 outright" claim is now historical.
  Future ADRs that cite "no TCP MariaDB" must check this one instead.
- The install.sh `install_mariadb_skip_networking` function name is
  load-bearing; renaming would churn `jabali update` smoke tests on
  hosts that have run prior versions. Comment block updated; function
  name kept.

## Verification

Bulwark `POST /api/auth/session` with bad credentials returns `401`
(was `502` pre-fix). Live-verified on testserver `mx.jabali-panel.com`
on 2026-04-27 against mailbox `dsfsdfdsf@jabali.site`.
