#!/usr/bin/env bash
# TDD: provision must re-mirror the snuffleupagus rule bundle into
# /usr/share so the reconciler (which prefers /usr/share over the repo
# fallback and only falls back if /usr/share is ABSENT) renders the
# UPDATED rules on an existing host. Without this, a jabali update that
# ships a 00-base.rules change (e.g. the phar whitelist fix 171b4ff4)
# never reaches existing hosts: stale /usr/share -> stale active.rules.
#
# Contract: ensure_snuffleupagus_bundle_synced <repo_rules> <share_dir>
# copies every *.rules from repo_rules into share_dir (idempotent).
set -uo pipefail
REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
fail=0; note(){ printf '%s\n' "$*"; }
sandbox="$(mktemp -d)"; trap 'rm -rf "$sandbox"' EXIT
repo="$sandbox/repo"; share="$sandbox/share"
mkdir -p "$repo" "$share"
# repo has the NEW rule (phar); share has the OLD (stale) one
printf 'sp.wrappers_whitelist.list("file,php,http,https,data,phar");\n' > "$repo/00-base.rules"
printf 'sp.wrappers_whitelist.list("file,php,http,https,data");\n' > "$share/00-base.rules"
printf 'X\n' > "$repo/10-wordpress.rules"

if ! grep -q '^ensure_snuffleupagus_bundle_synced()' "$REPO_ROOT/install.sh"; then
  note "FAIL: ensure_snuffleupagus_bundle_synced() not defined (RED expected pre-fix)"; exit 1
fi
eval "$(awk '/^ensure_snuffleupagus_bundle_synced\(\) \{/,/^\}/' "$REPO_ROOT/install.sh")"
_log(){ :;}; _warn(){ :;}; _ok(){ :;}

ensure_snuffleupagus_bundle_synced "$repo" "$share"

if grep -q 'phar' "$share/00-base.rules"; then
  note "PASS: stale share 00-base.rules refreshed with phar"
else note "FAIL: share 00-base.rules still stale (no phar)"; fail=1; fi
[[ -f "$share/10-wordpress.rules" ]] && note "PASS: new bundle file mirrored" \
  || { note "FAIL: 10-wordpress.rules not mirrored"; fail=1; }
ensure_snuffleupagus_bundle_synced "$repo" "$share"  # idempotent
grep -q phar "$share/00-base.rules" && note "PASS: idempotent" \
  || { note "FAIL: idempotent run regressed"; fail=1; }

[[ $fail -eq 0 ]] && { note "ALL GREEN"; exit 0; }
note "RED"; exit 1
