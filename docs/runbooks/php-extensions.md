# PHP extensions — operator runbook

Covers the admin PHP Extensions tab (M9.6, shipped 2026-04-19). Read ADR-0031 for the architectural decision.

## Quick reference

| What | Where |
|---|---|
| UI | `/jabali-admin/php-pools` → PHP Extensions tab |
| API | `GET/POST /api/v1/admin/php/versions/:version/extensions[/:ext/apply]` |
| Agent commands | `php.ext.list`, `php.ext.apply` (see `panel-agent/internal/commands/php_ext_*.go`) |
| Allowlist | `internal/phpext/phpext.go` — 63 entries |
| Wire contract | `panel-api/internal/agent/php_ext_contract_test.go` + `testdata/*.json` |
| Module conf on disk | `/etc/php/<v>/mods-available/*.ini` (installed) and `/etc/php/<v>/fpm/conf.d/*.ini` (enabled, symlinks) |

## Common operations

### Enable an extension for everyone on PHP 8.5

1. Admin → PHP → PHP Extensions tab.
2. Pick `8.5` in the dropdown.
3. Find the extension (or use search), click **Install** (for non-installed) or **Enable** (for installed-but-disabled).
4. The apt install takes 5–60s on a warm apt cache, up to 3 min on a cold one. The row state refreshes when done.

### Disable without removing

Use **Disable**. This runs `phpdismod` — the `.ini` symlink is removed from `fpm/conf.d/` but the package stays installed. `phpinfo()` stops showing the module on next FPM reload (reload is automatic as part of apply).

### Remove an extension

**Remove** runs `apt-get remove` and reloads FPM. If another allowlist ext shares the same apt package (most common: xml ↔ dom/simplexml/xmlreader/xmlwriter), the request is rejected with `cannot remove <ext>: still in use by <other>`. Remove the conflicting ext first, or accept that removing any one of the xml group removes all of them.

## Troubleshooting

### `apt-get install: Could not get lock /var/lib/dpkg/lock-frontend`

Unattended-upgrades or another operator is holding the apt lock. The agent serializes apply calls through `aptMu` so two admin actions don't trip on each other, but it can't see a background process.

- Check: `ps auxf | grep -E '(apt|unattended)' | grep -v grep`
- Wait for unattended-upgrades to finish, or: `systemctl stop apt-daily.timer apt-daily-upgrade.timer` if you need to install urgently. Remember to re-enable after.

### `phpenmod: Module XXX ini file doesn't exist under /etc/php/<v>/mods-available`

The ext isn't installed for this version. The API should have caught this in the `requireInstalledBeforeEnable` guard, so if you see it the ini went missing after the panel's `list` read. Reinstall via Install button; the apt install places the ini back.

### Row says "Installed: No" right after I clicked Install and saw a success toast

The toast fired when the agent returned 200. The tab re-fetches afterward, but if FPM reload is still in progress (rare — reload is SIGUSR2, ≤100ms) the new state might not yet be in `fpm/conf.d/`. Hit the dropdown again to refetch, or click the tab twice.

### `cannot remove xml: still in use by dom`

Expected. `xml`, `dom`, `simplexml`, `xmlreader`, `xmlwriter` are five logical extensions that all ship in the same Debian `php<v>-xml` package. To remove any one of them, remove all first, or accept losing the group.

### An extension isn't in the allowlist

By design. The allowlist is versioned with the panel; adding an extension is a code change in `internal/phpext/phpext.go` + a release. If a customer is asking for something specific, note it for the next panel cycle.

### `ambiguous target; enable/disable mysqli or pdo_mysql directly`

You tried to Enable or Disable `mysql`, which is the meta-install for `php<v>-mysql` (ships both `mysqli` and `pdo_mysql`). The agent refuses because it can't know which one you meant. Operate on `mysqli` or `pdo_mysql` directly.

### apt succeeded but the module isn't loaded

Reload didn't fire or FPM is masked. Check:

```
systemctl status php<v>-fpm.service
systemctl list-units 'jabali-fpm@*.service' --state=running
```

Manual recovery:

```
# Global master (if used):
systemctl reload php<v>-fpm.service

# Per-user masters pinned to <v>:
for u in $(ls /etc/jabali-panel/user-phpver/); do
  [ "$(cat /etc/jabali-panel/user-phpver/$u)" = "<v>" ] \
    && systemctl reload jabali-fpm@$u.service
done
```

### I want to install an extension that isn't in the 63-entry allowlist, just once

Don't. If you ssh in and `apt install php8.5-whatever`, the next admin `list` call will show the ext as "installed but not in allowlist" — actually no, the panel list call filters to the allowlist, so the manually-installed ext will be invisible to the UI. It won't break anything, but the operator has no way to manage it through the panel. Submit a PR to add it properly.

## On-call checklist

1. Customer reports "extension not loading": check `list` API response for `installed=true, enabled=true` on their PHP version. If both true, the ext is loaded — ask them to check `php -m` under the right user/pool.
2. apt stuck: see the lock-file troubleshooting above. The 5-min agent timeout will fire a 504; clear the lock and retry.
3. FPM won't reload: `systemctl status` → is it masked (ADR-0025 distro-global mask)? If yes, confirm per-user masters are running; the distro-global is intentionally down.
4. Unexpected allowlist entry behaviour: cross-check `internal/phpext/phpext.go`. ADR-0031 §Decision documents the classification logic.

## Related

- ADR-0031 — architectural decision record
- ADR-0023 — per-user PHP-FPM pools
- ADR-0025 — per-user systemd slices (masks distro-global php<v>-fpm)
- Plan: `plans/php-extensions-tab.md`
