# M41 — Snuffleupagus PHP hardening runbook

ADR: [0088-snuffleupagus-php-hardening.md](../docs/adr/0088-snuffleupagus-php-hardening.md)
Plan: [m41-snuffleupagus.md](./m41-snuffleupagus.md)

## What it is

Snuffleupagus is a Zend extension that hooks dangerous PHP APIs (eval,
system, unserialize, ini_set, etc.) and either logs or blocks abuse
patterns. We ship it pre-built per PHP minor, with a Jabali-maintained
rule bundle (`/usr/share/jabali/snuffleupagus/rules/`), and surface a
3-mode toggle in the admin Security tab.

```
admin UI → POST /admin/security/snuffleupagus/mode
              → snuffleupagus_state.mode (DB-as-truth)
              → SnuffleupagusReconciler renders /etc/jabali/snuffleupagus/active.rules
              → agent snuffleupagus.reload graceful-restarts every phpX.Y-fpm.service
```

## Layers

| Layer | Path | Owner |
|---|---|---|
| Build | `install/snuffleupagus/build/build.sh` | install.sh per PHP minor |
| Bundle | `/usr/share/jabali/snuffleupagus/rules/*.rules` | mirrored from `install/snuffleupagus/rules/` |
| Active | `/etc/jabali/snuffleupagus/active.rules` | reconciler-rendered, atomic write, SHA-pinned |
| Loader | `/etc/php/<minor>/{fpm,cli}/conf.d/30-jabali-snuffleupagus.ini` | install.sh |
| State | `snuffleupagus_state` (singleton), `snuffleupagus_rule_overrides`, `snuffleupagus_incidents` | panel-api |

## Modes

| Mode | What happens |
|---|---|
| **off** | `sp.global.enable(0);` only — rules don't load. |
| **simulation** | Bundle loads, every `.drop()` action wrapped with `.simulation()`. Incidents logged, request continues. **Default on fresh install.** |
| **enforce** | Bundle loads as-is. Incidents logged AND request dropped. Flip here only after a 7-day soak. |

## Day-1 install

1. `bash install.sh` (or `jabali update`) on a host with `JABALI_PHP_VERSIONS="8.1 8.2 8.3 8.4 8.5"` (or any subset). install.sh:
   - apt-installs `build-essential libpcre2-dev`
   - Calls `install/snuffleupagus/build/build.sh <minor>` per minor (downloads pinned tag from GitHub, verifies SHA256, phpize+make+install)
   - Writes `/etc/php/<minor>/mods-available/jabali-snuffleupagus.ini` + symlinks into both `fpm/conf.d/` and `cli/conf.d/`
   - Mirrors `install/snuffleupagus/rules/*.rules` into `/usr/share/jabali/snuffleupagus/rules/`
   - Writes `/etc/audit/rules.d/jabali-snuffleupagus.rules` (key=`jabali_php_bypass`)
2. Migration `000109_create_snuffleupagus.up.sql` seeds `snuffleupagus_state` row 1 with `mode='simulation'`.
3. First panel-api startup invokes the reconciler — renders the bundle into `active.rules`, hashes it, calls `snuffleupagus.reload` once. `php -m | grep snuffleupagus` should now return `snuffleupagus` on every minor.

Verify:
```bash
# Loaded?
php -m | grep snuffleupagus
php8.5 -m | grep snuffleupagus

# Mode + SHA
curl --unix-socket /run/jabali-panel.sock http://x/api/v1/admin/security/snuffleupagus/status

# Active file present + parseable
sp.configuration_file=/etc/jabali/snuffleupagus/active.rules php -r 'echo "ok\n";'
```

## Day-7 soak review

```bash
# Last week's incidents bucketed by rule.
curl --unix-socket /run/jabali-panel.sock \
  "http://x/api/v1/admin/security/snuffleupagus/incidents?since=$(date -u -d '7 days ago' +%Y-%m-%dT%H:%M:%SZ)&limit=500" \
| jq '.data | group_by(.rule_name) | map({rule:.[0].rule_name, count:length})'
```

For each rule with non-zero incidents and the `simulated_block` action:
- Inspect raw lines in the UI Incidents table.
- If legitimate tenant traffic: open the rules modal, toggle the rule off, attach a reason. The reconciler will rerender + reload on the next mode/rule mutation; the disabled rule is excluded.
- If a real attack pattern: leave enabled.

When the simulated_block ratio across rules is acceptable, click **Mode → Enforce** in the admin UI. The confirmation modal exists to make this an explicit operator decision.

## Triage flow (false positive)

1. Tenant reports legitimate request blocked.
2. Find the incident in the admin Snuffleupagus card → take note of `rule_name`.
3. Open **Manage rules** modal → search for the rule → flip the switch off.
4. Reason field captures the why for the audit trail.
5. Reconciler renders new active.rules, agent reloads FPM. Tenant retries — should succeed.

If the rule is wholly broken / over-broad, raise via the standard issue tracker: it'll get pulled out of the next bundle revision rather than left disabled per host.

## Bundle update (`jabali update`)

`jabali update` pulls the latest commit + reruns `install_snuffleupagus`:
- New `/usr/share/jabali/snuffleupagus/rules/*.rules` files mirrored.
- `install_audit_php_bypass` re-emits the audit rules (idempotent).
- Reconciler-on-boot re-renders active.rules; SHA256 changes when the bundle changed → new file written + reload triggered.

Operator overrides survive updates: `snuffleupagus_rule_overrides` is keyed by `rule_name`, the bundle revision doesn't drop rows.

## Notifications

`snuffleupagus.incident.detected` fires every 60s ingest tick with one envelope per batch (cooldown 10m). Severity:
- `warning` if any block fired this batch.
- `info` otherwise (simulation / log only).

Body lists up to 10 incidents; deeplink jumps to the Security tab.

Toggle off per-channel via `/admin/notifications/events`.

## CLI bypass detection

`audit.rules.d/jabali-snuffleupagus.rules` watches every `/usr/bin/phpX.Y` execve from `auid>=1000`, key=`jabali_php_bypass`. Catches:
- `php -n shell.php` (skips conf.d, sp.so not loaded)
- Direct invocation outside the FPM pool

To pivot incidents:
```bash
ausearch -k jabali_php_bypass --start today
```

This is a detection layer, not a prevention layer. Pure prevention would require kernel binary-execution restrictions (LSM/AppArmor) which the M40 work-stream concluded are operator-tuning surface we don't want.

## Removal / rollback

```bash
# Disable cleanly: mode=off — rules don't load, FPM still works.
curl --unix-socket /run/jabali-panel.sock \
  -XPOST -H 'content-type: application/json' \
  --data '{"mode":"off"}' \
  http://x/api/v1/admin/security/snuffleupagus/mode

# Hard remove (debugging emergency only):
for d in /etc/php/*/; do
  rm -f "$d"/{fpm,cli}/conf.d/30-jabali-snuffleupagus.ini
done
systemctl reload-or-restart 'php*-fpm.service'
```

DB rows + bundle survive a hard remove — `mode=off` plus a re-run of `install_snuffleupagus` brings everything back.

## Known gaps

- E2E Playwright coverage queued (no upstream blocker).
- The bundle is curated from upstream Snuffleupagus examples + jvoisin community rule sets; coverage of long-tail CMSs (CodeIgniter, CakePHP) lives in 10-*.rules and is currently empty.
- Incident `request_uri` and `php_version` extraction from the journalctl line is best-effort; deeper telemetry (e.g. tenant domain attribution) requires emitting a JSON-formatted log via `sp.log_media` once Snuffleupagus 0.11+ ships JSON logging.
