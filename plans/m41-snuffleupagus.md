# M41 — Snuffleupagus PHP RCE hardening

**Status:** Blueprint
**ADR:** 0088
**Branch:** `m41/snuffleupagus`
**Migration:** 000109
**Supersedes:** M40 AppArmor (parked, ADR-0086)

## Goal

Ship Snuffleupagus across every PHP minor Jabali installs, with a
Jabali-maintained rule bundle, three operator-selectable modes, M14
notifications on incidents, and PHP-CLI bypass closure. Zero per-customer
config surface.

## Non-goals

- Per-domain or per-customer Snuffleupagus rules.
- Customer-facing Security UI panel.
- Daemon hardening (was M40; parked).
- Importing third-party commercial rule sets.

## Threat model (in scope)

- WordPress / Drupal / Joomla / PrestaShop / Magento / OpenCart plugin
  RCE via `eval()`, `assert()`, `create_function()`, dynamic includes,
  unsafe `unserialize()` of cookies / POST.
- Webshell drops via file-upload bypass writing `.php` under docroot.
- Customer with shell access running `php -c /tmp/own.ini` to evade
  our pinned `sp.configuration_file`.
- Bot scanning known-vulnerable CMS endpoints.

## Threat model (out of scope, deferred)

- Sandboxing arbitrary tenant binaries (M12 SFTP nspawn already covers).
- L7 WAF (CrowdSec AppSec / ModSecurity covers).
- Outbound exfil after a successful exploit (M34 per-user egress covers).
- Daemon RCE (M40 was the answer; parked).

---

## Step 1 — ADR + plan + repo skeleton

**Branch:** `m41/snuffleupagus` off `main`.

Create:

```
docs/adr/0088-snuffleupagus-php-hardening.md      [DONE in this wave]
plans/m41-snuffleupagus.md                         [DONE in this wave]
install/snuffleupagus/
  rules/
    .gitkeep
  build/
    .gitkeep
panel-agent/internal/commands/security_snuffleupagus.go   [stub]
panel-api/internal/api/security_snuffleupagus.go          [stub]
panel-ui/src/hooks/useSecuritySnuffleupagus.ts            [stub]
panel-ui/src/shells/admin/security/AdminSecuritySnuffleupagus.tsx [stub]
```

Stubs register routes returning `{"enabled": false}` so all subsequent
waves slot in without route-renaming churn.

**Mergeable:** yes (additive, all stubs default to disabled).

## Step 2 — Source-build pipeline (every PHP minor)

Add `install/snuffleupagus/build/build.sh` that takes one arg `<php-minor>`
(e.g. `8.3`) and:

1. Validates `php-config-<minor>` is on PATH.
2. Downloads the upstream tarball pinned by version+SHA256 from a constant
   in `install.sh` (e.g. `SNUFFLEUPAGUS_VERSION=0.10.0`,
   `SNUFFLEUPAGUS_SHA256=...`).
3. `phpize`, `./configure --with-php-config=...`, `make -j$(nproc)`.
4. Installs `snuffleupagus.so` to
   `/usr/lib/php/jabali-snuffleupagus/<minor>/snuffleupagus.so`.
5. Writes `/etc/php/<minor>/mods-available/jabali-snuffleupagus.ini` with
   `extension=/usr/lib/php/jabali-snuffleupagus/<minor>/snuffleupagus.so`
   and `sp.configuration_file=/etc/jabali/snuffleupagus/active.rules`.
6. Symlinks the mod into `/etc/php/<minor>/fpm/conf.d/30-jabali-snuffleupagus.ini`
   and `/etc/php/<minor>/cli/conf.d/30-jabali-snuffleupagus.ini`.

`install.sh` calls `build.sh` once per minor enumerated by the existing
M9.6 phpext loop. Idempotent: skips builds where the artifact already
matches the pinned SHA256.

**CI gate:** new job `m41-build` on every PR builds Snuffleupagus against
the matrix of supported PHP minors (8.1, 8.2, 8.3, 8.4) and asserts
`php -d extension=/path/snuffleupagus.so -m | grep snuffleupagus`.

**Mergeable:** yes (builds artefacts but `mode=off` ships, runtime no-op).

## Step 3 — Jabali-maintained rule bundle

`install/snuffleupagus/rules/` checked into the repo:

```
00-base.rules           # always-on safety: assert disable, eval guard,
                        # readonly_exec for system/exec/popen/passthru,
                        # disable_function for dl/show_source/phpinfo
                        # in production
10-wordpress.rules      # whitelist legitimate WP exec patterns; harden
                        # wp-admin/install.php + xmlrpc.php; ini-restore
                        # protection
10-drupal.rules         # CKEditor + Image Toolkit shellouts whitelisted;
                        # block writes to sites/default/settings.php
10-joomla.rules         # JEXEC guard, language-string includes,
                        # block writes to /administrator/cache
10-prestashop.rules     # SmartyCompiler include guards
10-magento.rules        # var/generation, var/view_preprocessed
                        # whitelist; block direct ./pub/static writes
99-jabali-overrides.rules  # operator-flagged false positives, per-rule
                           # kill via DB + reconciler render
simulation.rules        # template that adds .simulation() to every rule
                        # in the active set when mode=simulation
README.md               # how the bundle composes; how to add a CMS
```

Each `.rules` file starts from upstream `config/example.rules` per app,
trimmed and reviewed for shared-hosting realism (no `--enable-strict`
flags that break common plugins). All rules tagged with a stable
`name=` key so the per-rule kill list can target them.

**CI gate:** rules-parse job that runs
`php -d extension=snuffleupagus.so -d sp.configuration_file=...` per
PHP minor and asserts no parse errors. Plus a WordPress install canary
that runs `wp core install` with rules active and asserts exit 0.

**Mergeable:** yes (rules sit on disk; nothing references them yet).

## Step 4 — DB schema + reconciler config render

Migration `000109_create_snuffleupagus.up.sql`:

```sql
CREATE TABLE snuffleupagus_state (
  id          TINYINT      NOT NULL DEFAULT 1 PRIMARY KEY,
  mode        ENUM('off','simulation','enforce') NOT NULL DEFAULT 'off',
  last_applied_at  DATETIME(6) NULL,
  last_applied_sha256 CHAR(64) NULL,
  CHECK (id = 1)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

INSERT INTO snuffleupagus_state (id, mode) VALUES (1, 'off');

CREATE TABLE snuffleupagus_rule_overrides (
  rule_name   VARCHAR(128) NOT NULL PRIMARY KEY,
  enabled     TINYINT(1)   NOT NULL DEFAULT 1,
  reason      VARCHAR(512) NULL,
  set_by_user_id CHAR(26)  NULL,
  set_at      DATETIME(6)  NOT NULL DEFAULT CURRENT_TIMESTAMP(6)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE snuffleupagus_incidents (
  id          BIGINT       NOT NULL AUTO_INCREMENT PRIMARY KEY,
  ts          DATETIME(6)  NOT NULL,
  rule_name   VARCHAR(128) NOT NULL,
  action      ENUM('log','block','simulated_block') NOT NULL,
  source_ip   VARBINARY(16) NULL,
  request_uri VARCHAR(2048) NULL,
  php_version VARCHAR(8)   NULL,
  domain_id   CHAR(26)     NULL,
  raw         TEXT         NULL,
  KEY ix_ts (ts),
  KEY ix_rule (rule_name)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
```

Reconciler `panel-api/internal/reconciler/snuffleupagus.go`:

- Reads `snuffleupagus_state.mode` + `snuffleupagus_rule_overrides`.
- Composes the rule file: concat 00-base + 10-* + 99-overrides, with
  per-rule kill list applied by stripping rules whose `name=` matches
  a disabled override, and `.simulation()` wrapping every rule when
  `mode=simulation`.
- Computes SHA256 of the rendered output. If unchanged from
  `last_applied_sha256`, no-op. If changed, writes
  `/etc/jabali/snuffleupagus/active.rules` atomically (`mktemp` +
  `rename`), updates `last_applied_*`, and posts agent
  `snuffleupagus.reload` (which graceful-reloads every per-user FPM
  pool).

`mode=off` renders an empty rules file with `sp.global.enable=0`.

**Mergeable:** yes (reconciler is registered but state stays at `off`,
so reload is a no-op).

## Step 5 — PHP-CLI bypass closure

Two layers:

**Layer A — `jabali-php` wrapper** at `/usr/local/bin/jabali-php`:

```bash
#!/usr/bin/env bash
exec /usr/bin/php -c /etc/jabali/snuffleupagus/cli.ini "$@"
```

`cli.ini` pins `sp.configuration_file` and disables the
`extensions_dir` / `include_path` overrides via `disable_overrides`.

**Layer B — nspawn image mount + audit rule:**

Per-user nspawn image (M12 SFTP sandbox path) mounts
`/etc/jabali/snuffleupagus/` read-only. Customer cron jobs and SFTP-
shell PHP execs go through the wrapper because the real
`/usr/bin/php` is replaced inside the namespace by a symlink to
`/usr/local/bin/jabali-php`.

`/etc/audit/rules.d/jabali-snuffleupagus.rules`:

```
-a always,exit -F arch=b64 -S execve -F path=/usr/bin/php -F auid>=1000 -F auid!=4294967295 -k jabali_php_bypass
```

Agent's existing M39 audit-tail (auditd → notifications.send) picks up
the key `jabali_php_bypass` and fires a high-severity event with the
auid + cwd.

**Mergeable:** yes (wrapper sits next to `/usr/bin/php`; no-op until
audit rule is loaded — gated behind a feature flag for Wave C).

## Step 6 — Agent ingest pipeline

Agent command `snuffleupagus.reload` — already referenced by Step 4 —
implemented in `panel-agent/internal/commands/security_snuffleupagus.go`:

- Validates `/etc/jabali/snuffleupagus/active.rules` exists and parses.
- For each running per-user FPM pool, sends `service reload` (graceful).
- Returns the per-pool result map for the reconciler to record.

Agent journal-tail goroutine (started at agent boot if a state row
exists with mode != off):

- `journalctl -t snuffleupagus -f -o json --since=now`.
- Parses each JSON line into the incident schema.
- INSERT into `snuffleupagus_incidents` via the agent's panel-DB writer
  (M14 already has this connection).
- For action ∈ {block, simulated_block}, calls `notifications.send`
  with kind `snuffleupagus.incident`, severity high (block) / medium
  (simulated_block).

**Mergeable:** behind state.mode != off check, so until Wave E flips
the default, this stays a no-op on fresh installs.

## Step 7 — Panel-API REST routes

`panel-api/internal/api/security_snuffleupagus.go`:

- `GET  /admin/security/snuffleupagus/status` →
  `{ mode, last_applied_at, last_applied_sha256, php_versions:[{minor,loaded}], rules_count }`
- `POST /admin/security/snuffleupagus/mode` body `{ "mode": "..." }` —
  validates against the enum, updates the state row, calls reconciler.
- `GET  /admin/security/snuffleupagus/rules` →
  `{ rules: [{name, source_file, enabled}] }`. `source_file` is the
  bundle file the rule came from. `enabled` reflects override-list state.
- `POST /admin/security/snuffleupagus/rules/:name/toggle` body
  `{ "enabled": false, "reason": "WooCommerce false positive" }`.
- `GET  /admin/security/snuffleupagus/incidents?since=&limit=&rule=&domain=`
  → paginated incident list, joined to `domains.name` for display.

All routes gated by `middleware.RequireAdmin()`.

**Mergeable:** yes; UI lands in Step 8.

## Step 8 — Panel UI: Security → Snuffleupagus card

`panel-ui/src/shells/admin/security/AdminSecuritySnuffleupagus.tsx`:

- Status banner: mode chip (off / simulation / enforce), last-apply
  timestamp, per-PHP-version load indicators (8.1 ✓ 8.2 ✓ 8.3 ✓ 8.4 ✓).
- Mode segmented control: `Off | Simulation | Enforce`. Confirm modal
  on Enforce (pulls last 7 days of simulated-block counts so operator
  sees what flips on).
- Recent incidents AntD Table: timestamp, rule, action, IP, domain,
  request URI; filter by rule + IP + domain; pagination via
  TanStack Query + the SearchableTable shared component.
- Rules list: each rule with toggle (admin override) and reason
  textfield; bulk export of overrides for off-host backup.
- Link to runbook.

Hook `useSecuritySnuffleupagus.ts` mirrors the M40 AppArmor hook shape
(same prefix-relative-to-`/api/v1` rule we re-affirmed today).

Tab registration in `AdminSecurityPage.tsx` adds `snuffleupagus` to
the `TAB_KEYS` between `malware` and `ufw`. M40 `apparmor` tab is
removed from the same array.

**Mergeable:** yes; visible to admins immediately. With `mode=off` the
card just shows "Disabled".

## Step 9 — E2E + runbook + first-install default flip

Playwright suite `tests/e2e/security-snuffleupagus.spec.ts`:

- Toggles mode off → simulation → enforce → simulation → off, asserts
  reconciler ran each transition (last_applied_at moves).
- Installs WordPress on a test domain with `mode=enforce`, asserts
  `wp core install` exits 0 (canary against false positives).
- Forces a hit: `curl 'https://...?...=test%20-c%20id'` against a
  controlled endpoint that triggers a base rule (e.g.
  `disable_function` on `system`); asserts an incident appears in the
  UI table within 5 seconds and a notification fires (mock channel).
- Validates the per-rule kill switch: disable the triggered rule,
  re-fire the request, assert no new incident.
- Validates the bypass mitigation: invokes
  `aa-exec -p something /usr/bin/php -r 'system("id");'` (or the M12
  nspawn equivalent) and asserts `jabali_php_bypass` audit event +
  notification.

Runbook at `plans/m41-snuffleupagus-runbook.md`:

- False-positive triage workflow (incident → rule → override row → re-render)
- Onboarding a new CMS to the bundle (rule file template, CI canary, soak)
- Emergency rollback (set mode=off, reconciler re-renders empty file
  on next tick, FPM pools graceful-reload)
- Upstream version bump procedure (pin SHA256, rebuild matrix, soak)

Wave E flip:

- `install.sh` post-install seeds `snuffleupagus_state.mode = 'simulation'`
  on fresh installs (idempotent: only on the row's first creation; never
  downgrades an existing operator's chosen mode).
- Existing installs upgrading to M41 keep `mode = 'off'` until the
  operator opts in via the UI.

**Mergeable:** yes — this is the wave that flips first-install
defaults. Coordinate with release notes.

---

## Wave dispatch plan

- Wave A: steps 1, 2 (additive scaffolding + per-PHP build pipeline).
- Wave B: steps 3, 4 (rules + DB + render).
- Wave C: step 5 (CLI bypass closure — gated behind feature flag).
- Wave D: steps 6, 7, 8 (ingest + REST + UI).
- Wave E: step 9 (E2E + runbook + first-install default).

## Open questions / future work

- **Rule auto-generation for existing customer accounts:** the upstream
  `generate_rules.php` scans a docroot for already-present dangerous
  function calls and emits whitelist rules. Worth adding as a per-domain
  "audit-only onboarding" tool in M41.1 once the base bundle is stable.
- **CrowdSec scenario integration:** hook into the existing CrowdSec
  bouncer (M27) so a Snuffleupagus block at the PHP layer also triggers
  an IP ban at the network layer. Deferred to M41.2.
- **Customer-facing incident view:** if operators want their tenants to
  see their own blocked attempts (transparency), expose a read-only
  per-domain incident list under the user shell. Deferred until
  customer demand.
