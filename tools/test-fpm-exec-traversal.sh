#!/usr/bin/env bash
# TDD reproducer for the per-user FPM crash-loop caused by an
# unreadable /etc/jabali-panel parent directory.
#
# Bug (observed live on mx.jabali-panel.local / 192.168.100.150):
#   /etc/jabali-panel was mode 0750 root:jabali. jabali-fpm@<user>.service
#   runs ExecStart as User=<hosting-user> (NOT root, NOT group jabali).
#   fpm-exec does `cat /etc/jabali-panel/user-phpver/<user>` AFTER the
#   privilege drop, so it cannot traverse the 0750 parent -> "Permission
#   denied" -> `set -e` exit 1 -> systemd restart loop (observed restart
#   counter 595) -> every hosted PHP site permanently 502.
#
#   The dir is *created* 0750 (install.sh) and only loosened to 0755 as
#   a side-effect buried inside install_powerdns + the sso-key step. Any
#   `jabali update` whose provision path does not re-run those two
#   functions leaves it 0750 and breaks all per-user PHP.
#
# Contracts under test (all must hold for the bug to stay fixed):
#   C1 install.sh creates /etc/jabali-panel with mode 0755, never 0750.
#   C2 provision_new_software (runs on every `jabali update`) calls an
#      idempotent ensure_* that re-asserts /etc/jabali-panel is 0755,
#      independent of install_powerdns / sso-key running.
#   C3 fpm-exec never reads anything under /etc/jabali-panel (it must be
#      structurally immune to the parent-perm regression).
#   C4 fpm-exec reads the resolved PHP version from the per-user runtime
#      dir /run/php/jabali-<user>/phpver instead.
#   C5 fpm-pre-start (root, via "+" ExecStartPre) writes that runtime
#      phpver file so the decoupling is real, not a dangling read.
#
# Sandboxed/static only — no root, no real systemd. Mirrors the style
# of tools/test-apparmor-gate.sh.
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
INSTALL_SH="$REPO_ROOT/install.sh"
FPM_EXEC="$REPO_ROOT/install/systemd/fpm-exec"
FPM_PRE="$REPO_ROOT/install/systemd/fpm-pre-start"

fail() { echo "FAIL: $*" >&2; exit 1; }
pass() { echo "PASS: $*"; }

[[ -f "$INSTALL_SH" ]] || fail "install.sh not found at $INSTALL_SH"
[[ -f "$FPM_EXEC"   ]] || fail "fpm-exec not found at $FPM_EXEC"
[[ -f "$FPM_PRE"    ]] || fail "fpm-pre-start not found at $FPM_PRE"

# ---- C1: /etc/jabali-panel is created 0755, never 0750 -------------
if grep -Eq 'install -d -m 0750 -o root -g "\$SERVICE_USER" /etc/jabali-panel( |$)' "$INSTALL_SH"; then
  fail "C1: install.sh still creates /etc/jabali-panel with -m 0750 (breaks non-jabali traversal)"
fi
grep -Eq 'install -d -m 0755 -o root -g "\$SERVICE_USER" /etc/jabali-panel( |$)' "$INSTALL_SH" \
  || fail "C1: install.sh has no 'install -d -m 0755 ... /etc/jabali-panel' creation line"
pass "C1: /etc/jabali-panel created 0755"

# ---- C2: provision_new_software heals the dir mode every update ----
grep -Eq '^ensure_jabali_panel_dir_traversable\(\)' "$INSTALL_SH" \
  || fail "C2: ensure_jabali_panel_dir_traversable() not defined"
# It must actually assert 0755 on the dir.
awk '/^ensure_jabali_panel_dir_traversable\(\)/,/^}/' "$INSTALL_SH" \
  | grep -Eq 'chmod 0755 /etc/jabali-panel( |$|")' \
  || fail "C2: ensure_jabali_panel_dir_traversable() does not chmod 0755 /etc/jabali-panel"
# And provision_new_software must call it.
awk '/^provision_new_software\(\)/,/^}/' "$INSTALL_SH" \
  | grep -Eq 'ensure_jabali_panel_dir_traversable' \
  || fail "C2: provision_new_software does not call ensure_jabali_panel_dir_traversable"
pass "C2: provision_new_software heals /etc/jabali-panel mode every update"

# ---- C3: fpm-exec never touches /etc/jabali-panel -----------------
if grep -q '/etc/jabali-panel' "$FPM_EXEC"; then
  fail "C3: fpm-exec still references /etc/jabali-panel (not decoupled from the 0750 parent)"
fi
pass "C3: fpm-exec does not reference /etc/jabali-panel"

# ---- C4: fpm-exec reads ver from the per-user runtime dir ----------
grep -Eq '/run/php/jabali-\$\{?user\}?/phpver' "$FPM_EXEC" \
  || fail "C4: fpm-exec does not read /run/php/jabali-<user>/phpver"
pass "C4: fpm-exec reads ver from /run/php/jabali-<user>/phpver"

# ---- C5: fpm-pre-start writes that runtime phpver file -------------
grep -Eq '/run/php/jabali-\$\{?user\}?/phpver' "$FPM_PRE" \
  || fail "C5: fpm-pre-start does not write /run/php/jabali-<user>/phpver"
pass "C5: fpm-pre-start writes /run/php/jabali-<user>/phpver"

# ---- Behavioral sandbox: prove the decoupled read works even when
#      /etc/jabali-panel is totally unreadable (0000) ----------------
SBX="$(mktemp -d)"
trap 'chmod -R u+rwx "$SBX" 2>/dev/null || true; rm -rf "$SBX"' EXIT
mkdir -p "$SBX/etc/jabali-panel/user-phpver" "$SBX/run/php/jabali-bob"
echo "8.5" > "$SBX/etc/jabali-panel/user-phpver/bob"
# Simulate fpm-pre-start (root): resolve ver from the protected file
# (root can read it) and publish to the user-runtime dir.
ver_root="$(cat "$SBX/etc/jabali-panel/user-phpver/bob")"
printf '%s' "$ver_root" > "$SBX/run/php/jabali-bob/phpver"
# Now make the whole /etc/jabali-panel tree unreadable, mimicking the
# 0750-vs-non-jabali-user regression in its worst form.
chmod 0000 "$SBX/etc/jabali-panel"
# Simulate fpm-exec (dropped to the hosting user): it must resolve the
# version WITHOUT reading anything under /etc/jabali-panel.
ver_exec="$(cat "$SBX/run/php/jabali-bob/phpver")"
[[ "$ver_exec" == "8.5" ]] \
  || fail "behavioral: decoupled fpm-exec read resolved '$ver_exec', expected '8.5'"
if cat "$SBX/etc/jabali-panel/user-phpver/bob" >/dev/null 2>&1; then
  fail "behavioral: sandbox setup wrong — /etc/jabali-panel still readable"
fi
pass "behavioral: fpm-exec resolves PHP version with /etc/jabali-panel at mode 0000"

echo "ALL PASS: fpm-exec is decoupled from /etc/jabali-panel traversal"
