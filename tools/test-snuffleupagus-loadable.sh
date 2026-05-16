#!/usr/bin/env bash
# Regression: Snuffleupagus must actually load in the per-user php-fpm
# pool (User=<hosting-user>), not just in root-context `php-fpm -i`.
#
# Bug (live on mx.jabali-panel.local 2026-05-16): install.sh runs under
# umask 077, so:
#   - build.sh `mkdir -p /usr/lib/php/jabali-snuffleupagus/<minor>` -> 0700
#   - install.sh `cat > .../mods-available/jabali-snuffleupagus.ini`   -> 0600
# The per-user fpm master parses conf.d + dlopen's the .so as the
# unprivileged hosting user; a 0700 lib dir / 0600 ini is SILENTLY
# skipped -> snuffleupagus never loads on ANY web traffic -> zero PHP
# defense, while root `php-fpm -i` still shows it (masking the bug).
#
# Contracts:
#   S1 build.sh chmods the /usr/lib/php/jabali-snuffleupagus dir chain 0755
#   S2 install.sh chmods the mods-available sp ini 0644 (not umask 0600)
#   S3 ensure_snuffleupagus_loadable() defined and called in
#      provision_new_software (existing-host self-heal; build.sh is
#      idempotent and skips the in-build chmod when the .so exists)
set -euo pipefail
R="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
I="$R/install.sh"; B="$R/install/snuffleupagus/build/build.sh"
fail(){ echo "FAIL: $*" >&2; exit 1; }
pass(){ echo "PASS: $*"; }

grep -Eq 'chmod 0755 /usr/lib/php/jabali-snuffleupagus "\$OUT_DIR"' "$B" \
  || fail "S1: build.sh does not chmod 0755 the jabali-snuffleupagus dir chain"
pass "S1: build.sh chmods .so dir chain 0755"

grep -Eq 'chmod 0644 "/etc/php/\$minor/mods-available/jabali-snuffleupagus.ini"' "$I" \
  || fail "S2: install.sh does not chmod 0644 the sp mods-available ini"
pass "S2: install.sh chmods sp ini 0644"

grep -Eq '^ensure_snuffleupagus_loadable\(\)' "$I" \
  || fail "S3: ensure_snuffleupagus_loadable() not defined"
prov="$(awk '/^provision_new_software\(\)/,/^}/' "$I")"
grep -Eq 'ensure_snuffleupagus_loadable' <<<"$prov" \
  || fail "S3: provision_new_software does not call ensure_snuffleupagus_loadable"
pass "S3: ensure_snuffleupagus_loadable defined + called in provision"

echo "ALL PASS: Snuffleupagus loadable-by-unprivileged-fpm contracts hold"
