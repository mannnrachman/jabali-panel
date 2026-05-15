#!/usr/bin/env bash
# TDD reproducer: ensure_wpcli_symlink must (re)create the PATH-visible
# wp symlink when /opt/wp-cli/current exists but <bindir>/wp is gone.
#
# Live bug (mx.jabali-panel.com): wp-cli installed under /opt/wp-cli
# (phar + current symlink present) but /usr/local/bin/wp absent
# (uninstall residue). install_wpcli's idempotency guard only checks
# the /opt/wp-cli pair, early-returns, never recreates the external
# link; install_wpcli isn't in provision so `jabali update` can't
# self-heal -> every app install dies at `wp core download`
# ("Failed to find executable wp").
#
# Contract: ensure_wpcli_symlink <wp_root> <bindir> creates a valid
# <bindir>/wp -> <wp_root>/current symlink, idempotently.
set -uo pipefail
REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
fail=0
note(){ printf '%s\n' "$*"; }
sandbox="$(mktemp -d)"; trap 'rm -rf "$sandbox"' EXIT
wp_root="$sandbox/opt/wp-cli"; bindir="$sandbox/usr/local/bin"
mkdir -p "$wp_root" "$bindir"
printf '#!/usr/bin/env php\n' > "$wp_root/wp-cli-2.12.0.phar"
chmod +x "$wp_root/wp-cli-2.12.0.phar"
ln -s "wp-cli-2.12.0.phar" "$wp_root/current"
# external wp link intentionally ABSENT (the bug state)

if ! grep -q '^ensure_wpcli_symlink()' "$REPO_ROOT/install.sh"; then
  note "FAIL: ensure_wpcli_symlink() not defined (RED expected pre-fix)"; exit 1
fi
eval "$(awk '/^ensure_wpcli_symlink\(\) \{/,/^\}/' "$REPO_ROOT/install.sh")"
_log(){ :;}; _warn(){ :;}; _ok(){ :;}

ensure_wpcli_symlink "$wp_root" "$bindir"

if [[ -L "$bindir/wp" ]]; then
  tgt="$(readlink "$bindir/wp")"
  if [[ "$tgt" == "$wp_root/current" || "$tgt" == */current ]]; then
    note "PASS: $bindir/wp -> $tgt"
  else note "FAIL: wp symlink wrong target: $tgt"; fail=1; fi
else note "FAIL: $bindir/wp not created"; fail=1; fi
# idempotent second call must not error / must keep the link
ensure_wpcli_symlink "$wp_root" "$bindir"
[[ -L "$bindir/wp" ]] && note "PASS: idempotent" || { note "FAIL: idempotent run broke link"; fail=1; }

[[ $fail -eq 0 ]] && { note "ALL GREEN"; exit 0; }
note "RED"; exit 1
