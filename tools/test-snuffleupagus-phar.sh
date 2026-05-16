#!/usr/bin/env bash
# TDD reproducer: the Snuffleupagus base rule bundle MUST whitelist the
# phar:// stream wrapper.
#
# Live bug (mx.jabali-panel.com, deb.sury.org PHP 8.5 build): the rule
# `sp.wrappers_whitelist.list("file,php,http,https,data")` omits phar.
# On that PHP build snuffleupagus enforces the whitelist at the engine
# wrapper-registry level (not gated by snuffleupagus.enabled), stripping
# phar:// — so jabali's OWN wp-cli (a .phar at /opt/wp-cli) cannot
# bootstrap and EVERY app install dies at `wp core download`
# ("Unable to find the wrapper phar"). .150 (Debian-native PHP) didn't
# bite, masking it. jabali ships + requires wp-cli as a phar; phar.readonly
# is On on every host (blocks the phar-write RCE vector) so allowing the
# phar:// read wrapper is safe.
#
# Contract: every shipped *.rules file that sets sp.wrappers_whitelist
# MUST include "phar" in the list.
set -uo pipefail
REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
RULES_DIR="$REPO_ROOT/install/snuffleupagus/rules"
fail=0
note(){ printf '%s\n' "$*"; }

mapfile -t wl_files < <(grep -rl 'sp\.wrappers_whitelist\.list' "$RULES_DIR" 2>/dev/null)
if [[ ${#wl_files[@]} -eq 0 ]]; then
  note "FAIL: no sp.wrappers_whitelist.list found in $RULES_DIR"
  exit 1
fi
for f in "${wl_files[@]}"; do
  line="$(grep -n 'sp\.wrappers_whitelist\.list' "$f" | head -1)"
  if grep -Eq 'sp\.wrappers_whitelist\.list\("[^"]*\bphar\b[^"]*"\)' "$f"; then
    note "PASS: $(basename "$f") whitelists phar  [$line]"
  else
    note "FAIL: $(basename "$f") wrappers_whitelist omits phar  [$line]"
    fail=1
  fi
done

[[ $fail -eq 0 ]] && { note "ALL GREEN"; exit 0; }
note "RED"; exit 1
