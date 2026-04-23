# ADR-0050: M25 — Localhost Backend Hardening via Unix Sockets

**Status:** Accepted (2026-04-23)
**Driven by:** Plan `plans/m25-unix-sockets.md` (8 steps, 4 waves), Opus adversarial review (3 CRITICAL + 5 HIGH folded), pre-dispatch investigations I1 (Stalwart port ownership) + I2 (nginx Unix-socket proxy_pass syntax) on live VM 192.168.100.13 + Debian trixie nginx 1.26.3.

## Context

`netstat -lntp` on a fresh jabali-panel VM showed every internal HTTP backend bound to all interfaces by default:

| Service | Bind | Threat |
|---|---|---|
| Kratos public | `:::4433` | Bypassable login flow |
| Kratos admin | `:::4434` | **Full panel takeover** — admin API trusts localhost with zero auth |
| Stalwart admin-http | `:::8080` | Mail-server admin/management surface |
| Stalwart internal | `:::35181` (ephemeral) | Non-deterministic port; not even firewall-able |
| panel-api | `127.0.0.1:8443` + self-TLS | OK, but TLS for localhost is overhead and rotates only via panel update |
| MariaDB | `127.0.0.1:3306` TCP | OK loopback, but unnecessary protocol hop |
| LLMNR | `0.0.0.0:5355` | Unused on a server; LAN-segment name leak |

On a host without an external firewall (the default for VPS providers), Kratos `:::4434` is an instant full-panel compromise. Memory `feedback_install_sh_is_truth.md` says required postconditions belong in install.sh, not in operator-runbook "hope they ran ufw" — so the fix is to bind correctly at install time, not to write more docs.

## Decision

Convert every localhost-only HTTP backend to a Unix-domain socket. Pin every TCP service that doesn't support Unix sockets to an explicit `127.0.0.1`. Strip TLS from internal-only HTTP services. Make socket permissions the cross-process security boundary (one shared `jabali-sockets` group; mode `0660`).

### Architecture

1. **Unix sockets where supported; explicit `127.0.0.1` TCP where not.** Stalwart v0.16.0 doesn't support Unix sockets in its `NetworkListener` schema (verified by reading apply-plan.json.tmpl: bind is `ip:port` only). Stalwart admin moves from `:::` to `127.0.0.1:<same port>`. Everything else — Kratos public + admin, panel-api, Bulwark, MariaDB — goes to sockets.

2. **Single `jabali-sockets` group for cross-service socket reach.** New system group; members are `jabali` (panel-api + Kratos), `www-data` (nginx), `jabali-webmail` (Bulwark). `jabali-mail` (Stalwart) is intentionally NOT a member — Stalwart is a mail server, not an HTTP client of our internal stack. Socket file mode is `0660` everywhere; a connecting process needs (a) read+write on the socket file (group permission) AND (b) a successful `connect(2)` (which requires write permission). Mode `0640` would break `connect(2)` from nginx — verified by F-H-2 review note.

3. **`RuntimeDirectory=` per service.** Each unit declares `RuntimeDirectory=jabali-<name>` + `RuntimeDirectoryMode=0750` + `RuntimeDirectoryPreserve=no`. systemd creates `/run/jabali-<name>/` on every start with `User:Group:0750`; tears down on stop. The socket file inside is created by the service itself at `listen()` and lands at the process umask (typically 0022 → 0644). To pin the socket to `0660 jabali:jabali-sockets`, two mechanisms run together (review F-C-3 belt-and-braces):
   - The service config sets `socket.{mode,group}` where supported (Kratos: yes — verified empirically against v26.2.0 against scratch MariaDB during Step 2 verification gate).
   - `ExecStartPost=/bin/chmod 0660 ...` + `ExecStartPost=/bin/chgrp jabali-sockets ...` as defence-in-depth, idempotent on a correct config.

4. **nginx adopts Form D: `upstream u { server unix:/path; } ... proxy_pass http://u/;`.** Pre-dispatch I2 tested 4 syntactic forms on Debian trixie nginx 1.26.3; all pass `nginx -t`. Form D was adopted because rolling back any service to TCP is a one-line edit (`server unix:/path;` → `server 127.0.0.1:<port>;`) without touching any `proxy_pass` directive. Same upstream name reused across server blocks (e.g. `jabali_panel_api` referenced in both the panel vhost and the per-domain mail vhosts for `/sso/webmail`).

5. **TLS stripped from internal-only HTTP services; nginx terminates real TLS at the edge.** panel-api, Kratos public, Kratos admin all moved off self-TLS. The socket permission model (group `jabali-sockets` + mode `0660`) is the security boundary — Unix sockets are localhost by definition. Per F-H-5, the rollback drop-in restores both the TCP host config AND uncomments the TLS keys; both happen in the same systemd `.d/` file so rollback stays one edit.

6. **Reversibility via systemd drop-in, not a runtime feature flag.** Each service has a documented `/etc/systemd/system/jabali-<service>.service.d/90-revert-to-tcp.conf` template (in the runbook). Operator drops it in, `systemctl daemon-reload && systemctl restart jabali-<service>`, service binds TCP again. No code-path branches for socket-vs-TCP inside the service.

7. **Health checks + internal HTTP calls go through the Unix socket too.** `curl --unix-socket <path>` for shell-side; `http.Transport{DialContext: unixDialer(path)}` for Go. The kratosclient library exports `NewReverseProxyTransport(upstream string)` — accepts `http://`, `https://`, or `unix:/abs/path` and returns the wired transport. This is the single funnel; no `localhost:<port>` references survive in reconciler / monitoring code after M25 ships.

### Scope reduction (deferred to M25.1)

Two items from the plan were NOT shipped in M25. Documented here so the reasoning travels with the codebase:

- **MariaDB `skip-networking`.** Closes 3306 entirely. Not enabled because phpMyAdmin's `PMA_single_signon` plumbing (`install/phpmyadmin/sso.php`) still dials TCP — the panel-api SSO response sends `host:port`, and PMA passes those to PDO which uses a TCP DSN. Closing 3306 outright breaks SSO. M25.1 will update both the SSO panel-api response AND `sso.php` to pass a socket path, then enable `skip-networking`.
- **Kratos DSN flip.** Plan task 6.2 calls for a live-VM verification gate (`kratos migrate sql -e --config <test-config-with-unix-dsn>` against scratch MariaDB) before changing Kratos's DSN to socket form. We can't run that from a dev session. Kratos keeps `tcp(127.0.0.1:3306)` for now; works fine because skip-networking is also deferred. M25.1 closes both gates together.

What DID ship for MariaDB in Step 6: panel-api DATABASE_URL + PDNS DSN (panel-api reconciler + panel-agent client + pdns gmysql backend) all dial via `unix(/var/run/mysqld/mysqld.sock)`. Lower latency, no behavior change without skip-networking.

### LLMNR disable

`/etc/systemd/resolved.conf.d/10-jabali-disable-llmnr.conf` ships a `[Resolve]\nLLMNR=no` drop-in; install.sh reloads `systemd-resolved` after dropping it. Drop-in (not a wholesale config rewrite) so operators with LAN-resolution requirements can override via a higher-numbered drop-in.

## Threat model

After M25:

| Surface | Pre-M25 | Post-M25 |
|---|---|---|
| Kratos admin API | `:::4434` zero-auth on any host without UFW = full panel compromise | `/run/jabali-kratos/admin.sock` mode 0660 jabali:jabali-sockets — only processes in jabali-sockets group can `connect(2)` |
| Kratos public API | `:::4433` exposed login flow | `/run/jabali-kratos/public.sock` — same model; nginx fronts the only browser path |
| panel-api | `127.0.0.1:8443` self-signed TLS, certs renew only via panel update | `/run/jabali-panel/api.sock` — nginx terminates real Let's Encrypt cert at the edge |
| Bulwark webmail | `127.0.0.1:3000` reachable from any local process | `/run/jabali-bulwark/bulwark.sock` mode 0660 |
| Stalwart admin-http | `:::8080` admin/management UI exposed | `127.0.0.1:8080` (Stalwart can't bind sockets) |
| Stalwart internal | `:::35181` non-deterministic ephemeral | `127.0.0.1:18181` deterministic pinned |
| MariaDB | `127.0.0.1:3306` TCP loopback | `/var/run/mysqld/mysqld.sock` (default; 3306 still listens until M25.1) |
| LLMNR | `0.0.0.0:5355` LAN name leak | Disabled |

The `jabali-sockets` group is narrow: only socket files are owned by it. Document invariant — NEVER `chgrp jabali-sockets` on anything other than a socket.

## Verification

- 6 install-side bash tests (`install/tests/test_socket_helpers.sh`) cover the `verify_socket_perms` + `verify_no_all_interface_binds` helpers.
- Go-side unit tests cover the kratosclient unix-transport path (parse + rewrite + DialContext routing + end-to-end against unix-socket-bound httptest server) and the panel-api listener helper (TCP happy path, unix stale-socket cleanup, refuses-non-socket-file guard).
- install.sh post-start assertions block fresh installs that fail the bind invariants — `_die "Kratos admin still bound on TCP :4434"` etc.
- E2E (Playwright `panel-ui/tests/e2e/sockets.spec.ts`) is deferred to M25.1 alongside the live-VM verification gates that gate the Kratos DSN flip + skip-networking.

## Consequences

**Positive:**
- Kratos admin API is no longer one missing UFW rule away from full panel compromise on a public-IP host.
- Internal HTTP no longer pays per-request TLS handshake cost on every panel-api ↔ Kratos ↔ Bulwark hop.
- Self-signed cert rotation no longer matters for internal hops — only the edge (Let's Encrypt nginx cert) needs valid PKI.
- Socket file `0660` permission is the security boundary — easier to reason about than "trust 127.0.0.1 because no firewall holes".

**Negative:**
- One more system group to track (`jabali-sockets`). Manual `chgrp jabali-sockets` on a non-socket file would be a self-inflicted privilege expansion — documented as never-do in the runbook.
- Bulwark wrapper (`/opt/jabali-webmail/server-unix.js`) is jabali-owned code that has to stay compatible with whatever Next.js handler shape Bulwark ships. Mitigation: install.sh re-deploys the wrapper on every run, so a tarball-overwrite restores it on next `jabali update`.
- Operators running external monitoring (Zabbix, Nagios) that probed `127.0.0.1:<port>/health` need to switch to `--unix-socket` or hit the panel's edge nginx instead. Runbook documents both.
- Rollback to TCP is per-service (systemd drop-in template each), not a single global flag. Worse if the operator is debugging with no shell access; the same model means failures are confined.

## Related

- Plan: `plans/m25-unix-sockets.md`
- Runbook: `plans/m25-unix-sockets-runbook.md`
- Code:
  - `panel-api/internal/kratosclient/transport.go` — synthetic-host DialContext
  - `panel-api/cmd/server/listener.go` — unix-vs-tcp listener dispatch
  - `install/scripts/socket-helpers.sh` — `verify_socket_perms`, `verify_no_all_interface_binds`
  - `install/jabali-webmail/server-unix.js` — Bulwark Next.js Unix-socket wrapper
  - `install/nginx/jabali-panel-vhost.conf.tmpl` — TLS terminator on :8443 → unix upstream
- Memory: `project_m25_unix_sockets.md`
