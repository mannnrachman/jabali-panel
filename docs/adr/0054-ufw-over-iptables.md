# ADR-0054: UFW over raw iptables/nftables for the host firewall

**Status:** Accepted (2026-04-24)
**Driven by:** Plan `plans/m26-security-tab.md` (M26 Step 1 — security foundation), CrowdSec firewall-bouncer chain layout audit (ADR-0053).

## Context

The panel needs a host firewall that:

1. Default-denies inbound, allows the panel + nginx + mail + SSH allow-list out of the box.
2. Survives an `install.sh` re-run idempotently (no rule duplication, no service flap).
3. Coexists cleanly with CrowdSec's firewall bouncer (ADR-0053), which inserts its own jump chain.
4. Exposes a stable rule-numbering surface so the M26 Security tab can present, add, delete rules to operators without leaking implementation details.
5. Ships on Debian out of the box, no third-party repo.

Three options on Debian:

| Option | Default-deny | Idempotency | Numbered surface | Coexists w/ CrowdSec bouncer | Default-installed |
|---|---|---|---|---|---|
| Raw `iptables` (legacy) | Hand-rolled scripts | Hand-rolled | Position-by-position, brittle | Yes — but operator owns chain insertion order | No (replaced by nftables on trixie) |
| Raw `nftables` | Hand-rolled scripts | Hand-rolled | Named rules + handles, but no UI sugar | Yes | Yes (default backend) |
| `ufw` | `default deny incoming`, one line | apt + idempotent CLI (`ufw allow N/tcp` re-run is a no-op) | `ufw status numbered` gives stable position numbers | Yes — bouncer uses its own chain `CROWDSEC_CHAIN` jumped from INPUT position 1; UFW's `ufw-user-input` numbered chain is untouched (verified in `plans/m26-step1-chain-spike.txt`) | apt: `ufw` |

UFW is a wrapper over both backends — on bookworm it sits over iptables; on trixie it sits over nftables (its `before.rules` / `after.rules` translate). The panel doesn't need to care which backend is active.

## Decision

UFW is the panel's host firewall. install.sh's `install_ufw()` function:

1. `apt install ufw`.
2. `ufw default deny incoming`, `ufw default allow outgoing`.
3. Allow-list (TCP):
   - `22` (SSH) — never break operator access.
   - `80, 443` (nginx public)
   - `8443` (panel-api edge — ADR-0014)
   - `25, 465, 587, 993, 995, 4190` (Stalwart SMTP/SMTPS/submission/IMAPS/POP3S/managesieve)
4. Idempotent enable guard:
   ```bash
   if ! systemctl is-active --quiet ufw; then
     ufw --force enable
   fi
   ```
   Bare `ufw --force enable` reloads the firewall mid-install — that interrupts in-flight TCP (apt mirror reuse, Stalwart's first start). The guard ensures enable runs exactly once.

UFW must be active BEFORE `install_stalwart` (Stalwart binds public mail ports during its first start; UFW reloading iptables mid-bind is the documented Stalwart race). UFW must be active AFTER `setup_certbot` (certbot HTTP-01 reaches port 80 before any rule is installed; with default-deny, the LE validation fails — UFW first allows 80, then enables, so the path is open from the first rule).

## Alternatives considered

- **Raw nftables.** No idempotent CLI for individual rule management. The panel's M26 Security tab would have to grep/regex `nft list ruleset` — fragile against nftables version drift. Rejected.

- **firewalld.** RHEL-native; not Debian default; would force a new dep on every panel install. Conflicts with Debian-recommended ufw on bookworm/trixie. Rejected.

- **No host firewall (rely on cloud security groups).** Panel ships to bare-metal and self-hosted VPS, not just IaaS. ADR-0050 + ADR-0053 + this ADR layer the defence in depth — kernel-level deny is the floor.

- **iptables-persistent + hand-written rules.** Loses every operator-friendly verb. The Security tab would have to expose raw iptables syntax to admins. Rejected.

## Consequences

- UFW's default `deny incoming` requires the allow-list to be installed BEFORE `ufw enable` runs in the same install. A reordering bug would lock out SSH.
- The Step 1 install allow-list is fixed — operator can edit via M26 Step 4 (UFW tab in admin Security page). M26 Step 2 ships the agent commands; Step 3 ships the API; Step 4 ships the UI.
- Rule numbering is stable across reboots (UFW persists state in `/etc/ufw/`). Panel can read `ufw status numbered` and present positions 1..N for delete operations.
- No tracker/conntrack tuning here — defaults suffice for panel-scale traffic. If we hit conntrack limits later, the fix is a sysctl drop-in, not a UFW change.
- Operator overrides (e.g. permit a specific source IP to SSH) can be added via `ufw allow from <ip> to any port 22` — Step 4 UI exposes this. Manual `/etc/ufw/before.rules` edits are not blocked but not officially supported.
