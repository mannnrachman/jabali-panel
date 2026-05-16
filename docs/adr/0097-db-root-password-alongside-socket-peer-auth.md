# ADR-0097 — DB root/superuser password *alongside* socket/peer auth

**Date**: 2026-05-16
**Status**: accepted
**Deciders**: shuki (requested + chose Option A), Claude (design)
**Related**: ADR-0050 (unix-socket model), ADR-0071 (MariaDB loopback-only), M7 (databases), M46

## Context

cPanel/WHM exposes "Change MySQL root password". Jabali deliberately
does **not** use a root password: MariaDB `root@localhost` authenticates
via the `unix_socket` plugin and PostgreSQL `postgres` via `peer` on the
local socket. `install.sh:1660` hard-asserts socket-root reachability;
`db_create`, `backup_databases`, `db_restore`, `db_size`, and M46
maintenance/processlist all connect root-over-socket / postgres-over-peer.
A password is *also* already set for Postgres and stashed at
`/etc/jabali-panel/postgres.password` as a documented backup path.

Operators still expect the field (parity + a break-glass credential for
`mysql -uroot -p` from a non-`jabali` shell). The risk is doing it the
naive way and silently destroying socket/peer auth.

## Decision

Add/rotate a root (MariaDB) / superuser (Postgres) password that
**coexists with**, and never replaces, socket/peer auth (user Option A).
This **amends, does not supersede**, the socket-auth assumption: the
install.sh:1660 validator and the panel's socket/peer path are preserved.

**MariaDB — mandatory grammar** (verified against MariaDB official docs
via Context7; supported since 10.4, targets are 11.4/11.8). `ALTER USER
... IDENTIFIED VIA X OR Y` *removes any method not re-stated*, so the
only acceptable statement, socket listed first so the panel's socket
path keeps winning:

```sql
ALTER USER 'root'@'localhost'
  IDENTIFIED VIA unix_socket
  OR mysql_native_password USING PASSWORD('<generated>');
```

The agent MUST, after applying: (a) `SHOW CREATE USER 'root'@'localhost'`
and assert the output still contains `unix_socket`; (b) self-probe
`mysql --protocol=socket -e 'SELECT 1'` as root; (c) on any failure,
restore the prior auth definition and return an error. These are the
runtime guards for the install.sh:1660 invariant.

**PostgreSQL**: `ALTER ROLE postgres WITH PASSWORD '<generated>'`. Peer
auth on the socket is untouched (no `pg_hba` edit). This is largely
*exposing rotation* of the already-existing `/etc/jabali-panel/postgres.password`.

**Secret at rest**: MariaDB password written to a new
`/etc/jabali-panel/mysql-root.password` (`root:jabali`, mode `0640`),
mirroring the existing pg file. Both files are (re)written via
**temp-file + atomic rename**. The password is **never** stored in the
panel DB and **never** returned in any list/get response — the mint
endpoint returns it **once** (reveal-once, mirroring M7
`database_users.rotatePassword`).

Every rotation is `RequireAdmin`, audited (`db_admin_audit`), and emits
M14 `db.admin.root_password_rotated`.

## Alternatives considered

- **B — switch root to password-only auth.** Rejected by the user.
  Breaks install.sh:1660 + every root-over-socket consumer; would need
  to supersede the socket-auth model and rework backups/repair/db_create.
- **C — don't build it, explain in UI.** Defensible (socket auth is
  strictly more secure) but the user wants the credential available.
- **Store the secret in the DB (encrypted).** Rejected: the DB is the
  thing the credential gates; a flat root-only file matches the existing
  pg-password pattern and survives a DB outage.

## Consequences

- `mysql -uroot -p` / `psql -U postgres -h 127.0.0.1` work for a human;
  the panel/agent path is unchanged.
- A new root-only secret file to back up / rotate; documented in the
  runbook + ENV.md.
- If a future MariaDB drops `IDENTIFIED VIA ... OR ...`, the guard (a)
  fails loud rather than silently degrading — acceptable.
