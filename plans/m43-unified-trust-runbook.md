# M43 Unified-Trust Runbook

**ADR:** 0089 · **Plan:** `plans/m43-unified-trust-model.md`

Operator-facing how-to for the post-M43 security stack. Covers the four most common questions and the rollback path.

---

## Q1. "Admin wants to permablock an IP — what's the command now?"

**Old way (M26):** `ufw deny from 1.2.3.4`. **Don't.** UFW UI no longer accepts IP rules.

**New way:** CrowdSec is the source of truth.

```sh
cscli decisions add \
  --ip 1.2.3.4 \
  --duration 2160h \
  --reason "permablock per ops"
```

- `2160h = 90d` — matches the M43 migration default.
- `--scope range --value 1.2.3.0/24` for CIDR.
- For a true permaban use `--duration 87600h` (10y); but most "permanent" bans on residential IPs go stale within months as DHCP reassigns.

**Verify:** `cscli decisions list -i 1.2.3.4` or in panel: `/jabali-admin/security?tab=trust` → IP verdicts.

**Test it would actually drop:** Trust tab → "Test bench" → enter IP → matrix shows every brain's verdict.

---

## Q2. "I just ran `ufw deny from <ip>` out of habit. What now?"

It still works at the kernel level (UFW is still installed), but:

- It bypasses CrowdSec entirely. CTI/CAPI enrichment will never see that IP again.
- The Trust tab will flag it under "UFW IP rules" with a `migrate` tag.
- It will NOT survive `jabali ufw migrate-ip-bans` — that CLI moves it to CrowdSec on next run.

**Fix it manually:**
```sh
ufw status numbered                   # find the rule number
ufw delete <num>
cscli decisions add --ip <ip> --duration 2160h --reason "perma block"
```

**Or batch via the migration CLI (handles all UFW IP rules at once):**
```sh
jabali ufw migrate-ip-bans --dry-run  # see what would migrate
jabali ufw migrate-ip-bans --no-cdn --yes
```

`--no-cdn` confirms the panel is not behind a CDN. Required when CrowdSec has no `trusted_ips:` configured (otherwise CDN POPs would get banned). See Q4.

---

## Q3. "Who dropped this packet?"

Five surfaces collapse to one event source.

**Pre-M43:**
1. `journalctl -u jabali-panel` — app-layer rejects.
2. `tail /var/log/nginx/<host>-error.log | grep limiting` — nginx limit_req.
3. `tail /var/log/nginx/<host>-access.log | grep ' 403 '` — nginx-bouncer 403s.
4. `cscli decisions list -i <ip>` — CrowdSec ban verdict.
5. `journalctl _SYSTEMD_UNIT=ufw.service` or `dmesg | grep '[UFW BLOCK]'` — UFW drops.

**Post-M43:**
- M14 event source `security.decision.fired` aggregates UFW BLOCK + nginx limit_req drops every 5 min and posts one envelope per window. Subscribe via Notifications → Events.
- For a single IP: Trust tab → Test bench → matrix shows the verdict from each brain at this exact moment.
- CrowdSec specifically: still `cscli decisions list` (the source of truth, not the aggregator).

---

## Q4. "Panel is behind Cloudflare/CDN. What changes?"

Without configuration, CrowdSec sees the CDN edge IP — the *real* attacker IP is in `X-Forwarded-For`, hidden from CrowdSec, AppSec, and nginx-bouncer. This breaks the entire authority hierarchy. M43 doesn't fix this — it surfaces the failure mode and adds a hard guard so you can't accidentally ban CDN POPs.

**Setup steps when CDN is in front:**

1. List your CDN's edge IPs (Cloudflare publishes these at `https://www.cloudflare.com/ips/`).
2. Add to `/etc/crowdsec/config.yaml`:
   ```yaml
   api:
     server:
       trusted_ips:
         - 173.245.48.0/20
         - 103.21.244.0/22
         # … rest of CDN ranges
   ```
3. Add `set_real_ip_from` directives to nginx so `$remote_addr` reflects the real client. The panel ships this for `cf_real_ip` if `cloudflare_real_ip=true` in `server_settings`.
4. Restart CrowdSec: `systemctl restart crowdsec`.
5. Verify: `cscli config show -o json | grep trusted_ips` — populated.
6. THEN run `jabali ufw migrate-ip-bans` (no `--no-cdn` needed — guard sees `trusted_ips` and proceeds).

If CDN is not in front, just pass `--no-cdn`:
```sh
jabali ufw migrate-ip-bans --no-cdn --yes
```

---

## Rollback

The migration is reversible from a JSON snapshot.

```sh
ls -l /var/lib/jabali/m43-ufw-snapshot.json
# {timestamp, reason, rules: [...]}

jabali ufw migrate-ip-bans --revert --yes
```

Effects:
- For each migrated rule, deletes the matching CrowdSec decision (`cscli decisions delete --ip <ip>`).
- Re-adds the original UFW rule with the same action/proto/port/from spec.
- Snapshot file is preserved (re-running `--revert` is idempotent / safe).

If the snapshot file is missing, revert can't run. The CrowdSec decisions are tagged `reason="ufw-migrated"`, so a manual cleanup is:
```sh
cscli decisions list -o json | jq -r '.[] | select(.reason=="ufw-migrated") | .value'
# review, then for each:
cscli decisions delete --ip <ip>
```

---

## Verification commands (post-migration)

```sh
# Should be 0 (post-migration UFW has no IP rules)
ufw status | grep -E ' from ' | grep -v 'Anywhere' | wc -l

# CrowdSec is the IP-trust source of truth
cscli decisions list -o human

# Trust tab loads
curl -sk -o /dev/null -w '%{http_code}\n' https://<panel>/jabali-admin/security?tab=trust
# 200

# Event source publishes when activity > 0
mariadb jabali_panel -e "SELECT event_kind, enabled FROM notification_event_settings WHERE event_kind='security.decision.fired';"
```

---

## What didn't change (worth re-stating)

- **CrowdSec firewall-bouncer** is still the kernel-level enforcer. nftables `inet crowdsec` table still holds the drop chain. M43 only changed where decisions are *authored*, not where they're *applied*.
- **Per-user egress firewall** (M34) is unchanged. Different threat, different cgroup match. Not part of the IP-trust hierarchy. See ADR-0084.
- **AppSec engine** (M27) still on `127.0.0.1:7422`. nginx-bouncer dials it in-band per request. Verdict feeds CrowdSec scenarios → bouncer chain.
- **PHP Defense / AppArmor / AIDE / Malware** are independent layers (in-process, MAC, FIM, malware-scanning respectively). No relation to IP trust.

---

## Operator FAQ

**Q: Can I disable CrowdSec and just run UFW like the old days?**
You can, but the Trust tab will flag every UFW IP rule, the test bench will show `crowdsec=unknown`, and you lose CTI/CAPI signal. Strongly discouraged.

**Q: My SSH (port 22) deny rule on UFW — does that still work?**
Yes. M43 only touched UFW IP rules (`from <ip>`). Port allow/deny rules are untouched.

**Q: Can I add multiple IPs at once via `jabali ufw migrate-ip-bans`?**
Yes — that's its job. Migrates every existing UFW IP rule in one pass. Idempotent (safe to re-run).

**Q: I want a 1-year ban, not 90 days.**
`cscli decisions add --duration 8760h --ip <ip>`. The 90d default in `migrate-ip-bans` is a rotation hint, not a policy.

**Q: Does this affect mail blocking (Stalwart)?**
No. Stalwart has its own RBL + Bulwark integration. M43 is HTTP-side IP trust only. Mail-side bans live in Stalwart's queue management.
