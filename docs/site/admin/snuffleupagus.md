# Snuffleupagus

Security → Snuffleupagus. PHP runtime hardening loaded as a Zend extension into every installed PHP version.

## Default rule pack

The installer ships a baseline rule set that blocks the most common compromise vectors:

- Reject `eval` invocations against tainted request data.
- Disallow `include` / `require` from `php://`, `data:`, and remote URLs.
- Track taint flow from `$_GET` / `$_POST` into shell-execution sinks; block when the sink would receive tainted input.
- Block known-bad shellcode patterns by signature.
- Cookie integrity protection (signed session cookies) to defeat session hijacking.

The full rule set is shipped as `.rules` files under `/etc/php/<version>/snuffleupagus.rules.d/`.

## Per-app exception files

Some apps (WordPress, Moodle, NextCloud) legitimately use patterns that the baseline blocks. The panel ships per-app exception files for these cases, applied automatically when the app is installed via [Applications](./applications.md). Each exception scopes to a specific install path so the relaxation does not leak to other sites on the same host.

## Page surface

- **Enabled per version** — toggle Snuffleupagus on or off for each installed PHP version.
- **Rule pack version** — currently shipped; "update available" badge when a newer pack is in the next release.
- **Per-app exception count** — quick summary of how many app installs have applied exception files.
- **Recent blocks** — last 100 Snuffleupagus block events with path, rule, and offending input excerpt.

## Operator-defined exceptions

For a one-off legitimate pattern, the operator may add a rule under `/etc/php/<version>/snuffleupagus.rules.d/local/`. The local directory is not overwritten by `jabali update`.

## Performance

Snuffleupagus runs as a Zend extension; overhead is sub-microsecond per request for typical pages. Disable per-version only when isolating a performance regression and re-enable afterward.

## Related

- [PHP Manager](./php-manager.md) for per-version PHP configuration.
- [Removed Features](../removed-features.md) for context on the WAF stack changes.
