# M25 — Localhost Backend Hardening via Unix Sockets

**Branch:** `m25/unix-sockets`
**Target:** 8 PR-sized steps across 4 waves.
**Status:** Dispatch-ready (2026-04-23). Opus adversarial review complete (3 CRITICAL + 5 HIGH folded in). Pre-dispatch investigations I1 + I2 resolved on live VM 10.0.3.13 + Debian trixie nginx 1.26.3.

## Goal

Every localhost-only HTTP backend becomes a Unix-domain socket with filesystem permissions. Every `:::`-bound TCP service that doesn't support Unix sockets gets an explicit `127.0.0.1` bind. The Kratos admin API — currently reachable on all interfaces with zero auth on prod VMs without an external firewall — stops being a takeover surface.

## Pre-dispatch investigations — RESOLVED (2026-04-23)

Both gates closed; findings baked into the step definitions below.

### I1 — Stalwart port 8080 + 35181 ownership [RESOLVED]

Investigated on live VM 10.0.3.13 via `ss -lntp` + `/proc/<pid>/cmdline` + curl probes.

| Port | Listener | Identity | Source of binding |
|---|---|---|---|
| `0.0.0.0:8080` | stalwart-mail (PID 32798) | **Admin HTTP / Management UI** — `/admin`, `/management`, `/health`, `/status` all return 302 (auth redirect); this is the same surface Bulwark proxies on localhost. | **NOT** in `config/stalwart/apply-plan.json.tmpl`. Stalwart ships an implicit default `#admin-http` listener on `0.0.0.0:8080` that has to be **added explicitly** to the apply-plan with `"bind": {"127.0.0.1:8080": true}` to override the default. |
| `:35181` | (transient — not listening at probe time) | Likely ephemeral spam-classifier training or internal IPC socket chosen by OS when no `server.listener.<id>.bind` is set. Non-deterministic port. | Not in apply-plan. Fix: add an explicit internal listener with a pinned `127.0.0.1:<port>` in Step 7 so it stops roulette-picking. |

**Security finding:** `:::8080` is a real admin-takeover surface equivalent to Kratos `:::4434`. It must be closed in Wave D Step 7 before M25 ships.

**Step 7 scope confirmed:** add `#admin-http` (and `#internal-training` placeholder for :35181) as explicit `x:NetworkListener` entries in `apply-plan.json.tmpl`, each with `"bind": {"127.0.0.1:<port>": true}`. Apply via `stalwart-cli apply`; verify with `ss -lntp | grep stalwart` expects only `127.0.0.1:<port>` rows.

### I2 — nginx Unix-socket proxy_pass syntax [RESOLVED]

Tested on Debian trixie nginx 1.26.3 (the target platform) using 4 syntactic forms with `nginx -t -c /tmp/nginx-socket-test.conf`:

| Form | Literal | `nginx -t` result |
|---|---|---|
| A | `proxy_pass http://unix:/run/x.sock;` | PASS (no URI) |
| B | `proxy_pass http://unix:/run/x.sock:/;` | PASS (colon + URI `/`) |
| C | `proxy_pass http://unix:/run/x.sock:;` | PASS (colon + empty URI) |
| **D** | `upstream u { server unix:/run/x.sock; } ... proxy_pass http://u/;` | **PASS + recommended** |

**Adopted:** **Form D** (named `upstream { server unix:...; }` + `proxy_pass http://u/;`). Rationale: swap unix↔TCP in one line without touching any `proxy_pass` directive; matches the existing upstream pattern already used for panel-api in some server blocks; makes the feature-flag rollback (see Wave D Step 8) a one-line edit per service.

**Steps 3, 4, 5 updated** to use Form D where they previously inlined `proxy_pass http://unix:...:/;`. The proxy_pass literal in every rewritten `nginx` block is now `proxy_pass http://<service-upstream>/;` with a sibling `upstream <service-upstream> { server unix:<path>; }` declaration.

### Verdict

Both investigations green. **Plan is fully dispatch-ready** — Wave A (Step 1 foundation) can start immediately. Step 7 gains a specific "add `#admin-http` listener with explicit `127.0.0.1:8080` bind" task instead of the prior "investigate first" placeholder.

## Motivation

`netstat -lntp` on a live jabali-panel instance showed Kratos public (`:::4433`), Kratos admin (`:::4434`), Stalwart JMAP/management (`:::8080`, `:::35181`), and panel-api (`:::8443`) all bound to all interfaces. On a VM with a public IP and no firewall, Kratos `:::4434` is an instant full-panel compromise (admin API trusts localhost with no auth by default). Memory `feedback_install_sh_is_truth.md` says required postconditions belong in install.sh, not in operator runbooks — securing these binds belongs in the service config, not in "hope the operator ran `ufw`".

## Scope

**In scope**

| Service | Current | Target |
|---|---|---|
| Kratos admin (4434) | `:::4434` | `/run/jabali-kratos/admin.sock` (mode 0660, group `jabali-sockets`) — OR `127.0.0.1:4434` if Kratos v26.2.0 config rejects Unix sockets (verify at step start) |
| Kratos public (4433) | `:::4433` | `/run/jabali-kratos/public.sock` (mode 0660, group `jabali-sockets`) — OR `127.0.0.1:4433` fallback |
| panel-api (8443) | `127.0.0.1:8443` + TLS | `/run/jabali-panel/api.sock` (mode 0660, group `jabali-sockets`), TLS stripped for this binding |
| Bulwark next-server (3000) | `127.0.0.1:3000` | `/run/jabali-bulwark/bulwark.sock` via a ~20-line custom `server.js` wrapper — OR keep `127.0.0.1:3000` if custom server introduces unacceptable maintenance burden (decision gate in Step 5) |
| MariaDB (3306) | `127.0.0.1:3306` TCP | existing `/var/run/mysqld/mysqld.sock`; `skip-networking` in `my.cnf`; GORM DSN switches to `unix(...)` in BOTH panel-api AND Kratos (per review F-C-1 — Kratos has its own DB connection, hardcoded to TCP at `install/kratos.yml.tmpl:7`, would crash fresh installs if missed) |
| Stalwart 8080 / 8446 / 35181 (audit) | `:::8080`, `127.0.0.1:8446`, `:::35181` | `127.0.0.1:<same port>` for every localhost-only Stalwart listener (Stalwart does not support Unix sockets) |
| LLMNR `:5355` | `0.0.0.0:5355` + `:::5355` | disabled via `LLMNR=no` in `resolved.conf` drop-in |

**Out of scope — must stay out**

- **DNS-protocol services**: `pdns_server :5300` (loopback forwarder for recursor), `pdns_recursor :53`, `systemd-resolved :53` stubs — DNS protocol requires TCP/UDP; sockets don't apply.
- **Public-facing services**: nginx `:80`/`:443`, sshd `:22`, Stalwart SMTP `:25`/`:465`/`:587`, IMAPS `:993`, POP3S `:995`, ManageSieve `:4190`, pdns auth `10.0.3.13:53` — all correctly public, not touched.
- **Cross-host deployments**: M25 assumes single-host per memory `project_arch_decisions.md`. Any future auth/DB split to separate hosts exits this pattern and requires a new plan.
- **mTLS between backends**: currently panel-api uses TLS to talk to itself. Unix sockets obsolete this; no replacement mTLS is added — the socket permission model is the security boundary.

## Architecture decisions (fold into ADR-0050)

1. **Unix sockets where supported; explicit `127.0.0.1` TCP where not.** Stalwart doesn't support Unix sockets in v0.16.0 (verified in research: `install/stalwart/apply-plan.json.tmpl` binds use `ip:port` syntax only, no Unix path option). Stalwart listeners therefore move from `:::<port>` or unspecified (which defaults to all-interfaces) to explicit `127.0.0.1:<port>`. Everything else — Kratos, panel-api, Bulwark, MariaDB — goes to sockets.
2. **Single `jabali-sockets` group for cross-service socket reach.** Create a new system group `jabali-sockets`. Members: `jabali` (panel-api AND Kratos, both run as this user), `www-data` (nginx), `jabali-webmail` (Bulwark — receives requests from nginx, doesn't need group but added for symmetry so future internal HTTP callbacks work). Kratos runs as `jabali` so it owns its sockets; nginx reaches them via group. Unix socket semantics for HTTP over stream sockets: mode 0660 (`rw-rw----`) gives nginx full bidirectional access — reads request, writes response, on the same fd. No separate write permission needed beyond group membership because `connect(2)` on a stream socket requires write permission on the socket file, but once connected, the read/write happens on a new fd (the socketpair). Per review F-H-2 — 0660 is correct; 0640 would break `connect(2)` from nginx. `jabali-mail` does NOT get socket group membership — Stalwart doesn't talk to internal backends over sockets (it's a mail server, not an HTTP client of our stack).
3. **Socket directory via systemd `RuntimeDirectory=`.** Each service's unit file gets `RuntimeDirectory=jabali-<name>` + `RuntimeDirectoryMode=0750` + `RuntimeDirectoryPreserve=no`. systemd creates `/run/jabali-<name>/` on every start with correct owner:group:mode; tears down on stop. No install.sh logic to manage the directory.
4. **Reversibility via systemd drop-in, not a runtime feature flag.** Each service's install provides a documented `/etc/systemd/system/jabali-<service>.service.d/90-revert-to-tcp.conf` template in the runbook. Operator drops it in, `systemctl daemon-reload && systemctl restart jabali-<service>`, and the service binds TCP again. No code-path branches for socket-vs-TCP inside each service — config-driven entirely.
5. **Health checks and internal HTTP calls switch to `--unix-socket`** (curl) or `http.Transport{DialContext: unixDialer}` (Go). No `localhost:<port>` left anywhere in reconciler / monitoring scripts after M25 ships.
6. **`jabali update` is the migration path.** On upgrade, migration step: (a) create `jabali-sockets` group if absent, (b) add users to group, (c) replace configs, (d) `systemctl daemon-reload`, (e) restart services in dependency order (Kratos → panel-api → nginx → Bulwark), (f) probe each socket. No fresh install required; existing installs converge on next `jabali update -f`.
7. **Strip TLS from internal-only HTTP services, but keep the config keys commented for rollback.** panel-api, Kratos public, Kratos admin — all currently run TLS for their localhost binds. Over Unix sockets this is pointless: sockets are localhost by definition, and permission-gated. Comment out (not delete) the cert/key config keys in each service's config template; the socket permission model replaces the crypto boundary. If the rollback drop-in flips `host:` back to TCP, the operator uncomments the TLS keys in the same drop-in. nginx still terminates real TLS to the internet. Per review F-H-5 — a rollback to TCP with TLS stripped means the service can't start; preserving the keys as commented lets rollback be a one-file edit.

## Risk register

| ID | Risk | Severity | Mitigation |
|----|------|----------|---|
| R1 | Socket permissions wrong → nginx returns 502 because it can't read the backend socket | **HIGH** | Step 1 sets up `jabali-sockets` group with explicit membership verification; each service step includes a post-start probe (`sudo -u www-data curl --unix-socket <path> http://x/health`) that fails loudly. install.sh gains a `verify_socket_perms` function called after every unit start. |
| R2 | Kratos v26.2.0 doesn't accept Unix socket syntax in `serve.public.host` / `serve.admin.host` | **HIGH** | Step 2 opens with a verification gate: write a minimal test `kratos.yml` with `host: "unix:/tmp/test.sock"`, run `kratos validate` AND `kratos serve --config <file>` with a 5-second boot window. If either fails, the step pivots to the fallback (`host: "127.0.0.1"`) and the plan is annotated. Either outcome is valid scope completion. |
| R3 | Bulwark custom `server.js` wrapper diverges from stock Next.js behavior (middleware, static file serving, hot-reload paths) | **HIGH** | Step 5 uses Next.js's documented custom-server pattern (`const app = next({ dev: false }); app.prepare().then(() => server.listen(socketPath))`). Integration test pulls a known set of routes (login page, static asset, API route) over the socket and asserts 200s match the current 127.0.0.1:3000 behavior byte-for-byte. If divergence appears, step 5 decision gate fires and Bulwark stays on TCP (socket conversion for that service deferred to M25.1). |
| R4 | MariaDB `skip-networking` breaks something we didn't expect — e.g., an external script or a test fixture that uses TCP loopback | **HIGH** | Pre-flight grep across the repo for every `127.0.0.1:3306`, `localhost:3306`, `tcp(127.0.0.1`, `DB_HOST=127.0.0.1` (or localhost). All call-sites updated to use the socket DSN in the same commit. Step 6 runs the full panel-api test suite against socket-only before declaring done. |
| R5 | systemd `RuntimeDirectory=` created by systemd (root), but service's own socket inside it is created with default umask, leaving it 0600 — nginx 502 | **HIGH** | Per review F-C-3: RuntimeDirectory IS created before ExecStart (verified in systemd.unit(5)), but the SOCKET FILE itself is created by Kratos/panel-api at listen-time with the service's umask (typically 0022 → socket 0644 default). Three mitigations: (a) Kratos and panel-api configs specify `socket.mode: "0660"` + `socket.group: "jabali-sockets"` where supported; (b) systemd `ExecStartPost=/bin/chmod 0660 /run/jabali-<name>/<name>.sock` + `ExecStartPost=/bin/chgrp jabali-sockets /run/jabali-<name>/<name>.sock` as belt-and-braces regardless of whether the service config sets them; (c) the verify_socket_perms helper runs after each systemctl restart and fails if perms are wrong, catching this at install time. |
| R6 | Migration from TCP to socket mid-request — an in-flight HTTP call from panel-ui sees the TCP listener gone and fails | MEDIUM | `jabali update` already restarts services; users see a short outage (≤2s) during the bounce. This is the same UX as any update. Accept. Runbook documents: run `jabali update -f` during a maintenance window. |
| R7 | An operator's external monitoring (Zabbix, Nagios, Grafana probes) currently pings `127.0.0.1:8443/health` — breaks after the switch | MEDIUM | Runbook documents the socket paths + a curl example per service. Add a compatibility shim: an nginx `location /_health/<service>` that reverse-proxies to each socket, exposing health checks on `https://<panel>/_health/<service>` for external monitoring. This ALSO reduces the "bypass nginx" attack surface by making external health checks pass through nginx. |
| R8 | Adding `www-data` to `jabali-sockets` group expands nginx's filesystem reach beyond sockets — anything else jabali-sockets can read, so can nginx | MEDIUM | `jabali-sockets` is a narrow group; no files outside `/run/jabali-*/` are owned by it. Document this invariant: NEVER `chgrp jabali-sockets` on anything other than a socket. Add a lint check to install.sh that greps for accidental use. |
| R9 | LLMNR disable breaks local-segment name resolution (e.g., a LAN device that was resolving `jabali-panel.local` via LLMNR) | LOW | LLMNR is rarely used in datacenter environments; mDNS (via `systemd-resolved`) covers `.local` resolution and stays enabled. Runbook notes: if operators use LLMNR on purpose (Windows-heavy LAN), they can opt out of the `LLMNR=no` drop-in by overriding it. |
| R10 | A forgotten `:::`-bind elsewhere (Prometheus exporter, tracing collector, Goroutine debugger) stays exposed | MEDIUM | Step 7 audit extends beyond Stalwart to grep every config file for `"0.0.0.0"`, `"[::]"`, `":::"`, and every systemd unit for ports that bind implicit-any. Any discovery not covered by the plan gets flagged as a finding and folded into Step 7 scope. |

## Plan mutation rules

- Every step branches off `m25/unix-sockets` as `m25/step-N-<slug>`. FF-merged into `m25/unix-sockets` after review. Not to `main` until Step 8 is green.
- Any step that changes a systemd unit MUST include the full daemon-reload + restart sequence in its verification, AND an explicit `systemctl status --no-pager jabali-<service>` assertion (active, no failures).
- Any step that changes nginx MUST run `nginx -t` before reload AND `nginx -s reload` after, with assertions.
- Don't push to main partial — full ship only. Per memory `feedback_no_partial_blueprint_to_main.md`.

## Services affected by M25 — wire-contract summary

Backward-compatible with everything OUTSIDE jabali. Every service's external protocol (HTTP on nginx `:443`, SMTP on Stalwart `:25`, etc.) is unchanged. Internal HTTP URLs in config files, health-check scripts, reconciler calls — change from `https://127.0.0.1:<port>/` or `http://127.0.0.1:<port>/` to `http://unix:/run/jabali-<name>/<name>.sock:/` (nginx syntax) or Go-side unix Dialer.

---

## Waves

```
Wave A (foundation)   Step 1                                [dispatcher inline]
Wave B (Kratos)       Step 2 → Step 3                       [serial — shared config]
Wave C (services)     Step 4 → Step 5 → Step 6              [serial — each updates nginx + install.sh]
Wave D (tighten+ship) Step 7 → Step 8                       [serial — audit, then E2E + docs]
```

Parallelism is limited intentionally. Every step touches `install.sh` and/or nginx config files — concurrent step branches fighting over the same files triggers the exact contract-drift class in memory `feedback_subagent_contract_drift.md`.

---

## Step 1 — Foundation: group, RuntimeDirectory, LLMNR, install.sh (Wave A)

**Dependencies:** none. **Model tier:** default. **Branch:** `m25/step-1-foundation`.

**Context brief (cold-start):**
install.sh currently creates `jabali`, `jabali-mail`, `jabali-webmail` service users (lines 2030, 3218, 3660 approximately). No cross-service socket group exists. Every service's systemd unit either sits in `install/systemd/` or is written by install.sh. nginx runs as `www-data`. LLMNR defaults on via systemd-resolved.

**Tasks:**
1. **Add `jabali-sockets` group** — install.sh function `ensure_jabali_sockets_group` runs before any service user creation. Creates the group if absent (idempotent). Adds `jabali`, `www-data`, `jabali-webmail` to it. Does NOT add `jabali-mail`.
2. **Add `RuntimeDirectory=` to every service unit that will own a socket** — `jabali-kratos.service`, the panel-api unit (create if absent — research found it runs via install.sh logic rather than a checked-in unit; fix that first). `jabali-webmail.service` similarly. Set `RuntimeDirectoryMode=0750`, `RuntimeDirectoryPreserve=no`. NOTE: the owner of `/run/jabali-<name>/` is the service's `User=`; the group is its `Group=`. Make sure each service's Group is `jabali-sockets` OR a group `jabali-sockets` can access — easiest is to chgrp the socket itself after creation, which Kratos/panel-api will do via their config (see Step 2/4).
3. **LLMNR disable** — install.sh writes `/etc/systemd/resolved.conf.d/10-jabali-disable-llmnr.conf` with `[Resolve]\nLLMNR=no\n`. Drop-in so operator can override if needed. `systemctl reload-or-restart systemd-resolved`.
4. **install.sh helper `verify_socket_perms <path> <owner> <group> <mode>`** — post-service-start assertion helper. Fails with clear message if permissions drift. Reused in Steps 2-5.
5. **install.sh helper `verify_no_all_interface_binds <port>`** — runs `ss -lntp | grep ":<port>"` and fails if any line has `0.0.0.0:` or `[::]:` binding. Reused in Steps 2, 3, 4, 5, 7.
6. Unit tests for both helpers (bash tests under install/tests/ — follow existing pattern).

**Exit criteria:**
- Fresh VM install creates `jabali-sockets` group with exactly the 3 expected members.
- `getent group jabali-sockets` shows members.
- `/etc/systemd/resolved.conf.d/10-jabali-disable-llmnr.conf` exists; `resolvectl status | grep -i llmnr` shows `LLMNR setting: no`.
- Re-running install.sh is idempotent (no duplicate group, no re-add errors).

**Verification commands:**
```bash
./install.sh --debug | tee /tmp/install.log
getent group jabali-sockets
resolvectl status | grep -i llmnr
ss -lntp | grep 5355  # should be empty after reload
```

**Rollback:** revert the commit; the group and drop-in file stay behind (manually `groupdel jabali-sockets` + `rm /etc/systemd/resolved.conf.d/10-jabali-disable-llmnr.conf` if desired). No service impact from rollback — no service depends on the group until Step 2.

---

## Step 2 — Kratos admin (4434) → Unix socket (Wave B)

**Dependencies:** Step 1. **Model tier:** strongest (P0 security fix). **Branch:** `m25/step-2-kratos-admin`.

**Context brief:**
Kratos config at `install/kratos.yml.tmpl`. Lines 9-34 define `serve.public` (4433) and `serve.admin` (4434). Currently neither has a `host:` key — the public and admin API default to binding all interfaces. Kratos runs as `User=jabali Group=jabali` (systemd unit at `install/systemd/jabali-kratos.service`). Version v26.2.0.

**Tasks:**
1. **Verification gate** — before any config change, write a minimal `/tmp/kratos-socktest.yml` with:
   ```yaml
   serve:
     admin:
       host: "unix:/tmp/kratos-admin-test.sock"
       port: 4434
   ```
   Run `kratos serve --config /tmp/kratos-socktest.yml --dev` in the background with 5s timeout. Check `ls /tmp/kratos-admin-test.sock` and `curl --unix-socket /tmp/kratos-admin-test.sock http://x/admin/health/ready`. Three outcomes:
   - a) Socket exists + health OK → **unix-socket path**. Proceed to task 2a.
   - b) Kratos rejects syntax OR socket not created → **fallback path**. Proceed to task 2b. Record in ADR-0050 that v26.2.0 doesn't support Unix sockets and which release notes reflect this.
   - c) Some other failure (crash, auth error, etc.) → STOP. Debug. This is not a fallback scenario.
2a. **Unix-socket path** — edit `install/kratos.yml.tmpl`:
   ```yaml
   serve:
     admin:
       host: "unix:/run/jabali-kratos/admin.sock"
       socket:
         mode: "0660"
         group: "jabali-sockets"
   ```
   (Verify Kratos's `serve.admin.socket` sub-block matches v26.2.0 schema — grep their OpenAPI spec if unsure.)
2b. **Fallback path** — edit `install/kratos.yml.tmpl`:
   ```yaml
   serve:
     admin:
       host: "127.0.0.1"
   ```
3. Update `install/systemd/jabali-kratos.service`: ensure `RuntimeDirectory=jabali-kratos` is set (may already be from Step 1), `User=jabali`, `Group=jabali-sockets` (change from `Group=jabali` so socket creation inherits the socket group).
4. Update `install/nginx/.ory-location.conf:9`: rewrite `proxy_pass http://127.0.0.1:4433/;` to either:
   - Unix-socket path: **no change here** — this file is the public API (4433), not admin. Step 3 covers this. Admin API is NOT exposed through nginx; panel-api talks to it directly.
5. **panel-api internal change** — find where panel-api calls Kratos admin API (likely in `panel-api/internal/kratosclient/`). Grep for `4434` AND the pattern `http://127.0.0.1:4434`. Update client config to point to either `unix:/run/jabali-kratos/admin.sock` (socket path) or `127.0.0.1:4434` (fallback). Unix socket version uses `http.Transport{DialContext: unixDialer(path)}` + base URL `http://kratos.admin/` (host is arbitrary when dialing unix).
6. **Strip TLS from admin binding** — if `install/kratos.yml.tmpl` had `tls.cert` + `tls.key` under `serve.admin`, remove them. TLS over Unix socket or 127.0.0.1 is overhead.
7. **Unit + integration tests** — integration test validates panel-api → Kratos admin round-trip over the new transport. Use the existing test harness; just swap the URL/transport.
8. **Post-start assertions** — install.sh calls `verify_socket_perms /run/jabali-kratos/admin.sock jabali jabali-sockets 0660` (socket path) or `verify_no_all_interface_binds 4434` (fallback).

**Exit criteria:**
- Path 2a: `ls -la /run/jabali-kratos/admin.sock` shows `srw-rw---- jabali jabali-sockets`; `ss -lntp | grep 4434` is empty.
- Path 2b: `ss -lntp | grep 4434` shows exactly one line, `127.0.0.1:4434`.
- Either way: panel-api's Kratos integration tests pass.

**Rollback:** systemd drop-in override (documented in runbook) reverts the `serve.admin.host` to the old default. Also: revert the commit and re-run `jabali update -f`.

---

## Step 3 — Kratos public (4433) → Unix socket (Wave B)

**Dependencies:** Step 2 merged to `m25/unix-sockets`. **Model tier:** default. **Branch:** `m25/step-3-kratos-public`.

**Context brief:**
Step 2 resolved the Unix-socket-vs-127.0.0.1 question. Step 3 applies the same answer to the public API. nginx proxies `/.ory/*` to this endpoint (file `install/nginx/.ory-location.conf:9`).

**Tasks:**
1. Apply the same path (socket or 127.0.0.1) chosen in Step 2 to `serve.public` in `kratos.yml.tmpl`. Path: `/run/jabali-kratos/public.sock`.
2. Update `install/nginx/.ory-location.conf:9` using **Form D** (adopted at pre-dispatch I2). Declare an upstream in a server-block-adjacent snippet:
   ```nginx
   upstream kratos_public { server unix:/run/jabali-kratos/public.sock; }
   ```
   and change `proxy_pass http://127.0.0.1:4433/;` → `proxy_pass http://kratos_public/;`. Rationale: single source of truth for the backend target; rollback to TCP is a one-line edit (`server unix:...;` → `server 127.0.0.1:4433;`) without touching the `proxy_pass` directive.
3. If Step 2 chose the **127.0.0.1 fallback** (Kratos doesn't honour Unix sockets in v26.2.0): leave the nginx snippet on `server 127.0.0.1:4433;` inside the same upstream block. The `proxy_pass http://kratos_public/;` directive is identical either way — this is the whole point of Form D.
4. Run `nginx -t` → `nginx -s reload` in install.sh post-config.
5. Integration test: SPA loads `/login`, Kratos flow initialization round-trips through nginx.
6. `verify_socket_perms /run/jabali-kratos/public.sock jabali jabali-sockets 0660` OR `verify_no_all_interface_binds 4433`.

**Exit criteria:** `curl -sk https://<panel>/.ory/self-service/login/browser` returns 200 with a flow body. `ss -lntp | grep 4433` is empty (socket path) or shows only `127.0.0.1:4433` (fallback).

**Rollback:** revert; systemd drop-in + nginx snippet to restore TCP.

---

## Step 4 — panel-api → Unix socket + TLS strip (Wave C)

**Dependencies:** Step 3. **Model tier:** strongest (touches every API consumer). **Branch:** `m25/step-4-panel-api`.

**Context brief:**
panel-api's listener at `panel-api/cmd/server/serve.go:306`, address config-driven via `cfg.Server.Addr` (`panel-api/internal/config/config.go:176`). TLS checked at `serve.go:338-340` (`cfg.Server.TLSCert` + `TLSKey`). nginx hits it at `install/nginx/jabali-mail-vhost.conf.tmpl:84` (`proxy_pass https://127.0.0.1:8443`). Bulwark + other internal clients may also call panel-api directly.

**Tasks:**
1. Extend `panel-api/internal/config/config.go` — `Server.Addr` already supports a path. Add explicit Unix-socket detection: if `Addr` starts with `/` OR `unix:`, call `net.Listen("unix", path)`; else `net.Listen("tcp", addr)`.
2. After `net.Listen("unix", path)`, chmod the socket to 0660 and chown to the service's User + `jabali-sockets` group. Use `os.Chmod` + look up group via `os/user`.
3. **Strip TLS** — when listening on unix socket, skip the TLS branch unconditionally. Add an assertion: if `Addr` is a Unix path AND `TLSCert` is set, log a warning and ignore TLS.
4. **Pre-flight client inventory** (per review F-H-4) — grep the FULL tree, not just panel-api:
   - `grep -rn --include='*.go' -E 'https?://(127\.0\.0\.1|localhost):8443' .`
   - `grep -rn --include='*.sh' -E '127\.0\.0\.1:8443|localhost:8443' install/ panel-api/ panel-agent/`
   - `grep -rn --include='*.service' ':8443' install/systemd/`
   - `grep -rn 'panelAPIURL\|PANEL_API_URL' .`
   Enumerate every hit in the step's PR description BEFORE writing any code. If the list is empty, state that explicitly — a zero-count result is meaningful data. Expected surfaces: reconciler internal HTTP calls, panel-agent callbacks (if any), install.sh post-start health probes, Kratos client config pointing at panel-api, E2E helpers.
5. **Update all internal clients** — each call-site either (a) switches to a unix-dial Go `http.Client` or (b) keeps HTTPS-to-nginx at `https://<panel>/api/v1/...`. Prefer (b) where the caller is already using the panel's public URL (defense-in-depth: all internal traffic passes through nginx, subject to rate-limits + access logs).
5. **nginx** — update `jabali-mail-vhost.conf.tmpl:84` using **Form D**: add `upstream panel_api { server unix:/run/jabali-panel/api.sock; }` to the relevant http{} or server{} context, then change `proxy_pass https://127.0.0.1:8443` → `proxy_pass http://panel_api/;` (note protocol change `https→http` — plaintext over Unix socket). Strip all `proxy_ssl_*` directives from that location block.
6. **Systemd unit** — create `install/systemd/jabali-panel.service` if it doesn't exist as a checked-in file (research says it runs via install.sh logic — reverse that; a checked-in unit is the right shape). Add `RuntimeDirectory=jabali-panel`, `User=jabali`, `Group=jabali-sockets`, `RuntimeDirectoryMode=0750`.
7. **install.sh config rewrite** — the config file panel-api reads (probably `/etc/jabali-panel/config.yml` or similar) gets `server.addr: "/run/jabali-panel/api.sock"` and no `tls_cert`/`tls_key` keys.
8. **Reconciler / monitoring internal HTTP calls** — grep for any `http.Get("http://127.0.0.1:8443/...")` in reconciler code. Switch to unix dialer.
9. Full test suite + integration tests.
10. `verify_socket_perms /run/jabali-panel/api.sock jabali jabali-sockets 0660`.

**Exit criteria:** end-to-end SPA request through nginx succeeds; `ss -lntp | grep 8443` empty; panel-api logs show `listening on /run/jabali-panel/api.sock`.

**Rollback:** revert commit + systemd drop-in that sets `Environment=JABALI_PANEL_ADDR=127.0.0.1:8443` (if the config supports env override — implement if not).

---

## Step 5 — Bulwark webmail → Unix socket (Wave C)

**Dependencies:** Step 4. **Model tier:** default. **Branch:** `m25/step-5-bulwark-socket`.

**Context brief:**
Bulwark is Next.js v14+ standalone at `/opt/jabali-webmail/server.js`. Systemd unit at `install/systemd/jabali-webmail.service`, `Environment=HOSTNAME=127.0.0.1 PORT=3000`. Next.js stock standalone `server.js` doesn't accept a Unix socket path.

**Tasks (decision gate in task 1):**
1. **Decision gate** — inspect `/opt/jabali-webmail/server.js` (the file deployed by Bulwark's own standalone build). If it's a black-box Next.js generated file (yes, likely), we can't edit it in-place (next `bulwark` update overwrites). Three options:
   - a) **Wrap with a tiny `server-unix.js`** that imports Bulwark's handler and `http.Server.listen(socketPath)`. ~30 lines. Ship it to `/opt/jabali-webmail/server-unix.js`; systemd runs that instead.
   - b) **Proxy via socat** — `socat UNIX-LISTEN:/run/jabali-bulwark/bulwark.sock,reuseaddr,fork TCP:127.0.0.1:3000`. Hacky; adds a hop + a process.
   - c) **Defer** — keep Bulwark on `127.0.0.1:3000`. Mark as M25.1 if real socket conversion becomes justified.
   Write a decision note in the step's PR description: option chosen + why.
2. **If (a):** create `install/jabali-webmail/server-unix.js`. Deploy alongside `server.js` via install.sh's existing Bulwark install block. Edit systemd unit: `ExecStart=/usr/bin/node /opt/jabali-webmail/server-unix.js`, `Environment=SOCKET_PATH=/run/jabali-bulwark/bulwark.sock`. Add `RuntimeDirectory=jabali-bulwark`, `Group=jabali-sockets`.
3. **If (c):** skip the conversion; add an ADR-0050 entry noting Bulwark stays TCP and why. Proceed directly to task 5.
4. (If a) **Integration test**: curl `/webmail/` via socket, assert 200 + body matches the TCP baseline.
5. Update `install/nginx/jabali-mail-vhost.conf.tmpl:40` using **Form D** (if a): add `upstream bulwark_webmail { server unix:/run/jabali-bulwark/bulwark.sock; }` in the surrounding http{} context, then `proxy_pass http://bulwark_webmail/;`. Leave unchanged (if c).
6. `verify_socket_perms` OR `verify_no_all_interface_binds 3000`.

**Exit criteria (option a):** `/webmail` loads, JMAP calls via socket complete, `ss -lntp | grep 3000` empty. (Option c): no `:::3000` anywhere — must remain `127.0.0.1:3000`.

**Rollback:** systemd drop-in reverts `ExecStart` and `Environment=HOSTNAME=127.0.0.1 PORT=3000`.

---

## Step 6 — MariaDB → Unix socket + skip-networking (Wave C)

**Dependencies:** Step 4 (panel-api must already be on a socket; we want the DB switch to not be coupled to panel-api's binding issues). **Model tier:** default. **Branch:** `m25/step-6-mariadb-socket`.

**Context brief:**
MariaDB Debian default install; `/var/run/mysqld/mysqld.sock` exists but install.sh uses TCP DSN. panel-api DSN built at `panel-api/internal/db/db.go:59-62` (`ToDriverDSN()`). install.sh line ~1283 writes `mysql://...@127.0.0.1:3306/...`. GORM's `go-sql-driver/mysql` supports `unix(/path/to/sock)` format.

**Tasks:**
1. **Pre-flight grep** — scan whole repo for DB DSN usage:
   - `grep -rn 'tcp(127.0.0.1:3306)' .`
   - `grep -rn 'localhost:3306' .`
   - `grep -rn 'DB_HOST=' .`
   - `grep -rn '127.0.0.1:3306' install/ panel-api/ panel-agent/`
   Every hit gets enumerated in the step's PR description. **Must include `install/kratos.yml.tmpl:7`** — Kratos has its own DB DSN (per review F-C-1). If this isn't updated atomically with the panel-api DSN + skip-networking, Kratos crash-loops on every fresh install and every `jabali update -f`.
2. **Kratos DSN verification gate** — before committing Kratos DSN change, confirm Kratos v26.2.0 accepts `unix(...)` format (it uses the same `go-sql-driver/mysql`, but pin the version in `go.mod`). Run: `kratos migrate sql -e --config <test-config-with-unix-dsn>` against a scratch MariaDB + Unix socket. If accepted, proceed. If rejected, STOP: M25 cannot complete without this; escalate (possibly renumber Step 6 to be a blocker for Step 2+3). Per review F-M-3.
3. **DSN parser extension** — `panel-api/internal/db/db.go:ToDriverDSN()` already handles `mysql://` URLs. Extend to accept `unix:///path/to/mysqld.sock?db=jabali_panel` or equivalent. Output to the Go driver: `user:pass@unix(/var/run/mysqld/mysqld.sock)/jabali_panel?charset=utf8mb4&parseTime=True&loc=UTC`.
4. **install.sh — panel-api DSN** — change the DSN write at line ~1283 to socket format. Keep the existing TCP DSN around as a commented-out fallback for rollback.
5. **install.sh — Kratos DSN** — edit the Kratos config renderer (the block that writes `/etc/jabali-panel/kratos.yml` from `install/kratos.yml.tmpl`). The template's `dsn:` line must switch to `mysql://{{.KratosDatabaseUser}}:{{.KratosDatabasePassword}}@unix(/var/run/mysqld/mysqld.sock)/{{.KratosDatabaseName}}?parseTime=true&multiStatements=true`. Verify the rendered file after install.
6. **my.cnf drop-in** — install.sh writes `/etc/mysql/mariadb.conf.d/99-jabali-skip-networking.cnf` with `[mariadbd]\nskip-networking\nskip-name-resolve`.
7. **migration tool** — `panel-api/cmd/migrate/` (wherever golang-migrate runs) also needs the new DSN. Verify it handles `unix(...)` format — it does, because golang-migrate-mysql uses the same driver.
8. **Every test fixture** — testcontainers in integration tests MUST still use TCP (they run inside containers without access to the host's MariaDB socket). Keep them on TCP against their container. Only the production install flips to socket.
9. **`jabali update` path** — restart order: stop panel-api + Kratos → reload MariaDB (pick up skip-networking) → start Kratos + panel-api on new DSNs. Document in runbook. Kratos MUST restart BEFORE panel-api (panel-api's Kratos client tries to reach admin API on startup; if Kratos isn't up, panel-api's own startup stalls).
10. Full test pass against a fresh install with socket-only DSN — MUST include BOTH panel-api AND Kratos health endpoints. Run `kratos migrate status --config /etc/jabali-panel/kratos.yml` and assert no pending migrations (confirms Kratos can reach its DB).

**Exit criteria:** `ss -lntp | grep 3306` empty. panel-api + migrate tool both talk to MariaDB over the socket. `mariadb -u panel_api` from a shell (with correct socket auth) works.

**Rollback:** revert commit; remove `/etc/mysql/mariadb.conf.d/99-jabali-skip-networking.cnf`; change DSN back.

---

## Step 7 — Stalwart tighten + audit for other `:::` binds (Wave D)

**Dependencies:** Step 6. **Model tier:** default. **Branch:** `m25/step-7-stalwart-tighten`.

**Context brief:**
Stalwart (v0.16.0) doesn't support Unix sockets. Current apply-plan at `install/stalwart/apply-plan.json.tmpl` declares SMTP 25/465/587 on `0.0.0.0` (correct, public), IMAP 993 on `0.0.0.0` (correct, public), JMAP 8446 on `127.0.0.1` (correct, private). I1 confirmed `:::8080` is Stalwart's **admin HTTP** listener — default `0.0.0.0:8080`, **not present** in the apply-plan, so it falls to the Stalwart default bind. `:::35181` is a transient/ephemeral internal listener with non-deterministic port. Both need explicit `NetworkListener` entries to override the default.

**Tasks:**
1. **Add `#admin-http` listener to `install/stalwart/apply-plan.json.tmpl`** — new `x:NetworkListener` object:
   ```json
   {
     "@type": "NetworkListener",
     "id": "admin-http",
     "bind": { "127.0.0.1:8080": true },
     "protocol": { "@type": "Http" },
     "tls": { "@type": "Disabled" }
   }
   ```
   Confirm the exact `protocol`/`tls` shape with `stalwart-cli config export | jq '.listeners'` on the live VM (the admin listener may have a different protocol discriminator in v0.16.0 — use the exported value verbatim).
2. **Add a pinned listener for the :35181 ephemeral** — pick a stable port (e.g. `127.0.0.1:35181` or better, pick an unreserved high port like `127.0.0.1:18181` and document). Same `NetworkListener` shape. Rationale: without an explicit `bind`, Stalwart picks a random free port at startup which breaks firewall-whitelisting and any health-check.
3. **install.sh verification** — after `stalwart-cli apply` runs in the Stalwart install block, assert:
   - `verify_no_all_interface_binds 8080` + `verify_no_all_interface_binds <pinned-35181-replacement>`
   - Broader sweep: `ss -lntp -e '!( sport = :80 or sport = :443 or sport = :25 or sport = :465 or sport = :587 or sport = :993 or sport = :995 or sport = :4190 or sport = :22 or sport = :53 )' | grep -E '0\.0\.0\.0|\[::\]:'` — must produce no output on a healthy install.
4. **Audit for OTHER `:::`/`0.0.0.0` binds** — grep repo for `"0.0.0.0"`, `"::"`, `":::"` in `install/**/*.{yml,yaml,toml,json,tmpl}`. Every hit that's not a documented public-facing service becomes a Step 7 finding. Document in the PR description.
5. Restart Stalwart, verify on live VM.

**Exit criteria:** every localhost-only Stalwart listener binds `127.0.0.1` explicitly; every service-wide `ss -lntp` assertion in install.sh passes; no `0.0.0.0` or `[::]:` lines appear on non-public ports.

**Rollback:** revert the config template changes.

---

## Step 8 — E2E + ADR-0050 + runbook + BLUEPRINT update (Wave D)

**Dependencies:** Steps 1-7 all merged to `m25/unix-sockets`. **Model tier:** default. **Branch:** `m25/step-8-ship`.

**Context brief:**
Everything is converted. Time to E2E and document. ADRs at `docs/adr/`. Runbooks at `plans/m*-runbook.md`. Changelog at `docs/BLUEPRINT.md`.

**Tasks:**
1. **Playwright E2E `panel-ui/tests/e2e/sockets.spec.ts`** — full-stack flow on the test VM:
   - Login via Kratos (public flow through nginx → unix socket Kratos).
   - Admin actions via panel-api (unix socket).
   - Webmail opens via Bulwark (unix socket if Step 5 went option-a; TCP if option-c).
   - `ss -lntp` on VM via SSH asserts the expected bind state.
2. **`docs/adr/0050-unix-sockets.md`** — captures the 7 architecture decisions from this plan + the fallback logic for Kratos (socket-vs-127.0.0.1) + Bulwark (option-a-vs-c). Include the `jabali-sockets` group design + RuntimeDirectory pattern + reversibility via systemd drop-in.
3. **`plans/m25-unix-sockets-runbook.md`** — day-2 ops:
   - Rollback procedure per service (systemd drop-in templates).
   - Troubleshooting: "nginx 502" → check socket permission + group membership.
   - Monitoring: the new `/_health/<service>` nginx location pattern.
   - LLMNR override for LAN-heavy environments.
   - Stalwart port audit: explicit map of every port → what-it-is → expected-bind.
4. **`docs/BLUEPRINT.md`** updates:
   - Add `### 4.X Localhost Backend Hardening (M25)` section under Reconciler.
   - Add `### M25: Unix Sockets (SHIPPED)` milestone under M24.
   - Changelog row: `| M25: Unix sockets + bind tightening | <date> | <tip SHA>`.
5. **Memory** — new entry `project_m25_unix_sockets.md` linked from `MEMORY.md`. Summary: merged 2026-NN-NN; Kratos admin no longer internet-reachable; Bulwark conversion status.

**Exit criteria:** E2E green; merge `m25/unix-sockets` → `main` is a clean fast-forward; `ss -lntp` on fresh install matches the expected bind state for every service.

---

## Dispatch guidance

- **Wave A (Step 1)** — dispatcher executes inline. Setup is small and spans install.sh + systemd — no sub-agent handoff.
- **Wave B (Steps 2/3)** — serial. Step 2 discovers whether Kratos supports Unix sockets; Step 3 follows that answer.
- **Wave C (Steps 4/5/6)** — serial. Each touches install.sh + systemd + at least one Go file; concurrent work collides.
- **Wave D (Steps 7/8)** — serial. Step 7's audit may surface findings that Step 8's docs must reflect.

## Verification gate between every wave

Before merging a wave's branches into `m25/unix-sockets`:
1. `go test ./...` across panel-api + panel-agent.
2. `npm run build` + `npm run test` in panel-ui.
3. Fresh install on clean VM: `./install.sh --debug` succeeds end-to-end; full `ss -lntp` matches the expected state for the waves completed so far.
4. `mcp__gitnexus__detect_changes` confirms the change set matches the step's declared scope.

## Open questions — to resolve before or during Wave A

1. **Kratos v26.2.0 Unix socket support for its HTTP listener** — resolved at Step 2's verification gate. Plan has both paths; no blocker.
2. **Kratos v26.2.0 Unix socket support for its MariaDB DSN** — resolved at Step 6's verification gate (per review F-M-3). Plan assumes it works via `go-sql-driver/mysql`; escalation path if not.
3. **Bulwark custom server wrapper complexity** — resolved at Step 5's decision gate. Plan has fall-back.
4. **nginx `proxy_pass http://unix:...:/` syntax exact form on Debian trixie nginx 1.26** — ✅ resolved at pre-dispatch I2. Adopted Form D: `upstream u { server unix:/path; } ... proxy_pass http://u/;`.
5. **Stalwart port 8080 + 35181 identity** — ✅ resolved at pre-dispatch I1. `:8080` is the Stalwart admin HTTP listener (default `0.0.0.0:8080`, NOT in apply-plan); Step 7 adds an explicit `#admin-http` NetworkListener bound to `127.0.0.1:8080`. `:35181` is transient/ephemeral; pin it in Step 7 via a dedicated NetworkListener.
6. **LLMNR drop-in removes the `:5355` listener entirely** — verify at Step 1. If systemd-resolved keeps a listener even with `LLMNR=no`, document and accept.
7. **If a future `bulwark update` (upstream) overwrites `server-unix.js`** — build an install.sh smoke that re-installs `server-unix.js` every update. Flag in runbook.
8. **ADR-0049 was claimed by M24 (shipped 2026-04-22 per memory `project_m24_ip_manager.md`).** M25's next unused ADR is 0050. Confirmed.
9. **phpMyAdmin DB TCP connection after `skip-networking`** — phpMyAdmin (M7) connects to MariaDB. Does it use TCP or socket? If TCP, Step 6 must also update its connection config or it breaks. **Verification task folded into Step 6 task 1's pre-flight grep** (scan for `3306` anywhere in the repo, not just panel-api).
10. **Kratos admin DSN for its OWN database operations** — Kratos has two DB-touching code paths: the main `dsn:` config for its ORM, AND the `kratos migrate sql` subcommand for schema migrations. Both must use the same DSN format. Step 6 task 10 validates both paths.

## Post-review status

**Review date:** 2026-04-23 (opus/architect adversarial pass).
**Review verdict:** GO-WITH-FIXES → all 3 CRITICAL + 5 HIGH findings folded in.

**Dispatch status (2026-04-23):** ✅ **READY**. Both pre-dispatch investigations (I1 Stalwart ports + I2 nginx syntax) completed on live VM 10.0.3.13 + Debian trixie nginx 1.26.3. Findings folded into the top "Pre-dispatch investigations — RESOLVED" section and Steps 3/4/5/7. Wave A (Step 1) dispatchable now.
