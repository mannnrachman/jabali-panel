#!/usr/bin/env bash
# TDD reproducer for the AppArmor broken-kernel gate durability bug.
#
# Bug: on a kernel without /sys/.../apparmor/features/unix, the gate
# unloaded jabali daemon profiles with `apparmor_parser -R` only. That
# is transient — the profile FILES remain in /etc/apparmor.d/, so the
# system apparmor.service re-parses + re-loads them on the next boot /
# reload / apt trigger, silently re-EACCESing every unix-socket connect
# (agent -> /run/mysqld/mysqld.sock => WordPress install dies at
# db.create). Observed live on mx.jabali-panel.com (Ubuntu 24.04 HWE
# kernel 6.8, features/unix absent): gate marker set, yet 4 jabali
# profiles loaded again in complain mode.
#
# Contract under test: apparmor_durably_disable_jabali <apparmor_d_dir>
# must, for every jabali daemon profile (incl. stalwart-mail and any
# stray *.test variant), leave a state the system apparmor.service
# will NOT re-load — i.e. an /etc/apparmor.d/disable/<name> symlink
# (apparmor's standard skip mechanism) AND no leftover *.test file.
#
# This test sandboxes the filesystem contract only (no root, no real
# apparmor_parser needed). Run: bash tools/test-apparmor-gate.sh

set -uo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
fail=0
note() { printf '%s\n' "$*"; }
check() { # check <desc> <cond-cmd...>
  local desc="$1"; shift
  if "$@"; then
    note "PASS: $desc"
  else
    note "FAIL: $desc"
    fail=1
  fi
}

# --- sandbox a fake /etc/apparmor.d ---
sandbox="$(mktemp -d)"
trap 'rm -rf "$sandbox"' EXIT
aad="$sandbox/apparmor.d"
mkdir -p "$aad/disable"
for n in jabali-agent jabali-bulwark jabali-panel-api jabali-kratos stalwart-mail; do
  printf 'profile %s {\n}\n' "$n" > "$aad/usr.local.bin.$n"
done
# Stray .test variant the live box had — must also be neutralised.
cp "$aad/usr.local.bin.jabali-agent" "$aad/usr.local.bin.jabali-agent.test"

# --- load the function under test from install.sh ---
# Extract just the function body so sourcing install.sh doesn't run
# the whole installer. The function must exist post-fix.
if ! grep -q '^apparmor_durably_disable_jabali()' "$REPO_ROOT/install.sh"; then
  note "FAIL: apparmor_durably_disable_jabali() not defined in install.sh (RED expected pre-fix)"
  exit 1
fi
# shellcheck disable=SC1090
eval "$(awk '/^apparmor_durably_disable_jabali\(\) \{/,/^\}/' "$REPO_ROOT/install.sh")"
# Stub apparmor_parser so the function's immediate-unload call is inert
# in the sandbox; the durable filesystem contract is what we assert.
apparmor_parser() { return 0; }
_log() { :; }; _warn() { :; }; _ok() { :; }

apparmor_durably_disable_jabali "$aad"

# --- assertions: every jabali profile durably skipped ---
for n in jabali-agent jabali-bulwark jabali-panel-api jabali-kratos stalwart-mail; do
  check "disable symlink exists for $n" test -L "$aad/disable/usr.local.bin.$n"
  if [[ -L "$aad/disable/usr.local.bin.$n" ]]; then
    tgt="$(readlink "$aad/disable/usr.local.bin.$n")"
    check "disable/$n points back at the profile" \
      bash -c "[[ \"$tgt\" == *usr.local.bin.$n ]]"
  fi
done
check "stray jabali-agent.test removed" \
  bash -c "[[ ! -e \"$aad/usr.local.bin.jabali-agent.test\" ]]"

if [[ $fail -eq 0 ]]; then
  note "ALL GREEN"
  exit 0
fi
note "RED"
exit 1
