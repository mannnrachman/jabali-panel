# M49 — Unified audit log (operate / verify / retain)

ADR-0106. Append-only `audit_events`, dedicated `jabali:audit:queue`
Redis stream + a single-writer hash-chain consumer. This covers the
non-obvious operator actions: integrity verification, a chain break,
retention, and incident querying.

---

## 1. Verify chain integrity (tamper-evidence)

**Symptom** — routine integrity check, or you suspect audit tampering
/ corruption.

**Diagnosis / Fix**
```bash
jabali audit verify
# chain OK — <N> sealed rows verified (<M> total rows)        → exit 0
# CHAIN BROKEN at row <ULID> (after <K> sealed rows verified) → exit 1
```
`verify` replays `sha256(prev || RS || canonical(row))` over every
sealed row in `ts ASC, id ASC` order. Rows with a NULL `row_hash`
(M46 `db_admin_audit` fold-in / Redis-down-fallback rows the consumer
hasn't sealed yet) are **skipped** — they are not chain members and
are not breaks.

**Verification** — exit 0 + the sealed-row count is non-decreasing
across runs. Wire `jabali audit verify` into a daily timer/monitor;
non-zero exit = page.

**A real CHAIN BROKEN** means a sealed row's content or `row_hash`
changed after sealing (DB tamper, restore from an inconsistent
backup, or a deliberate change to `canonical()`/`computeRowHash` —
which is a chain break *by design* and must be versioned). Treat as a
security incident: preserve the DB, diff the broken row id against
backups, escalate. **Never** "fix" by re-sealing — that destroys the
evidence the chain exists to provide.

---

## 2. Chain not advancing / unsealed backlog

**Symptom** — `verify` shows the sealed count flat while activity
continues, or many rows have NULL `row_hash`.

**Diagnosis**
```bash
systemctl is-active redis-server jabali-panel        # consumer needs Redis
journalctl -u jabali-panel | grep -i 'audit:'        # consumer/recorder logs
mysql -N -e "SELECT COUNT(*) FROM jabali_panel.audit_events WHERE row_hash IS NULL;"
```
Recorder is **fail-open-but-recorded**: when Redis is down it writes
rows straight to the DB with NULL hashes (audit is never lost). The
single-writer consumer back-fills those via its sweep when Redis
recovers (startup + every idle tick).

**Fix** — restore Redis + restart `jabali-panel`; the consumer's
sweep chains the backlog automatically. No manual SQL.

**Verification** — `WHERE row_hash IS NULL` count trends to ~0 (only
the M46 fold-in historical rows stay NULL by design) and `jabali
audit verify` sealed-count climbs.

---

## 3. Retention prune

**Symptom** — `audit_events` growth; retention policy enforcement.

**Fix**
```bash
jabali audit prune --days 365      # bounded DELETE of rows older than N days
```
Run by a timer for unattended retention (mirrors the M30
backup-retention pattern). The prune **records its own
`audit.retention.prune` event** (cutoff, pruned count, days) — never
a silent selective delete. ADR-0106's eventual target is a
whole-partition DROP; the bounded DELETE is the honest v1.

**Verification** — output prints the pruned count + cutoff; a new
`audit.retention.prune` row appears in `jabali audit query`.

---

## 4. Incident querying

```bash
jabali audit query --limit 200                 # recent, tabular
jabali audit query --q automation.token --json # filter + machine-readable
```
Admin/forensics view (all rows). End users see only their own feed at
`/api/v1/me/activity` (subject scope enforced server-side — never a
client filter). Bodies are never recorded; `meta` is structured
context only.
