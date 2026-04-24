# ADR-0057: Per-domain DNSSEC via pdnsutil shell-out

**Status:** ACCEPTED (2026-04-24).
**Related:** ADR-0011 (PowerDNS MySQL backend), ADR-0002 (DB-as-truth).

## Context

Operators want to enable DNSSEC signing per domain and hand the resulting DS
record set to their registrar. PowerDNS 4.9 backs every jabali zone with a
MySQL-schema (`jabali_pdns`). PowerDNS signs zones using `cryptokeys` +
`domainmetadata` tables; it also ships a first-party CLI — `pdnsutil` — that
mutates both.

## Decisions

### 1. Shell out to `pdnsutil`, don't write `cryptokeys` directly

Options considered:

- (a) Direct SQL into `cryptokeys` / `domainmetadata` from the agent's existing
      pdns client, same transaction idiom as zone records.
- (b) Shell out to `/usr/bin/pdnsutil` (chosen).

Rationale: `pdnsutil secure-zone` does more than one INSERT — it generates
private key material with the correct algorithm, seeds NSEC3 parameters,
marks the KSK active, and rectifies the zone serial. Reimplementing that
in Go duplicates the crypto-generation step and drifts whenever PowerDNS
changes defaults. `pdnsutil` runs locally, talks to the same MySQL
backend, and is the first-party supported surface.

M25 unix-socket lockdown stands: the agent already runs as root and can
exec `pdnsutil` without opening any new loopback port.

### 2. Algorithm 13 (ECDSAP256SHA256) only in v1

`pdnsutil secure-zone` picks ECDSAP256SHA256 (algorithm 13) as of PowerDNS
4.9. Shorter keys, smaller DNSKEY + DS records than RSA, broad registrar
support. We pass no algorithm override — whatever PowerDNS ships with is
what we sign with.

Operators who need a different algorithm for registrar compatibility can
disable DNSSEC through the UI, edit the PDNS config, and re-enable. That
flow is documented in the runbook. A UI algorithm selector is out of scope
for v1; it materially increases complexity and would risk suggesting
algorithms the registrar won't accept.

### 3. Store intent + cache; never the private key

The `domains` table gains two columns:

- `dnssec_enabled` — boolean operator intent.
- `dnssec_enabled_at` — nullable timestamp of the most recent enable.

A new table `domain_dnssec_keys` caches `(key_tag, key_type, algorithm,
public_key, active)` rows produced by `pdnsutil show-zone`. The cache is
advisory — the API always re-reads from PDNS on `GET /domains/:id/dnssec`
and upserts the cache. Private key material never leaves
`/var/lib/powerdns/`.

### 4. DS retrieval is read-through, never cached

`GET /domains/:id/dnssec/ds` invokes the agent, which invokes
`pdnsutil export-zone-ds`, which prints the DS records for each active
KSK. Caching the DS bytes would go stale the moment an operator rotates
the KSK; the window is measured in seconds and the cost of a stale DS at
the registrar is days of validation failure. Trade the 10-30 ms shell-out
for correctness.

### 5. NSEC3 default `1 0 10 ab`

After `secure-zone`, the agent runs `pdnsutil set-nsec3 <zone> '1 0 10 ab'`
(SHA-1, opt-in, 10 iterations, two-byte salt). This is the PowerDNS
recommended modern default. A small subset of registrars reject NSEC3 in
opt-in mode; the runbook documents `pdnsutil unset-nsec3 <zone>` as the
escape hatch for those cases. The UI does not expose this knob in v1.

## Consequences

- Adding a dependency: `pdnsutil` must be present at `/usr/bin/pdnsutil`
  (ships with the `pdns-server` Debian package install.sh already installs).
- First enable is visible: zone serial bumps, TTLs honour the cache for
  existing recursors.
- Rollback is clean: `pdnsutil disable-dnssec` removes keys + rectifies.
  The DB cache is populated from the next reconcile; until then, the last
  observed keys stay in `domain_dnssec_keys` until the API's GET refreshes
  them. Nothing else in the stack references the cache.
- The UI shows only public information. Private keys stay in
  `jabali_pdns.cryptokeys` which only the PDNS user and `pdnsutil` can
  read.

## Follow-up

- A future feature could surface key-tag, DS set, and key-rollover state
  on a per-zone detail page; v1 only exposes on/off + "Copy DS".
- If PowerDNS moves `pdnsutil` to an HTTP endpoint in a later release, we
  swap the shell-out for the HTTP call without changing the UI contract.
