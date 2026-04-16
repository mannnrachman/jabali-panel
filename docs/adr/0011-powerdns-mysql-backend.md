# 0011 — PowerDNS with MySQL backend

## Status
Accepted — 2026-04-16

## Context
An authoritative nameserver is required for hosted domains. PowerDNS is featureful, supports MySQL backend, and has a REST API (though we don't use it here). The panel owns the DNS database, not PowerDNS independently.

## Decision
Authoritative nameserver = PowerDNS 4.9 on the local node. Backend = separate `jabali_pdns` MariaDB database. The panel-agent writes DNS records and domainmetadata to pdns tables in the SAME transaction as the panel-db write. Serial number is incremented on every successful push.

## Consequences

### Positive
- Unified DNS data (lives in DB, not zone files)
- Atomic transactions (panel row + pdns row commit together or both roll back)
- Standard backend (MySQL); compatible with secondary nameservers
- REST API available if needed later

### Negative
- PowerDNS is a separate service (operational surface)
- Two databases (panel_db + jabali_pdns) must stay in sync
- Serial management is manual (no auto-increment in pdns)

### Neutral
- Requires transaction discipline to keep both DBs consistent

## Alternatives considered

- **Bind with zone files**: Rejected — zone file management is painful, no API
- **NSD (authoritative only)**: Rejected — no dynamic API, small ecosystem
- **PowerDNS with sqlite**: Rejected — single-writer, doesn't scale to multi-node

## References
- `panel-api/internal/reconciler/dns.go` — DNS record push logic
- `panel-api/internal/agent/` — agent-side PowerDNS transaction handling
- `docs/dns-secondary-nameserver.md` — secondary nameserver setup
