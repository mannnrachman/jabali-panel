# ADR-0053: CrowdSec over fail2ban for behaviour-based IP blocking

**Status:** Accepted (2026-04-24)
**Driven by:** Plan `plans/m26-security-tab.md` (M26 Step 1 — security foundation), Debian trixie packaging audit on test VM 192.168.100.13.

## Context

The panel needs server-side intrusion prevention for the public surface (panel-api on `:8443`, nginx on `:80/:443`, Stalwart SMTP/IMAPS, SSH `:22`). Two mature options exist on Debian:

| Option | Model | Threat-intel | Bouncer model | Packaging |
|---|---|---|---|---|
| fail2ban | Local log scan + iptables rules | None (each host scans alone) | iptables/firewalld direct | apt: `fail2ban` (Debian repo) |
| CrowdSec | Local log scan + LAPI + Central API (opt-in) | Community-shared decisions feed | Decoupled bouncers (firewall, nginx, cloudflare, …) | apt: `crowdsec` + `crowdsec-firewall-bouncer-{iptables,nftables}` (upstream packagecloud repo — see Packaging note below) |

fail2ban edits iptables INPUT directly. CrowdSec separates *detection* (the agent — log parsing → decisions in a local SQLite/LAPI) from *enforcement* (a bouncer — pulls decisions from LAPI and applies them to a firewall, web server, etc.). The panel will need to query and manipulate decisions live (admin Security tab) — that's the Local API surface (`cscli` against the LAPI socket), which fail2ban does not have a clean equivalent for.

### Packaging — upstream repo, not Debian stock

VM smoke on trixie 192.168.100.13 surfaced two packaging facts that drove a deviation from the original "use Debian-repo crowdsec" assumption:

1. **Trixie ships CrowdSec 1.4.6**, not 1.5+. CrowdSec 1.4.x's `csconfig.LocalApiServerCfg` has NO `listen_socket` field — added in 1.5.x. Writing `api.server.listen_socket: /run/crowdsec/api.sock` on a 1.4.x install yields fatal yaml unmarshal at startup. ADR-0050 requires socket binding (Stalwart owns 127.0.0.1:8080), so 1.4.x is non-viable.
2. **Debian's `crowdsec-firewall-bouncer` is a single meta-package**, not the variant pair `-iptables` / `-nftables`. The bouncer-detection logic (lsb_release-driven) only works against upstream packaging.

Both facts force the upstream packagecloud repo (`https://packagecloud.io/crowdsec/crowdsec/`) as a hard dependency. install.sh's `add_crowdsec_apt_source()` writes the apt source + signed-by keyring at `/etc/apt/keyrings/crowdsec.gpg`, then `install_crowdsec` `apt install`s `crowdsec` (1.7.x at time of writing) plus the detected bouncer variant.

The panel's bouncer choice for M26 Step 1 is the firewall bouncer (drops connections at the kernel before they hit nginx or stalwart). On trixie we prefer `crowdsec-firewall-bouncer-nftables` (Debian 13 default backend) with apt-cache fallback to `crowdsec-firewall-bouncer-iptables`. On bookworm (Debian 12) we use the iptables variant.

## Decision

CrowdSec is the panel's behaviour-based blocker. fail2ban is not installed.

CrowdSec runs as the upstream `crowdsec` systemd service. Its Local API is bound on a Unix socket per ADR-0050: `/run/crowdsec/api.sock` (mode `0660`, owner `root`, group `jabali`). A systemd drop-in at `/etc/systemd/system/crowdsec.service.d/10-jabali-socket.conf` declares `RuntimeDirectory=crowdsec` + `RuntimeDirectoryMode=0750` + `Group=jabali` so the panel-agent (running as root) and panel-api (running as group `jabali`) both reach the socket via `cscli`, which reads `/etc/crowdsec/local_api_credentials.yaml` for the path.

The bouncer install is debian-release-aware: trixie (13+) prefers `crowdsec-firewall-bouncer-nftables`, falls back to `crowdsec-firewall-bouncer-iptables` if the nftables variant is missing. Bookworm (12) installs the iptables variant unconditionally. Detection runs at install time via `lsb_release -rs` + `apt-cache show`.

## Alternatives considered

- **fail2ban only.** Direct iptables edits make UFW rule numbers shift unpredictably (no separate chain). No native admin-API. No community threat-intel. No bouncer/detection split — extending to nginx-level blocking requires hand-rolled jail definitions. Rejected.

- **CrowdSec without firewall bouncer (only nginx-bouncer).** Pure-nginx blocking is application-layer; SSH brute-force traffic still reaches sshd. Firewall bouncer is the floor. Per-vhost nginx bouncer can layer on later if/when needed.

- **CrowdSec on TCP loopback (`127.0.0.1:8080`).** Conflicts with Stalwart admin-http (ADR-0050 pins Stalwart to `127.0.0.1:8080`). Unix socket is also the right ADR-0050 default — every internal HTTP backend that supports a socket uses one.

- **Centrally-hosted detection (CrowdSec Console / CrowdSec SaaS).** Out of scope for M26 Step 1. Operator can run `cscli console enroll` later if they want centralised observability — no install-time dependency.

## Consequences

- One additional `apt` source IS added: CrowdSec upstream repo at `https://packagecloud.io/crowdsec/crowdsec/`. Signing key at `/etc/apt/keyrings/crowdsec.gpg`. Source line at `/etc/apt/sources.list.d/crowdsec.list`. install.sh's `add_crowdsec_apt_source()` is idempotent (skips fetch + write if both files exist + match).
- LAPI socket is a new cross-process boundary. Panel-agent (root) and panel-api (jabali) both call `cscli`; cscli authenticates against LAPI via `local_api_credentials.yaml`. Threat surface narrowed: the socket is `0660 root:jabali`, not world-reachable.
- UFW rule layout in M26 Step 1 must NOT be disturbed by the bouncer. The firewall bouncer creates its own `CROWDSEC_CHAIN` referenced by a jump from `INPUT` position 1 — UFW's `ufw-user-input` chain stays untouched. This invariant is verified by the M26 Step 1 spike artefact (`plans/m26-step1-chain-spike.txt`).
- ModSecurity (ADR-0055) handles application-layer (HTTP request body) blocking; CrowdSec firewall-bouncer handles network-layer source-IP blocking. The two are complementary, not overlapping.
- M26 Step 2+ wires panel-agent NDJSON RPCs around `cscli`. Per ADR-0001, the panel-api never invokes `cscli` directly — every privileged operation flows through the agent.
