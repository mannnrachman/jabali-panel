# Plan: M6 — Email via Stalwart Mail Server

**Status:** Draft (2026-04-21). Ready for adversarial review, then dispatch.
**Owner:** shuki
**Scope:** M6 per `docs/BLUEPRINT.md` §4.? (currently listed as PLANNED).
**Depends on:** M1 ✅ (users), M2 ✅ (domains + nginx), M4 ✅ (DNS zones via PowerDNS), M7 ✅ (shadow-password + SSO pattern), M9.5 ✅ (per-user slices — Roundcube PHP app consumes this), M20 ✅ (Kratos — webmail SSO handoff).
**Next migration:** `000054_create_mailboxes` (single migration groups 4 new columns + 2 tables; see step 1).
**Next ADRs:** `0041-m6-mail-storage-rocksdb`, `0042-m6-sql-directory-mailboxes-table`, `0043-m6-dkim-key-rotation-policy`, `0044-m6-imap-migrate-deferred-to-m15`.
**Working directory:** `/home/shuki/projects/jabali2-c` — branch `m6/email-stalwart` (this plan lives on that branch).
**Baseline commit at plan time:** `44fdafc` (two commits ahead of `main`; rebase before dispatch).

---

## 0. Operating assumptions (read before you start any step)

### Conventions inherited from this repo

- **Commit rhythm:** one commit per step. **Branch first, PR via Gitea** (primary remote is `origin` = Gitea at `git.linux-hosting.co.il`, not GitHub — `gh` is linked to the mirror only). Per `.claude/hooks/block-agent-commit-main.sh`, no direct commit to `main` from any agent. Dispatcher merges. Conventional commits (`feat`, `fix`, `refactor`, `docs`, `test`, `chore`).
- **CI:** Gitea Actions runs `.gitea/workflows/ci.yml` on every push + PR (3 parallel jobs: Go tests + vet, vitest, Playwright E2E). Do not push to a branch without ensuring the three checks go green before asking for review — `act_runner` is host-mode and sometimes restarts.
- **Go style:** `gofmt` + `go vet`; table-driven tests; `go test -race -count=1 ./...` must stay green. Handlers follow the `api.*HandlerConfig` injection pattern (see `panel-api/internal/api/databases.go`).
- **Migrations:** golang-migrate, both `.up.sql` and `.down.sql`. Schema defaults live in SQL, not Go. Down migration must not silently drop data; drop the new table only.
- **Agent wire:** NDJSON over UDS at `/run/jabali/agent.sock`, `Default.Register("<command>", handler)` pattern in `panel-agent/internal/commands/`. Response struct JSON tags must match what the panel unmarshals **verbatim** — see `~/.claude/projects/-home-shuki-projects-jabali2/memory/feedback_cross_boundary_contracts.md`. For every new command add a golden fixture in `testdata/` that both panel unmarshal + agent marshal round-trip (`panel-api/internal/agent/php_ext_contract_test.go` is the shape to copy).
- **Installer helpers:** `_log` / `_ok` / `_warn` / `_err` only in `install.sh` (see `feedback_install_sh_logger`). Extend the tiny-logger block if needed, don't invent helpers.
- **Installer is truth:** every required postcondition (systemd mask, perms, group membership) goes into `install.sh` (see `feedback_install_sh_is_truth`). No separate cutover CLIs that only run on the dev host.
- **Shared code that both panel-api and panel-agent import** goes at repo root `internal/<package>/` (Go internal-rule; `internal/phpext/`, `internal/cronvalidate/`, `internal/filesafe/` are the precedent). For M6 this means `internal/mailaddr/` (email-address canonicalisation + validation, used by both the API handler and the agent JMAP call) and `internal/dkim/` (keygen + DNS-record rendering).
- **HTTPS only:** the panel is https-only on `:8443`. Do not introduce plaintext or port-80-only paths. Stalwart's SMTP submission listener still lives on 465 (SUBMISSIONS, implicit TLS) + 587 (STARTTLS); port 25 accepts STARTTLS inbound.
- **Per-user execution:** anything that writes into `/home/<user>/...` runs as the domain-owning OS user inside `jabali-user-<user>.slice` via `systemd-run --uid=<user> --slice=...`. Do NOT wrap with `sudo -u` — that breaks cgroup accounting (ADR-0025).
- **Reconciler is the convergence engine:** API writes DB; reconciler reads DB and drives the filesystem + agent. Mailbox rows are inline best-effort per ADR-0013 (fast-path failure surfaced in the API response — the same shape as database users in M7). Domain-level email state (enabled, DKIM key present, DNS records published) is reconciled.

### What we are NOT doing in M6 (explicit out-of-scope)

- **Per-user Sieve/filtering UI** — Stalwart's Sieve backend is there; no UI. Defer to M6.1.
- **CalDAV / CardDAV** — Stalwart ships these in v0.16.0; separate milestone.
- **Mailing-list manager (Mailman-style).**
- **External MX failover / secondary MX host.**
- **Cluster mode (FoundationDB)** — single-node RocksDB only. This is a hosting panel, not a mailcluster.
- **Provider-external antispam** — rely on Stalwart's built-in spam-filter pipeline (FTRL-Proximal linear classifier + DNSBL lookups + greylist).
- **Stalwart's native auto-DNS** — Stalwart v0.16.0 can publish MX/SPF/DKIM/DMARC itself via its DNS management layer. **We disable this.** The panel's M4 PowerDNS code path is the single DNS source of truth (ADR-0002). ADR-0043 records the trade-off: one DNS writer, predictable DKIM rotation.
- **Stalwart's webadmin as end-user webmail** — the `webadmin` ships with v0.16.0 but is admin-facing. Real webmail is Bulwark 1.4.14 (decision recorded in ADR-0041).
- **IMAP migration (`imap-migrate`)** — Stalwart has a built-in importer, but first-class importer UX lives in M15. ADR-0044 pins this deferral and describes the CLI escape hatch (`stalwart-cli imap-migrate …`) for operators who need it day-one.

### Version pins (verified 2026-04-21)

- **Stalwart** `v0.16.0` (released 2026-04-20 — latest stable). Upstream install script at `https://github.com/stalwartlabs/stalwart/blob/main/install.sh`; we capture a SHA-256 of the tarball into `install/stalwart.sha256` (same shape as `install/kratos.sha256`).
- **Bulwark webmail** `v1.4.14` (latest stable; `https://github.com/bulwarkmail/webmail`). Next.js, JMAP-native, purpose-built for Stalwart. AGPL-v3 licensed — we ship unmodified as a network service (no linking, no fork), so the source-provision obligation is satisfied by Bulwark's own public repo. If we fork to patch, the panel's own repo publication obligation kicks in — ADR-0041 records the trade-off. Installed from source (`git clone` → `npm ci` → `npm run build` → `npm start`), pinned via `.bulwark-version` + SHA-256 of the tarball captured on first deploy.
- **Node.js** 20 LTS (panel-ui already requires ≥20 per `package.json`; `install.sh` installs NodeSource tarball if the host has no Node yet).

### Upstream behaviour that shapes the design

1. **v0.16.0 replaced its REST API with a JMAP management API** reachable through the same `/jmap` endpoint. All agent management calls (create account, set password, set quota, get usage) go via JMAP against `http://127.0.0.1:8446/jmap` with a service-account bearer.
2. **v0.16.0 SQL directory backend still supported** but filter queries must match on full email address (`alice@example.com`), not bare account name. Our `mailboxes.email_cached` column is a generated stored column (`CONCAT(local_part, '@', (SELECT name FROM domains d WHERE d.id = mailboxes.domain_id))`) to let Stalwart do indexed lookups without a join.
3. **v0.16.0 removed `smtp`, `imap`, and `memory` directory backends** — SQL is the only external directory we care about (LDAP also supported but not in scope).
4. **`aws-lc` + `rustls-platform-verifier`** replace `ring` and `webpki` in v0.16.0 — means the process reads the OS CA bundle (`/etc/ssl/certs/ca-certificates.crt`). Panel's self-signed cert for `mail.<domain>` is therefore a non-issue for outbound Stalwart→panel calls (Stalwart won't call the panel). For inbound, Stalwart's TLS certs come from certbot's `/etc/letsencrypt/live/<domain>/` paths (same ACME surface as M5; no duplicate cert plumbing).

---

## 1. What's in scope

A single hosting server runs SMTP (25/465/587) + IMAP (993) + JMAP (443, proxied to :8446) with per-domain mailbox hosting. Operators click **Email → Enable** on a domain and then create mailboxes inline. End users log into webmail at `https://<domain>/webmail/` (or `https://mail.<domain>/`) using Kratos session → Roundcube shim. DNS autoconfig (MX / SPF / DKIM / DMARC / `autoconfig.<domain>` / `_autodiscover._tcp.<domain>`) is injected into the domain's PowerDNS zone via the existing M4 dnscompile path.

### Storage architecture (recorded in ADR-0041)

- **Mail data** (messages, mailbox indices, full-text search, bayes state): RocksDB at `/var/lib/stalwart/` (Stalwart's default single-node store). Not MariaDB — Stalwart's data model doesn't map to SQL, and v0.16.0 dropped its in-memory/SQL-ish stores for message content.
- **Directory** (accounts, passwords, aliases, quotas, group memberships): Stalwart's **SQL directory backend** pointed at our existing `jabali_panel` MariaDB, with a new `mailboxes` table. Provisioning = one SQL transaction in the panel, plus a JMAP call to Stalwart (for cache-invalidation only — Stalwart re-reads SQL on every auth).
- **TLS certs:** reuse panel's existing certbot. No duplicate cert plumbing. Stalwart reads `/etc/letsencrypt/live/<domain>/fullchain.pem` and `privkey.pem` at bind time.
- **DKIM keys:** panel-owned, at `/etc/jabali-panel/dkim/<domain>.key` (0600 `jabali:jabali`), public key inserted into PowerDNS zone. Stalwart's DNS management is disabled. Rotation policy in ADR-0043.

### Password model (explicit — the blueprint input conflated two things; simplified after adversarial review)

**Single column, single purpose (v1):**

- `mailboxes.password_hash VARCHAR(255) NOT NULL` — bcrypt cost 12. This is what Stalwart's SQL directory reads and verifies. The `{scheme}hash` prefix format is supported if we ever switch to argon2id.

**No AES-GCM envelope.** The plaintext is never stored anywhere. The user sees it once as part of the `POST /api/v1/mailboxes` response body (in-memory only; never logged). This is the M7 `database_users.password_hash` shape — no shadow ciphered copy.

**Consequence:** end-user webmail (Step 8) is a plain IMAP login; the user types the password they saw at create time. No panel-mediated SSO for webmail in v1 (the original "Kratos cookie → Stalwart login shim" design leaks plaintext through the shim). Webmail SSO is explicitly deferred to M6.1 — the right design needs Stalwart JMAP admin-session-minting (v0.16.0 may support it, needs verification; if not, XOAUTH2 via Stalwart OIDC once that lands upstream).

**Password change path:** Roundcube's `password` plugin configured with the Stalwart/SQL driver talks directly to Stalwart over IMAP's password-change extension, which flows through Stalwart's SQL-directory `change_password` filter (UPDATE `password_hash`). Panel is not in the loop — no callback, no drift risk. If the user prefers, they can also rotate from the panel via `POST /api/v1/mailboxes/:id/rotate-password` which returns a new plaintext once.

### Wire contract (new agent commands)

| Command | Request | Response | Owner |
|---|---|---|---|
| `mailbox.create` | `{id, email, password_plain, quota_bytes}` | `{ok}` | agent |
| `mailbox.delete` | `{id, email}` | `{ok}` | agent |
| `mailbox.set_quota` | `{id, quota_bytes}` | `{ok, quota_bytes}` | agent |
| `mailbox.set_password` | `{id, password_plain}` | `{ok}` | agent |
| `mailbox.usage` | `{id}` | `{used_bytes, message_count, last_used_at}` | agent |
| `domain.email_enable` | `{domain_id, domain_name}` | `{ok, dkim_selector, dkim_public_key}` | agent |
| `domain.email_disable` | `{domain_id, domain_name}` | `{ok}` | agent |

Cross-boundary golden fixtures live at `panel-api/internal/agent/testdata/mailbox_*.json` + `domain_email_*.json`, round-tripped in `panel-api/internal/agent/mailbox_contract_test.go` (see `php_ext_contract_test.go` for the precise pattern).

### DNS records injected on domain.email_enable

All via the existing M4 `dnscompile` path — the reconciler writes these records into `dns_records` and PowerDNS reloads on tick:

| Name | Type | Value | Owner |
|---|---|---|---|
| `@` | MX | `10 mail.<domain>.` | panel |
| `mail` | A / AAAA | server public IPv4/IPv6 | panel |
| `@` | TXT (SPF) | `v=spf1 mx ~all` | panel |
| `jabali._domainkey` | TXT (DKIM) | `v=DKIM1; k=ed25519; p=<base64pub>` | panel |
| `_dmarc` | TXT (DMARC) | `v=DMARC1; p=quarantine; rua=mailto:postmaster@<domain>` | panel |
| `autoconfig` | CNAME | `mail.<domain>.` | panel |
| `_autodiscover._tcp` | SRV | `0 0 443 mail.<domain>.` | panel |

Selector `jabali` is hard-coded for v1; rotation schema carries a selector column for later use (ADR-0043).

---

## 2. Step decomposition

**Total:** 9 steps. **Dispatchable in parallel after Step 1:** Steps 2 ∥ 3. After Step 4: Steps 5 ∥ 6 ∥ 7 ∥ 8. Step 9 is last.

```
        Step 1 (ADRs + install.sh + migration)
             │
      ┌──────┴──────┐
   Step 2         Step 3
(Stalwart cfg)  (Agent cmds)
      │             │
      └──────┬──────┘
          Step 4
       (Panel API +
        reconciler)
             │
   ┌──────┬──┴──┬──────┐
Step 5  Step 6  Step 7  Step 8
(DNS)   (CLI)   (UI)    (Webmail)
   └──────┴──┬──┴──────┘
          Step 9
        (E2E + runbook
         + blueprint)
```

### Step 1 — Foundations: ADRs, install.sh, migration `000054_create_mailboxes` **[strongest model]**

**Branch:** `m6-step1-foundations` (off `m6/email-stalwart`).
**Rollback:** revert the branch; no cutover has happened yet.
**Rebase:** `git fetch origin main && git rebase origin/main` before PR. Re-run `go test ./... && cd panel-ui && npm test && npx playwright test` post-rebase.

**Pre-dispatch decisions (operator-confirmed 2026-04-21):**

1. **Webmail:** **Bulwark 1.4.14** (`https://github.com/bulwarkmail/webmail`) — Next.js + JMAP, purpose-built for Stalwart. Rationale: native Stalwart integration via `STALWART_FEATURES=true` + `STALWART_API_URL`; password change flows webmail → Stalwart admin API directly (panel stays out of the loop, matches the post-review decision to avoid panel-mediated SSO); OAuth2/OIDC native so Kratos integration is a future drop-in rather than a custom shim. Trade-off: AGPL-v3 license (fine for unmodified network-service deployment; if we ever fork to patch, source-publication obligation kicks in — ADR-0041 records this explicitly), second runtime (Node 20) alongside Go + PHP. Record in ADR-0041.
2. **RocksDB vs Maildir vs MariaDB-everywhere for mail data.** Decision: **RocksDB**. Rationale: Stalwart's default; has FTS + blob dedup; single-node performance > Maildir; MariaDB is not a supported backend for message content in v0.16.0. Record in ADR-0041.
3. **Directory table vs Stalwart internal directory.** Decision: **External SQL directory against `jabali_panel.mailboxes`**. Rationale: panel already writes the row (ADR-0003 one-write-path), no sync between internal Stalwart directory and panel rows, single-transaction provisioning. Record in ADR-0042.
4. **DKIM key type + storage.** Decision: **Ed25519 (RFC 8463), panel-owned keys under `/etc/jabali-panel/dkim/<domain>.key`, Stalwart auto-DNS disabled.** Rationale: one DNS source of truth (PowerDNS), predictable rotation, smaller DNS TXT record (no packed 2048-bit RSA → no multi-string split hell), faster signing. **Known trade-off:** Ed25519 DKIM is RFC 8463 (Sept 2018); Gmail / M365 / Fastmail support it, but long-tail corporate + government mail servers may silently skip the signature. With default `DMARC p=quarantine` + passing SPF, affected recipients get messages into spam/junk rather than full rejection. **Mitigation pre-committed in ADR-0043:** if delivery issues surface against a specific receiver, add a second RSA-2048 selector (`jabali-rsa`) alongside the Ed25519 primary — Stalwart can sign with both, receivers pick the algorithm they understand. Escape-hatched in the runbook, not in v1 default. Rotation schedule: every 365 days, selector bump `jabali-YYYY-MM` rolled by reconciler + DNS TXT coexistence window (old + new for 72h).
5. **imap-migrate on day 1?** Decision: **Deferred to M15**. Record in ADR-0044. Operators needing day-one migration use `stalwart-cli imap-migrate …` manually (runbook links to upstream docs).
6. **Default per-mailbox quota:** **1 GiB flat** (not inherited from `hosting_packages`). Simpler v1; per-package plumbing is a later add.
7. **Mail hostname shape:** **per-domain `mail.<domain>` MX label** with a dedicated A record per domain pointing at the server's public IPv4 (and AAAA for IPv6). Not shared `mail.<server-hostname>`. Upside: each domain's mail traffic is independently addressable; splitting a domain to a different mailserver later doesn't require an MX change on the other domains.

**Deliverables:**

- `docs/adr/0041-m6-mail-storage-rocksdb.md` — captures items 1 + 2 above.
- `docs/adr/0042-m6-sql-directory-mailboxes-table.md` — captures item 3 + the schema below.
- `docs/adr/0043-m6-dkim-key-rotation-policy.md` — captures item 4 + the 365-day + 72h coexistence rotation.
- `docs/adr/0044-m6-imap-migrate-deferred-to-m15.md` — captures item 5.
- `panel-api/internal/migrations/000054_create_mailboxes.up.sql`:
  ```sql
  -- Mailboxes: one row per hosted mail account.
  CREATE TABLE mailboxes (
    id CHAR(26) NOT NULL PRIMARY KEY,
    domain_id CHAR(26) NOT NULL,
    local_part VARCHAR(64) NOT NULL,
    email_cached VARCHAR(320) NOT NULL,        -- populated by trigger below; kept in sync with local_part + domains.name
    password_hash VARCHAR(255) NOT NULL,       -- bcrypt cost 12 for Stalwart SQL-directory verify
    quota_bytes BIGINT UNSIGNED NOT NULL DEFAULT 1073741824,  -- 1 GiB default
    is_disabled TINYINT(1) NOT NULL DEFAULT 0,
    last_usage_bytes BIGINT UNSIGNED NOT NULL DEFAULT 0,
    last_usage_at DATETIME(6) NULL,
    created_at DATETIME(6) NOT NULL,
    updated_at DATETIME(6) NOT NULL,
    UNIQUE KEY ux_mailboxes_email_cached (email_cached),
    KEY ix_mailboxes_domain (domain_id),
    CONSTRAINT fk_mailboxes_domain FOREIGN KEY (domain_id)
      REFERENCES domains(id) ON DELETE CASCADE
  ) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin;

  -- Triggers keep email_cached in sync (portable across MariaDB versions;
  -- STORED generated columns with subqueries are not universally supported).
  DELIMITER //
  CREATE TRIGGER trg_mailboxes_before_insert BEFORE INSERT ON mailboxes
  FOR EACH ROW BEGIN
    SET NEW.email_cached = CONCAT(NEW.local_part, '@', (SELECT name FROM domains WHERE id = NEW.domain_id));
  END//
  CREATE TRIGGER trg_mailboxes_before_update BEFORE UPDATE ON mailboxes
  FOR EACH ROW BEGIN
    IF NEW.local_part <> OLD.local_part OR NEW.domain_id <> OLD.domain_id THEN
      SET NEW.email_cached = CONCAT(NEW.local_part, '@', (SELECT name FROM domains WHERE id = NEW.domain_id));
    END IF;
  END//
  -- If a domain is renamed (rare; not supported via panel API today but possible via direct SQL),
  -- mailbox rows must be resynced. Trigger on domains:
  CREATE TRIGGER trg_domains_after_update_sync_mailboxes AFTER UPDATE ON domains
  FOR EACH ROW BEGIN
    IF NEW.name <> OLD.name THEN
      UPDATE mailboxes SET email_cached = CONCAT(local_part, '@', NEW.name)
        WHERE domain_id = NEW.id;
    END IF;
  END//
  DELIMITER ;

  -- Domain-level email state.
  ALTER TABLE domains
    ADD COLUMN email_enabled TINYINT(1) NOT NULL DEFAULT 0,
    ADD COLUMN dkim_selector VARCHAR(64) NULL,
    ADD COLUMN dkim_public_key TEXT NULL,
    ADD COLUMN email_enabled_at DATETIME(6) NULL;
  ```
  Down migration drops triggers first, then column additions, then the `mailboxes` table. No data loss for pre-M6 rows on either direction.
- `install.sh` additions (idempotent, fail-loud):
  - `install_stalwart()`: download `stalwart-x86_64-unknown-linux-gnu.tar.gz` for tag `v0.16.0`, verify against `install/stalwart.sha256` (captured on first deploy — leave a placeholder + `_err` if empty, per the DokuWiki/MediaWiki precedent), extract to `/opt/stalwart/`, symlink `/usr/local/bin/stalwart`.
  - `install_node20()`: if `node --version` returns less than `v20.0.0` or node is absent, install Node.js 20 LTS from NodeSource tarball (pin + SHA-256 in `install/node.sha256`). Skip if already satisfied (panel-ui's build requirement means Node 20 is often already present). Idempotent.
  - `install_bulwark()`: if `/opt/jabali-webmail/VERSION` matches `1.4.14`, skip. Otherwise `git clone https://github.com/bulwarkmail/webmail.git --branch v1.4.14 --depth 1 /opt/jabali-webmail-new`, verify the commit's SHA (pinned in `install/bulwark.commit-sha`; on first deploy, capture the commit SHA that v1.4.14 resolves to), `npm ci` + `npm run build` as `jabali-webmail` user, then atomic swap (`mv /opt/jabali-webmail /opt/jabali-webmail-prev && mv /opt/jabali-webmail-new /opt/jabali-webmail`). Write `VERSION` file with `1.4.14`.
  - Create service users:
    - `jabali-mail` (primary group `jabali-mail`, supplementary group `jabali`) — runs Stalwart.
    - `jabali-webmail` (primary group `jabali-webmail`, supplementary group `jabali`) — runs Bulwark.
  - Create dirs:
    - `/var/lib/stalwart/` (0750 `jabali-mail:jabali-mail`)
    - `/etc/stalwart/` (0750 `jabali-mail:jabali-mail`)
    - `/etc/jabali-panel/dkim/` (0750 `jabali:jabali` — agent reads this as root via supplementary group; Stalwart reads it via `jabali-mail:jabali` group membership)
    - `/opt/jabali-webmail/` (0755 `jabali-webmail:jabali-webmail`)
    - `/var/lib/jabali-webmail/` (0750 `jabali-webmail:jabali-webmail`) — Bulwark's settings-sync data dir.
  - Write systemd units under `jabali.slice` (system daemons, not per-user):
    - `/etc/systemd/system/jabali-stalwart.service`: `User=jabali-mail`, `ExecStart=/usr/local/bin/stalwart --config=/etc/stalwart/config.toml`, `Restart=on-failure`, `WatchdogSec=30`.
    - `/etc/systemd/system/jabali-webmail.service`: `User=jabali-webmail`, `WorkingDirectory=/opt/jabali-webmail`, `ExecStart=/usr/bin/node /opt/jabali-webmail/server.js` (actual path resolved from Bulwark's `package.json` → `start` script in Step 8), `Environment=HOSTNAME=127.0.0.1 PORT=3000`, `EnvironmentFile=/etc/jabali-panel/bulwark.env` (created in Step 8), `Restart=on-failure`.
  - Write a minimal `/etc/stalwart/config.toml` skeleton: listeners bound to `127.0.0.1:8446` (HTTP/JMAP), `0.0.0.0:25/465/587/993` (mail protocols); storage = rocksdb at `/var/lib/stalwart/`; directory = placeholder (filled in Step 2); TLS paths point at `/etc/letsencrypt/live/` with per-binding SNI resolution.
  - nginx snippet at `/etc/nginx/sites-available/00-jabali-mail-proxy.conf` stubbed (filled in Step 4/8).
  - **Both systemd units disabled by default** — enabled by the first panel `domain.email_enable` call via `systemctl enable --now jabali-stalwart jabali-webmail` executed from the agent. Idempotent: re-runs of `install.sh` don't start the services.
  - **JMAP admin token** `/etc/jabali-panel/stalwart-admin.token` (0640 `jabali:jabali-mail`): generated **only if missing** via `openssl rand -base64 32`; re-runs of `install.sh` read the existing file. Stalwart config references it via `!include_path "/etc/jabali-panel/stalwart-admin.token"` so rotation is a file write + `systemctl reload` without re-rendering the whole config. Rotation procedure documented in the runbook (Step 9).
  - **Bulwark SESSION_SECRET** `/etc/jabali-panel/bulwark-session.key` (0640 `jabali-webmail:jabali-webmail`): generated only if missing via `openssl rand -base64 32`; re-runs preserve. Referenced by `/etc/jabali-panel/bulwark.env` via `SESSION_SECRET_FILE=`.
- `internal/mailaddr/mailaddr.go`: `Canonicalise(raw string) (local, domain string, err error)` — lowercases, strips `+tag`, rejects non-ASCII for v1 (punycode domains OK; UTF-8 locals deferred), rejects shell metacharacters. Shared by panel-api + panel-agent.

**Verification:**

```bash
go build ./...
go test ./panel-api/internal/migrations/... ./internal/mailaddr/...
# Apply + rollback migration against a scratch MariaDB:
migrate -source file://panel-api/internal/migrations -database "${JABALI_TEST_DATABASE_URL}" up
migrate -source file://panel-api/internal/migrations -database "${JABALI_TEST_DATABASE_URL}" down 1
# install.sh idempotence:
bash install.sh && bash install.sh  # second run must _ok everything, not re-install
systemctl cat jabali-stalwart.service | grep -E 'Slice=jabali.slice|User=jabali-mail'
```

**Exit criteria:**

- All 4 ADRs merged.
- Migration applies + rolls back cleanly against MariaDB 10.11.
- `install.sh` installs Stalwart v0.16.0 binary, creates dirs, writes systemd unit, does NOT start the service.
- `go test ./internal/mailaddr/...` covers ≥ 10 cases incl. `+tag`, `UPPERCASE`, IDN, shell-metachar rejection.
- Branch rebased on `origin/main`, CI green on the PR, branch name + SHA(s) + `git log main..<branch>` summary in the completion report.

---

### Step 2 — Stalwart SQL directory + listener config **[strongest model]** *(parallel with Step 3)*

**Branch:** `m6-step2-stalwart-config` off `m6-step1-foundations` once Step 1 is merged to `m6/email-stalwart`.
**Rollback:** revert.
**Parallel with:** Step 3. No shared files (this step owns `install/stalwart/config.toml.tmpl` and `install.sh` stalwart-config block; Step 3 owns `panel-agent/internal/commands/mailbox_*.go`).

**Deliverables:**

- `install/stalwart/config.toml.tmpl` (new) — complete Stalwart config with:
  - `directory "jabali" { type = "sql"; store = "jabali-mariadb" }` pointed at the `jabali_panel.mailboxes` + `domains` tables.
  - Bind addresses: `25`, `465`, `587`, `993` on `0.0.0.0`; `8446` on `127.0.0.1` for JMAP management.
  - TLS: `certificate.default = "acme"` with ACME disabled (reuse certbot certs at `/etc/letsencrypt/live/`). Explicitly disable Stalwart's ACME client (ADR-0041).
  - DNS integration: `config.dns.providers = []` (explicit empty list — we own DNS via PowerDNS).
  - Queries (exact Stalwart v0.16.0 filter syntax — verify against upstream schema during step):
    ```toml
    [directory."jabali".lookup]
    query.name    = "SELECT local_part, domain_id, quota_bytes FROM mailboxes WHERE email_cached = ? AND is_disabled = 0"
    query.auth    = "SELECT password_hash FROM mailboxes WHERE email_cached = ? AND is_disabled = 0"
    query.domains = "SELECT name FROM domains WHERE email_enabled = 1"
    query.emails  = "SELECT email_cached FROM mailboxes WHERE email_cached = ? AND is_disabled = 0"
    ```
  - A service account for JMAP management (for the agent's `mailbox.usage` + invalidation calls). Secret lives in `/etc/jabali-panel/stalwart-admin.token` (0600 `jabali:jabali-mail`, group-readable so the agent — running as root, with supplementary group jabali-mail — can read it). Token is generated by `install.sh` on first install via `openssl rand -base64 32` and written atomically.
- `install.sh`: render `/etc/stalwart/config.toml` from the template + env (database URL, DKIM dir path, JMAP admin token path). **Reads** the existing `/etc/jabali-panel/stalwart-admin.token` (created in Step 1); does not regenerate. `systemctl enable --now jabali-stalwart.service` still NOT called by install.sh at this step — service starts on first `domain.email_enable`.
- `panel-api/internal/config/config.go`: new `Mail` section — `Mail.JMAPAdminTokenPath`, `Mail.StalwartAdminURL` (default `http://127.0.0.1:8446/jmap`), `Mail.DKIMKeyDir` (default `/etc/jabali-panel/dkim`).
- Manual integration test (documented in the PR description): insert a row into `mailboxes` by hand, `systemctl start jabali-stalwart`, `openssl s_client -connect localhost:465 -starttls smtp` + `AUTH PLAIN` with the seeded credentials, confirm Stalwart accepts auth.

**Verification:**

```bash
# Template renders with fake env:
bash install/stalwart/render-config.sh --dry-run > /tmp/stalwart-config.out && diff /tmp/stalwart-config.out install/stalwart/testdata/config.expected.toml
# Stalwart accepts the config without error:
sudo -u jabali-mail /usr/local/bin/stalwart --config=/etc/stalwart/config.toml --validate
# Start + bind check:
systemctl start jabali-stalwart && systemctl is-active jabali-stalwart
ss -tlnp | grep -E ':(25|465|587|993|8446)\b'
# Manual SMTP auth with a hand-seeded row:
INSERT INTO mailboxes (id, domain_id, local_part, password_hash, …) VALUES (…);
echo 'QUIT' | openssl s_client -starttls smtp -connect localhost:587 -quiet < <(printf "EHLO t\r\nAUTH PLAIN %s\r\nQUIT\r\n" "$(echo -en '\0alice@example.com\0P@ss' | base64)")
# Expect 235 Authentication successful.
```

**Exit criteria:**

- `stalwart --validate` green against the rendered config.
- Stalwart binds all expected ports after `systemctl start`.
- Hand-seeded mailbox can authenticate via SMTP AUTH PLAIN on 587 and IMAP LOGIN on 993.
- Config template committed with a golden `config.expected.toml` fixture + render test.
- PR rebased + CI green + branch report.

---

### Step 3 — Agent commands + wire contract (all golden fixtures live here) **[default model]** *(parallel with Step 2)*

**Branch:** `m6-step3-agent-commands` off `m6-step1-foundations` once merged.
**Rollback:** revert.
**Parallel with:** Step 2.

**Deliverables:**

- `agentwire/commands.go`: add command-name constants `MailboxCreate`, `MailboxDelete`, `MailboxSetQuota`, `MailboxSetPassword`, `MailboxUsage`, `DomainEmailEnable`, `DomainEmailDisable`.
- `panel-agent/internal/commands/mailbox_create.go` and siblings for each command — registered via `Default.Register("mailbox.create", handler)` etc. Handlers:
  - `mailbox.create` / `mailbox.delete` / `mailbox.set_quota` / `mailbox.set_password`: **no-op except for a JMAP cache-invalidation call** (`POST /jmap` with `{"using": ["…:core"], "methodCalls": [["Principal/invalidate", {...}, "c0"]]}`). The row is written by the panel; Stalwart re-reads SQL on the next auth; invalidate just clears the Stalwart LRU.
  - `mailbox.usage`: JMAP `Quota/get` against the principal, returns `{used_bytes, message_count, last_used_at}`. If the mailbox hasn't been touched yet, all zeros.
  - `domain.email_enable`: (a) generate **Ed25519** DKIM keypair via `crypto/ed25519` (32-byte seed + 32-byte public), write the 32-byte raw private key base64-encoded to `/etc/jabali-panel/dkim/<domain>.key` (0600 `jabali:jabali`) and return the public key in DKIM TXT format `v=DKIM1; k=ed25519; p=<base64-32B-pub>` (a single short string — no `"…" "…"` concatenation needed, unlike RSA-2048); (b) `systemctl enable --now jabali-stalwart.service` (idempotent); (c) reload Stalwart via `systemctl reload jabali-stalwart.service` (drops SIGHUP → Stalwart re-reads directory query cache).
  - `domain.email_disable`: (a) `rm /etc/jabali-panel/dkim/<domain>.key`; (b) reload Stalwart; (c) do NOT stop the Stalwart service — other domains may still be enabled.
- `internal/dkim/dkim.go`: `GenerateEd25519() (privateRawBase64, publicDKIMTxt []byte, err error)` — uses `crypto/ed25519.GenerateKey`, base64-encodes the 32-byte seed for `privateRawBase64`, formats the 32-byte public key as `v=DKIM1; k=ed25519; p=<base64pub>`. `WritePrivate(path string, priv []byte) error` with atomic `os.CreateTemp` + `os.Rename` + `chmod 0600`. Shared by panel-api (reconciler uses it for rotation) and panel-agent. Structure also leaves room for a future `GenerateRSA2048` sibling if ADR-0043's fallback path is ever needed — same `WritePrivate` semantics, different key-type tag in the filename suffix.
- **All seven cross-boundary golden fixtures owned by this step** (Step 4 imports the same files, does not duplicate):
  `panel-api/internal/agent/testdata/mailbox_create.json`, `mailbox_delete.json`, `mailbox_set_quota.json`, `mailbox_set_password.json`, `mailbox_usage.json`, `domain_email_enable.json`, `domain_email_disable.json`.
  Round-trip tests in `panel-api/internal/agent/mailbox_contract_test.go` (mirror `php_ext_contract_test.go`) — each fixture is unmarshalled into the panel-side request type, marshalled back through the agent-side handler, and compared byte-for-byte. Tests live on both sides so neither can drift without CI catching it.
- Unit tests: `panel-agent/internal/commands/mailbox_*_test.go` — table-driven, mock the JMAP HTTP round-trip via `httptest.NewServer`.

**Verification:**

```bash
go test -race -count=1 ./panel-agent/internal/commands/... ./panel-api/internal/agent/... ./internal/dkim/...
# Live agent + live Stalwart smoke (on a VM):
echo '{"command":"domain.email_enable","args":{"domain_id":"01…","domain_name":"example.com"}}' | nc -U /run/jabali/agent.sock
# Expect {"ok":true,"dkim_selector":"jabali","dkim_public_key":"v=DKIM1..."}
ls -l /etc/jabali-panel/dkim/example.com.key   # 0600 jabali:jabali
systemctl is-active jabali-stalwart            # active (running)
```

**Exit criteria:**

- All 7 commands registered in the agent registry; each has a unit test with ≥ 3 scenarios (happy, JMAP error, bad args).
- 7 golden fixtures present; `mailbox_contract_test.go` round-trips them both directions.
- Live smoke against a VM produces the expected DKIM key + public-key output.
- `go test -race` green; branch rebased + CI green + report.

---

### Step 4 — Panel API (repositories, handlers) + reconciler **[default model]**

**Branch:** `m6-step4-panel-api-reconciler` off `m6/email-stalwart` once Steps 2+3 merged.
**Rollback:** revert (but the migration from Step 1 stays — it's already live in prod DBs; revert of this step leaves orphan table + columns, which is fine).
**Depends on:** Steps 1, 2, 3 all merged to `m6/email-stalwart`.

**Deliverables:**

- `panel-api/internal/models/mailbox.go` — GORM model mirroring the migration.
- `panel-api/internal/repository/mailbox_repository.go` — `Create(ctx, m *Mailbox) error`, `Get(ctx, id string)`, `ListByDomain(ctx, domainID string, opts ListOptions)`, `ListByUser(ctx, userID string, opts ListOptions)` (JOIN domains.user_id), `Update`, `Delete(ctx, id string)`, `UpdateUsage(ctx, id, usedBytes uint64, messages uint, at time.Time)`.
- `panel-api/internal/api/mailboxes.go`:
  - `GET /api/v1/mailboxes` (admin: all; user: scoped to domains they own).
  - `POST /api/v1/mailboxes` — body `{domain_id, local_part, password?, quota_bytes?}`. Generates a random password if `password` omitted, bcrypts cost-12, writes the row. Returns the plaintext ONCE in the response body (no persistence). **Inline best-effort:** writes the row + calls `agent.mailbox.create` (JMAP cache-invalidate only) in the same handler. If the agent call fails, return 500 but leave the row — the reconciler will retry invalidation; the mailbox still works because Stalwart reads SQL directly on every auth.
  - `GET /api/v1/mailboxes/:id`.
  - `PATCH /api/v1/mailboxes/:id` — quota change + enable/disable.
  - `POST /api/v1/mailboxes/:id/rotate-password` — returns the new plaintext once.
  - `DELETE /api/v1/mailboxes/:id` — row + `agent.mailbox.delete`.
  - `GET /api/v1/mailboxes/:id/usage` — proxies `agent.mailbox.usage`, caches 30s.
- `panel-api/internal/api/domain_email.go`:
  - `POST /api/v1/domains/:id/email` — flips `email_enabled=1`, generates DKIM key via `agent.domain.email_enable`, stores selector + public key in `domains.dkim_*`. Queues DNS record insertion (done in Step 5). **No blocking reachability guard at enable time** (Stalwart may not be running yet on first enable, and ISP-level port 25 blocks are external — a blocking guard false-positives too often). The reachability check is a Step 5 `GET /api/v1/domains/:id/email/dns-status` endpoint called by the UI *after* enable; reports per-record status + per-port reachability as advisories, never blocks the enable.
  - `GET /api/v1/domains/:id/email/dns-status` — returns `{dkim: {published: bool, expected: string}, mx: {…}, spf: {…}, dmarc: {…}, ports: [{port: 25, reachable: bool, note: "ISPs commonly block 25 outbound"}]}`. Implementation: queries `dns_records` + probes local binds via `system.info`. No remote-IP probe in v1 (asymmetric trust); runbook tells operators how to self-test with `telnet gmail-smtp-in.l.google.com 25` from their own workstation.
  - `DELETE /api/v1/domains/:id/email` — flips `email_enabled=0`, `agent.domain.email_disable`, **does NOT delete mailboxes** (they stay orphaned but unusable until email re-enabled; a separate cascade flag `?cascade=1` purges them).
- `panel-api/internal/reconciler/mailbox_reconcile.go`:
  - `reconcileDomainEmailState(ctx)` — for every `domain.email_enabled=1`, verify the DKIM key file exists + Stalwart is active. If key file missing (e.g. restore-from-backup scenario), regenerate + re-publish DNS. If Stalwart isn't running but there's ≥ 1 enabled domain, `systemctl start jabali-stalwart`.
  - `reconcileMailboxUsage(ctx)` — every 5 minutes, sample `agent.mailbox.usage` for a rolling window of mailboxes (bounded — say 50/pass), write `last_usage_*` columns. This is the data the UI's Progress bar reads.
- API route registration in `panel-api/internal/server/routes.go` + Kratos middleware guarding them.
- Tests: table-driven handler tests in `mailboxes_test.go`, sqlmock for repo tests, reconciler tests with mock agent.

**Verification:**

```bash
go test -race -count=1 ./panel-api/internal/...
make test-integration  # hits a real MariaDB via $JABALI_TEST_DATABASE_URL
# API smoke:
curl -sS -H "Cookie: ory_kratos_session=…" -X POST http://localhost:8443/api/v1/domains/<id>/email \
  | jq '.email_enabled, .dkim_public_key'
curl -sS -H "Cookie: ory_kratos_session=…" -X POST http://localhost:8443/api/v1/mailboxes \
  -d '{"domain_id":"…","local_part":"alice","quota_bytes":524288000}' | jq '.email, .password_reveal'
```

**Exit criteria:**

- All 8 endpoints green in handler tests (happy + 3 failure modes each).
- Reconciler passes hit the mock agent exactly the expected number of times (assert via `agent.MockAgentClient.CallCount("mailbox.usage")`).
- Integration test against a real MariaDB seeds a domain, enables email, creates a mailbox, reads usage, disables email — all in < 5s.
- `go test -race` green; branch rebased + CI green + report.

---

### Step 5 — DNS autoconfig (MX/SPF/DKIM/DMARC/autoconfig/autodiscover) **[default model]** *(parallel with 6,7,8)*

**Branch:** `m6-step5-dns-autoconfig`.
**Depends on:** Step 4 merged.

**Deliverables:**

- `panel-api/internal/dnscompile/email_records.go`: `BuildEmailRecords(domain models.Domain, serverIPv4, serverIPv6 string) []models.DNSRecord` — returns the 7 records from §1 deterministically. Idempotent by `(zone_id, name, type)` uniqueness.
- Reconciler hook `reconcileDomainEmailState` calls `dnsZones.UpsertRecords(ctx, records)` when a domain flips enabled. On disable, deletes the same 7 records by `(zone_id, name, type, managed_by='m6')` tuple.
- `dns_records` schema: add `managed_by VARCHAR(16) NULL` column via the existing migration (fold into Step 1's `000054`) so M6 records are distinguishable from user-edited ones. **User-edited overrides win** — if a user manually created `@ MX` in the DNS UI, M6 skips it and surfaces a warning in the Email tab.
- Autoconfig HTTP endpoint: `GET /mail/config-v1.1.xml` on nginx (served via panel) — Thunderbird autoconfig format. Separate tiny handler `panel-api/internal/api/autoconfig.go` returns the XML keyed by domain. DNS record `autoconfig.<domain> CNAME mail.<domain>.` + panel vhost rule forwards the request.
- `_autodiscover._tcp.<domain> SRV 0 0 443 mail.<domain>.` — handled by DNS injection above. (Outlook-flavoured `Autodiscover.xml` path is a separate follow-up; SRV + autoconfig XML covers Thunderbird + modern mobile.)

**Verification:**

```bash
go test ./panel-api/internal/dnscompile/...
# After enabling email on example.com (local VM):
dig @127.0.0.1 example.com MX +short          # 10 mail.example.com.
dig @127.0.0.1 jabali._domainkey.example.com TXT +short | head -1  # v=DKIM1; k=rsa; p=...
dig @127.0.0.1 _dmarc.example.com TXT +short  # v=DMARC1; p=quarantine; ...
curl -sS https://example.com/mail/config-v1.1.xml | xmllint --format -  # valid autoconfig XML
```

**Exit criteria:**

- All 7 DNS records appear in `dns_records` + PowerDNS after `POST /domains/:id/email`.
- `DELETE /domains/:id/email` removes exactly the 7 M6-managed records and leaves user-edited overrides alone.
- Autoconfig XML validates against the Thunderbird schema (manual check; no strict tooling).
- Test with a user-edited `@ MX` override: M6 skips it, logs a warning, surfaces it in the Email tab response.

---

### Step 6 — CLI `jabali mailbox …` + `jabali domain email-*` **[default model]** *(parallel with 5,7,8)*

**Branch:** `m6-step6-cli`.
**Depends on:** Step 4 merged.

**Deliverables:**

- `panel-api/cmd/server/mailbox_cli.go`: `jabali mailbox list [--domain <d>]`, `jabali mailbox create --domain <d> --local <l> [--password <p>] [--quota-mb <n>]`, `jabali mailbox delete <email>`, `jabali mailbox set-quota <email> <mb>`, `jabali mailbox passwd <email>` (prompt + rotate, print once).
- `panel-api/cmd/server/domain_email_cli.go`: `jabali domain email-enable <domain>`, `jabali domain email-disable <domain>`.
- Commands hit the panel's local UDS (same pattern as `jabali limits` in M18) — not the HTTP API — so they bypass Kratos auth. This is admin-only by definition (you need to be root / `jabali` group to read the UDS).
- Short godoc comments + `--help` strings; no separate CLI man pages.
- One unit test verifies `jabali mailbox list --domain <d1>` and `jabali mailbox list --domain <d2>` return disjoint result sets when both domains have mailboxes (scoping regression guard).

**Verification:**

```bash
jabali mailbox create --domain example.com --local alice --quota-mb 500
# Password: W8NpB…  (shown once)
jabali mailbox list --domain example.com
jabali mailbox passwd alice@example.com
jabali mailbox set-quota alice@example.com 1000
jabali mailbox delete alice@example.com
jabali domain email-enable example.com
jabali domain email-disable example.com
```

**Exit criteria:**

- All 7 subcommands work against a live panel.
- `jabali mailbox --help` lists them; each subcommand has its own `--help`.
- One unit test per subcommand (mocking the UDS server).

---

### Step 7 — Admin UI: Email tab on domain-edit + Mailboxes tab **[default model]** *(parallel with 5,6,8)*

**Branch:** `m6-step7-ui`.
**Depends on:** Step 4 merged (wire contract `{data, total, page, page_size}` — per `feedback_verify_wire_contract`, read `panel-api/internal/api/mailboxes.go` actual envelope before coding hooks).
**Post-M21 — AntD-native, no Refine.**

**Deliverables:**

- `panel-ui/src/shells/admin/domains/DomainEmailTab.tsx` — enable/disable switch; shows MX / SPF / DKIM / DMARC record status (green if present in PowerDNS, yellow if missing, red if conflicting user-override) by polling `GET /api/v1/domains/:id/email/dns-status` every 10s while the tab is open; "Copy DKIM public key" button; per-port reachability advisory panel with the runbook's self-test hint for port 25.
- `panel-ui/src/shells/admin/domains/MailboxesTab.tsx` — AntD Table listing mailboxes for the current domain, columns: email, quota (Progress bar using `last_usage_bytes / quota_bytes`), last-login, actions (rotate password, delete). Modal for create.
- `panel-ui/src/shells/user/mailboxes/UserMailboxList.tsx` — same shape, user-scoped.
- Hooks: `useMailboxes`, `useMailboxUsage`, `useDomainEmail` — built on `useListQuery` + `useOneQuery` + `useCreate|UpdateMutation` (from `panel-ui/src/hooks/useQueries.ts`). URL-backed state via `useTableURL`.
- Icons: AntD `<MailOutlined>`.
- Nav: sidebar item "Mailboxes" added to `panel-ui/src/nav.ts` for `shell: "user"` when the user has ≥ 1 email-enabled domain (query on mount).
- Unit tests: vitest component tests for the create modal (password generation + reveal flow) and the Progress bar rendering.

**Verification:**

```bash
cd panel-ui
npm test
npm run build
npm run test:e2e -- --grep @m6  # tagged e2e, written in Step 9
```

**Exit criteria:**

- vitest green; tsc clean; production build clean.
- Manual browser walkthrough: enable email → create mailbox → see quota bar → rotate password (shown once) → delete.
- `grep -r "@refinedev" panel-ui/src` still returns 0 hits (no regression of M21).

---

### Step 8 — Webmail: Bulwark 1.4.14 (Next.js + JMAP) **[default model]**

**Branch:** `m6-step8-webmail`.
**Depends on:** Step 4 merged (mailbox-create API gives accounts to log into) + Step 1's Bulwark install (binary + systemd unit already placed; this step wires config + nginx + reconciler).
**Parallel with:** 5, 6, 7.

**Scope note:** Originally proposed Roundcube 1.6.9; operator chose Bulwark (`https://github.com/bulwarkmail/webmail`) — purpose-built for Stalwart, JMAP-native, with OAuth2/OIDC and native Stalwart admin-API integration. This is cleaner than Roundcube: single Node service (not per-user PHP-FPM), password change flows webmail → Stalwart admin API directly (panel stays out of the loop), no separate webmail database, no PHP plugin wiring. No panel-mediated SSO in v1 (same decision as the Roundcube design for the same post-review reasons); revisit in M6.1 once Bulwark's OAuth2/OIDC can be pointed at Kratos as the IdP.

**What's already in place from Step 1:** `jabali-webmail` service user, `/opt/jabali-webmail/` checkout with `npm run build` output, `/var/lib/jabali-webmail/` data dir, `/etc/jabali-panel/bulwark-session.key` secret, `jabali-webmail.service` systemd unit (disabled, points at `/etc/jabali-panel/bulwark.env`). All idempotent in install.sh.

**Deliverables:**

- `install/bulwark/bulwark.env.tmpl` rendered to `/etc/jabali-panel/bulwark.env` (0640 `jabali-webmail:jabali-webmail`):
  ```env
  # Bulwark config — all runtime, no rebuild needed on change.
  HOSTNAME=127.0.0.1
  PORT=3000
  JMAP_SERVER_URL=https://mail.${JABALI_SERVER_HOSTNAME}   # nginx routes /jmap → Stalwart
  STALWART_FEATURES=true
  STALWART_API_URL=http://127.0.0.1:8446                    # Bulwark → Stalwart admin API (loopback)
  SESSION_SECRET_FILE=/etc/jabali-panel/bulwark-session.key
  SETTINGS_SYNC_ENABLED=true
  SETTINGS_DATA_DIR=/var/lib/jabali-webmail/settings
  LOG_FORMAT=json
  LOG_LEVEL=info
  # Branding — point at the panel's own assets so the webmail matches the control-panel theme.
  APP_NAME=Jabali Mail
  LOGIN_COMPANY_NAME=${JABALI_SERVER_HOSTNAME}
  # No OAUTH_ENABLED in v1 — password login against Stalwart. Kratos-as-IdP is M6.1.
  ```
  Rendered by install.sh at the Stalwart-config phase (Step 2) so rendering is co-located with the other config templates. Idempotent: re-rendering only touches the file if it'd change, keyed by sha256 of the rendered content.
- `install/nginx/jabali-mail-vhost.conf.tmpl` — per-domain `mail.<domain>` server block:
  ```nginx
  server {
    listen 443 ssl http2;
    server_name mail.${DOMAIN};
    ssl_certificate /etc/letsencrypt/live/${DOMAIN}/fullchain.pem;
    ssl_certificate_key /etc/letsencrypt/live/${DOMAIN}/privkey.pem;

    # Bulwark webmail (Next.js) — everything not under /jmap or /api routes here.
    location / {
      proxy_pass http://127.0.0.1:3000;
      proxy_set_header Host $host;
      proxy_set_header X-Forwarded-For $remote_addr;
      proxy_set_header X-Forwarded-Proto https;
      proxy_http_version 1.1;
      proxy_set_header Upgrade $http_upgrade;        # JMAP push via websocket
      proxy_set_header Connection "upgrade";
      proxy_read_timeout 3600s;                       # long-poll JMAP push
    }

    # Stalwart JMAP + admin API — everything the browser needs to talk to the mailserver.
    location /jmap               { proxy_pass http://127.0.0.1:8446; proxy_set_header Host $host; proxy_http_version 1.1; proxy_read_timeout 3600s; }
    location /.well-known/jmap   { proxy_pass http://127.0.0.1:8446; proxy_set_header Host $host; }
    location /api                { proxy_pass http://127.0.0.1:8446; proxy_set_header Host $host; }  # Stalwart admin API (Bulwark's STALWART_API_URL target)
    location /autoconfig         { proxy_pass http://127.0.0.1:8443; proxy_set_header Host $host; }  # panel serves /mail/config-v1.1.xml (Step 5)
  }
  # HTTP -> HTTPS redirect.
  server {
    listen 80;
    server_name mail.${DOMAIN};
    return 301 https://$host$request_uri;
  }
  ```
  **No `/webmail/` path-mount on the primary domain** — operator chose the clean per-domain `mail.<domain>` shape (decision 7 in pre-dispatch). Users go to `https://mail.<domain>/` and that's it.
- Reconciler hook `reconcileWebmailVhosts` (panel-api/internal/reconciler/webmail_reconcile.go): for every domain with `email_enabled=1`, render the template above, write to `/etc/nginx/sites-enabled/<domain>-mail.conf`, nginx-reload if the hash changed; for disabled domains, `rm` the file + reload. On any domain with email enabled but Bulwark service not running, `systemctl start jabali-webmail` (same lazy-start semantics as Stalwart).
- Certbot wiring: when `domain.email_enable` fires, the existing M5 SSL path must already cover `mail.<domain>` — the reconciler queues a certificate issuance for `mail.<domain>` using the same ACME-first-then-self-signed code. Panel's SAN-list for the domain's cert includes both `<domain>` and `mail.<domain>` (verify in Step 4's cert plumbing; if not, fold a `ssl.expand_san` agent command call into `domain.email_enable`).
- Tests:
  - `install.sh` idempotence test: two consecutive runs of `install_bulwark()` + `install_stalwart()` on a clean VM; second run reports "skip" for both and neither regenerates secrets.
  - Go test for `reconcileWebmailVhosts` — mock domain repository, assert exact file content for an enabled + a disabled domain.

**Verification:**

```bash
# On a clean VM, post-install:
systemctl status jabali-webmail   # inactive (dead) — no domain enabled yet.
curl -fsS http://127.0.0.1:8443/api/v1/domains/<id>/email -X POST -H "Cookie: ory_kratos_session=…"
systemctl status jabali-webmail   # active (running).
curl -fsSL https://mail.example.com/ | grep -qi 'Bulwark'    # login page served.
# Log in as alice@example.com with the mailbox password from Step 4's create response:
#   browser → https://mail.example.com → login form → JMAP Session call to /jmap → inbox.
# Password change via Settings → Account Security:
#   Bulwark calls Stalwart admin API at http://127.0.0.1:8446/api/… → Stalwart updates password_hash in the SQL directory.
#   Verify: SELECT password_hash FROM jabali_panel.mailboxes WHERE email_cached='alice@example.com'; shows a new bcrypt vs the pre-change value.
```

**Exit criteria:**

- `https://mail.<domain>/` serves Bulwark 1.4.14; Page has `<title>Bulwark…</title>`.
- Mailbox password from panel create → JMAP login via Bulwark → inbox visible.
- Password change inside Bulwark → next JMAP auth uses new password → `mailboxes.password_hash` bcrypt reflects it.
- Reconciler hook toggles the vhost snippet in sync with `domain.email_enabled`; disabled domains don't expose the webmail endpoint.
- Certificate for `mail.<domain>` issued (or self-signed fallback per M5) automatically on email-enable.
- install.sh idempotence: two consecutive full runs don't regenerate `bulwark-session.key` or `stalwart-admin.token`; Bulwark source tree isn't re-cloned if the pin matches.
- Branch rebased + CI green + report.

---

### Step 9 — E2E, runbook, blueprint update **[default model]**

**Branch:** `m6-step9-e2e-runbook-docs`.
**Depends on:** Steps 5, 6, 7, 8 all merged.

**Deliverables:**

- `panel-ui/tests/e2e/m6-email.spec.ts` tagged `@m6`:
  1. Log in as admin; create a domain `m6-e2e-<random>.test`.
  2. Enable email on the domain; assert MX, DKIM, DMARC present via a DNS assertion helper (calls `dig @127.0.0.1`).
  3. Create mailbox `alice@m6-e2e.<random>.test`; capture the reveal-once password.
  4. Log in as the user; navigate to `/jabali-panel/mailboxes`; assert alice present.
  5. SSO into webmail; assert inbox loads.
  6. SMTP send a test email from `alice` to `alice` (loopback); IMAP poll for it to arrive. Poll up to 15 retries × 1s = 15s total wall-time; on timeout, tail `journalctl -u jabali-stalwart --since '1 minute ago'` into the test artefact and fail.
  7. Delete mailbox; assert 404 on `/api/v1/mailboxes/:id`.
  8. Disable email; assert DNS records gone (except user-edited overrides).
- `plans/m6-email-runbook.md`: operator runbook:
  - First-enable checklist (ports 25/465/587/993 open in firewall, reverse-DNS for server hostname, public IP matches `dig @8.8.8.8 <server-hostname> A`).
  - DKIM rotation procedure (manual CLI path for v1; reconciler-driven rotation is a follow-up in ADR-0043).
  - Spam-score debugging (`journalctl -u jabali-stalwart -f` + Stalwart CLI).
  - Backup/restore of `/var/lib/stalwart/` (RocksDB checkpoint) + `/etc/jabali-panel/dkim/`.
  - Migrating an existing mailbox from an external server using `stalwart-cli imap-migrate` (pointer to upstream docs — M15 is the real answer).
  - Port reachability test: `openssl s_client -connect <host>:465 -servername <host>` + expected TLS cert output.
  - Troubleshooting matrix:
    - "Bulwark login fails" → `systemctl status jabali-webmail` + `journalctl -u jabali-webmail -n 200`; verify `JMAP_SERVER_URL` reachable from browser (`curl https://mail.<domain>/jmap -I`).
    - "mailbox usage always 0" → reconciler `reconcileMailboxUsage` health; check `journalctl -u jabali-panel | grep mailbox.usage`.
    - "DKIM records missing" → `dig @127.0.0.1 jabali._domainkey.<domain> TXT`; if empty, check `/etc/jabali-panel/dkim/<domain>.key` exists and reconciler enqueued records.
    - "Mail to Gmail delivers but mail to Corp-mail-server bounces/spams" → **known Ed25519 DKIM long-tail issue (see ADR-0043)**. Fallback: enable dual-signing by adding an RSA-2048 selector. Recipe in the runbook escape-hatch section: `jabali domain email-dkim-add-rsa <domain>` (command added in M6.1; in v1, the manual recipe is documented step-by-step — generate RSA-2048 key, write to `/etc/jabali-panel/dkim/<domain>.rsa.key`, add `jabali-rsa._domainkey` TXT record, flip a `dkim_selectors` JSON column on the domain, reload Stalwart).
    - "Port 25 blocked by ISP (AWS/GCP/Hetzner/OVH default)" → operator must request unblock from the upstream provider; panel cannot work around this. Detection: `openssl s_client -connect aspmx.l.google.com:25` from the server times out. Documented as the first checklist item.
    - "Bulwark shows stale mailbox after password change via panel CLI" → `systemctl reload jabali-stalwart` (Bulwark's JMAP session uses Stalwart's directory cache; reload clears it).
- `docs/BLUEPRINT.md` M6 entry flipped from PLANNED → SHIPPED with anchor commits + updated changelog row.
- `CHANGELOG` entry in whatever form the repo uses (currently rolled into the BLUEPRINT changelog table).

**Verification:**

```bash
npx playwright test panel-ui/tests/e2e/m6-email.spec.ts
# Manual: follow the runbook end-to-end on a clean VM, no edits needed.
```

**Exit criteria:**

- E2E spec green on Gitea Actions' Playwright job (host-mode `act_runner`).
- Runbook tested: a fresh operator can enable email on a domain using only the runbook, no Slack.
- BLUEPRINT updated; memory `project_m6_email.md` pointer written to `MEMORY.md`.
- Final report includes: branch SHAs, `git log main..<branch>` summary, rebase confirmation, e2e pass count.

---

## 3. Dependency + parallelism summary

- **Critical path:** Step 1 → Step 4 → Step 9. 3 serial steps.
- **Parallelisable:** {2, 3} after 1; {5, 6, 7, 8} after 4.
- **Branch model:** every step is a feature branch off its parent merge-point. Dispatcher merges each branch to `m6/email-stalwart`, then finally merges `m6/email-stalwart` to `main` after Step 9 closes.
- **CI budget:** 9 PRs × 3 parallel jobs = 27 runs. `act_runner` handles this comfortably — average CI run 8-12 min.

## 4. Risks + kill-switches

| Risk | Likelihood | Kill-switch |
|---|---|---|
| Stalwart v0.16.0 SQL directory filter syntax differs from what ADR-0042 records (upstream docs thin) | Medium | Step 2 has a validate + live-auth smoke; if filters don't match, rewrite Step 2 before dispatching Step 3. Don't wait for Step 3 failure. |
| Generated-column subquery on `email_cached` is rejected by the target MariaDB version | Medium | Fallback in Step 1: use a `BEFORE INSERT/UPDATE` trigger. Verified pre-dispatch. |
| Stalwart native DKIM + auto-DNS leaks through despite our config disabling it (e.g. Stalwart publishes to a cached resolver) | Low | Post-enable assertion in Step 5 E2E: `dig @127.0.0.1 <domain> MX` must return exactly our record, not 2+. Fail fast. |
| Roundcube password-change callback (Step 8) races with a concurrent panel-side rotate-password | Low | Both paths go through `POST /api/v1/mailboxes/:id/rotate-password`; repository uses `SELECT … FOR UPDATE` on the row. Race-free. |
| IMAP/SMTP ports 25/465/587/993 blocked by upstream provider (common on cheap VPS; AWS/GCP block 25 by default) | **High** — external to our control | Step 4's reachability guard catches this pre-enable. Docs in runbook. Operator knows before domain DNS propagates. |
| DKIM key loss via `/etc/jabali-panel/dkim/` accidental wipe | Low | Reconciler's `reconcileDomainEmailState` regenerates + re-publishes; causes a DKIM-key TTL gap (signature validation fails for inflight messages for 1 TTL). Document in runbook. |
| Stalwart service hung (JMAP port alive but IMAP wedged) | Low | systemd `Restart=on-failure` + `WatchdogSec=30` in the unit file. Reconciler also pings IMAP LOGIN for a "health-check" mailbox every 5 min; restarts on 3 consecutive failures. |

## 5. Memory + conventions for each step's dispatchee

Each sub-agent dispatched for a step MUST:

1. Run `mcp__gitnexus__impact` on the target symbol(s) before first edit; include blast radius in its completion report.
2. Run `mcp__gitnexus__detect_changes` before committing; verify scope.
3. Branch first. Never commit to `main`. Never `git push` (dispatcher pushes).
4. Before final report: `git fetch origin main && git rebase origin/main`; re-run `go test -race` + relevant JS/Playwright; confirm in report.
5. For unfamiliar code paths, use `mcp__gitnexus__query` / `context` before Grep.
6. Report format: branch name, commit SHAs on branch, `git log main..<branch>` summary, rebase + re-test confirmation, impact-analysis summary for touched symbols.
7. For Step 8 (strongest-model): reference the M22 SSO-file lessons (`project_m22_rework_sso_file.md` memory) — the one-shot filename + flock + unlink + systemd reaper pattern is the mental model for the SSO token handling; do NOT invent callback-based flows that echo M22's original (failed) magic-link design.

## 6. Post-review operator decisions (2026-04-21)

Operator (shuki) confirmed the §7 open questions with these choices; plan already reflects them:

1. **Webmail:** Bulwark 1.4.14 (not Roundcube). Step 8 fully rewritten for Next.js + JMAP. Single Node service on 127.0.0.1:3000, no per-user PHP-FPM, no separate webmail database. AGPL-v3 license trade-off noted in ADR-0041.
2. **DKIM key type:** Ed25519 (not RSA-2048). Step 1 & 3 DKIM keygen switched to `crypto/ed25519`; DNS TXT record format updated to `k=ed25519`; ADR-0043 records the RFC 8463 compatibility trade-off and the RSA-2048 fallback-selector escape hatch for corporate-receiver delivery issues.
3. **Default per-mailbox quota:** 1 GiB flat (plan already used this).
4. **Mail hostname shape:** per-domain `mail.<domain>` MX label (plan already used this). Dual `/webmail/` path-mount on the primary domain dropped; only `mail.<domain>` is served.

§7 below (open questions) is now closed — kept for audit trail.

## 7. Adversarial-review fold-in (audit trail)

Opus sub-agent reviewed the first draft on 2026-04-21. **Zero CRITICAL, 4 HIGH, 4 MEDIUM/LOW.** Folded in before registration:

- **HIGH #1, #2, #3 (Step 8 SSO security flaws):** Dropped the Kratos → Roundcube SSO shim from v1. Removed `password_enc` column from the schema. Webmail password-change now goes Roundcube → Stalwart SQL directory directly; panel is not in the loop. Webmail SSO deferred to M6.1 once Stalwart JMAP admin-session primitives are confirmed upstream.
- **HIGH #4 (generated-column fallback invisibility to Step 2):** Replaced the STORED generated column with a regular column + 3 triggers that keep `email_cached` in sync. Portable across MariaDB versions; Step 2's Stalwart filter always queries `email_cached` identically.
- **MEDIUM #5 (reachability guard false-positives):** Removed the blocking guard from Step 4's enable path. Added advisory-only `GET /api/v1/domains/:id/email/dns-status` in Step 4's deliverables; runs post-enable. Port-25 ISP-block advisory goes in the runbook.
- **MEDIUM #6 (UI warning plumbing):** New `GET /api/v1/domains/:id/email/dns-status` endpoint surfaces per-record conflict + per-port reachability; Step 7 UI polls it every 10s.
- **MEDIUM #7 (fixture ownership ambiguity):** Golden fixtures explicitly owned by Step 3; Step 4 imports the same `testdata/` directory, no duplication.
- **MEDIUM #8 (JMAP admin token idempotence):** Token is generated in Step 1 (not Step 2), only if `/etc/jabali-panel/stalwart-admin.token` is missing; re-runs read the existing file. Config references it via `!include_path`, not inline.
- **LOW #9 (CLI scoping):** Added the per-domain disjoint-result unit test to Step 6.
- **LOW #10 (E2E flake):** E2E IMAP poll loop bounded to 15s with journal tail on failure.

## 8. Open questions for the user before dispatch (CLOSED 2026-04-21)

- ~~**Webmail choice:**~~ → Bulwark 1.4.14. Recorded in §6.
- ~~**DKIM key type:**~~ → Ed25519. Recorded in §6 + ADR-0043 decision + runbook escape hatch.
- ~~**Default per-mailbox quota:**~~ → 1 GiB flat. Recorded in §6.
- ~~**Mail hostname shape:**~~ → per-domain `mail.<domain>`. Recorded in §6.

All four answered. Plan is dispatch-ready.

---

**Last updated:** 2026-04-21 — adversarial review + operator decisions folded in. Webmail = Bulwark 1.4.14 (Next.js + JMAP); DKIM = Ed25519; quota = 1 GiB; per-domain `mail.<domain>`. §6 + §7 are the audit trail. Plan is dispatch-ready.
