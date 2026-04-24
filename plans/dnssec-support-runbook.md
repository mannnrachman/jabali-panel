# DNSSEC runbook

Per-domain DNSSEC signing via pdnsutil shell-out. Admin + user UI at
`/jabali-admin/dnssec` and `/jabali-panel/dnssec`. See ADR-0057 for the
decision record.

## Architecture

```
 UI (table, Switch, DS modal)
  │
  ▼ PUT /domains/:id/dnssec { enabled: true|false }
 panel-api (domain_dnssec.go)
  │  1. auth (admin or owner)
  │  2. agent.Call("dns.dnssec_enable" | "dns.dnssec_disable", {domain_name})
  │  3. on success: UpdateDNSSECEnabled + ReplaceAll(keys)
  │
  ▼ agentwire over unix socket
 panel-agent (commands/dns_dnssec_*.go)
  │
  ▼ pdns.SecureZone / DisableDNSSEC / ListKeys / ExportZoneDS
 pdnsutil (shell out)
  │
  ▼ PowerDNS authoritative backend
```

**Database is truth for "enabled"**, PDNS is truth for keys.
The `domain_dnssec_keys` table is a cache refreshed on every GET when
`dnssec_enabled = 1` (single pdnsutil query, cheap).

## Algorithm + params

- Default: ECDSAP256SHA256 (algorithm 13) per RFC 8624 §3.1 current best practice.
- NSEC3: `1 0 10 ab` (SHA-1, opt-out off, 10 iterations, salt "ab") matching
  RFC 9276 guidance.
- Single CSK: `pdnsutil secure-zone` defaults to one combined signing key;
  fine for a managed panel that rolls keys opportunistically.

## Operator tasks

### Enable DNSSEC on a single zone (CLI)

```bash
ssh root@host
pdnsutil secure-zone example.com.
pdnsutil set-nsec3 example.com. "1 0 10 ab"
pdnsutil rectify-zone example.com.
pdnsutil show-zone example.com.
pdnsutil export-zone-ds example.com.
```

The UI does the same four calls in sequence; prefer the UI.

### Disable DNSSEC

```bash
pdnsutil disable-dnssec example.com.
```

The UI does this and then clears the domain's rows in `domain_dnssec_keys`.

### Verify in the wild

```bash
# Chain of trust:
delv example.com
dig +dnssec +cd example.com | grep ad
# Specific DS at the parent:
dig @<parent-ns> example.com DS +short
```

`delv` returns "; fully validated" on success.

### Rollover (emergency KSK swap)

```bash
pdnsutil add-zone-key example.com. ksk active=no
# verify tag in `pdnsutil show-zone`
pdnsutil activate-zone-key example.com. <NEW_TAG>
pdnsutil deactivate-zone-key example.com. <OLD_TAG>
# wait for propagation + publish new DS
pdnsutil remove-zone-key example.com. <OLD_TAG>
```

Panel UI does not expose rollover — use CLI on the host.

## Troubleshooting

**PUT returns 502 `agent_error`.** Check agent logs
(`journalctl -u jabali-panel-agent`) and the panel-api log for the exact
pdnsutil stderr line. Common: zone not in `pdns.conf` backend or not yet
provisioned by the reconciler.

**Enabled but "Keys" column shows provisioning…**. Refresh the page; the
cache updates on GET. If persistent, `pdnsutil show-zone <zone>` on the
host — if it shows no keys the initial `secure-zone` failed silently.

**DS modal empty.** The parent zone has not received the DS yet. Check
`pdnsutil export-zone-ds <zone>` on host — the authoritative side is
independent of publication at the registrar.

## Files

- Migration: `panel-api/internal/db/migrations/000069_*.sql`
- Models: `panel-api/internal/models/domain_dnssec_key.go` + field additions in `domain.go`
- API: `panel-api/internal/api/domain_dnssec.go`
- Agent: `panel-agent/internal/commands/dns_dnssec_*.go`, `panel-agent/internal/pdns/dnssec.go`
- UI hook: `panel-ui/src/hooks/useDNSSEC.ts`
- UI component: `panel-ui/src/components/dnssec/DNSSECTable.tsx`
- Admin page: `panel-ui/src/shells/admin/dnssec/AdminDNSSECPage.tsx`
- User page: `panel-ui/src/shells/user/dnssec/UserDNSSECPage.tsx`

## Not in scope

- Automatic DS publication to registrar (ADR-0057 §decision 4). Would require
  per-registrar API integration — consider after M-registrar-api lands.
- Panel-side key rollover UI. Emergency rollovers are rare and CLI-safe;
  UI complexity not justified.
- M6.5 phases/ registry integration — the registry is dead code today,
  convergence runs inline in the PUT handler.
