#!/usr/bin/env bash
# install/scripts/socket-helpers.sh — M25 socket-related verification helpers.
#
# This file is sourced by install.sh after clone_or_update_repo, and by
# install/tests/test_socket_helpers.sh directly. The canonical definitions
# live here so the unit tests assert the exact code that ships in install.sh.
#
# Available helpers:
#   verify_socket_perms <path> <user> <group> <mode>
#       Asserts a Unix-domain socket exists with the expected owner / group /
#       mode. Fails with a clear _err line if any attribute is wrong, including
#       the actual values seen, so the operator doesn't have to re-run `ls -la`
#       to figure out what drifted.
#
#   verify_no_all_interface_binds <port>
#       Asserts no listener on <port> is bound to 0.0.0.0 or [::]. Pass-through
#       if there's no listener at all (the helper's job is to catch
#       all-interfaces leaks, not to assert presence). Used after every M25
#       service-bind change to guarantee we didn't accidentally re-introduce
#       a wildcard bind.
#
# Logger expectations: when sourced from install.sh the _log/_ok/_warn/_err
# functions are already defined. When sourced from tests we install fallback
# stubs so the helpers stay self-contained — the tests can pre-define their
# own loggers to capture output if they need to assert on it.

# ---------- M25 SOCKET PATTERN — RuntimeDirectory + ExecStartPost belt-and-braces
#
# Steps 2–5 of M25 each convert one service from a TCP listen to a Unix-domain
# socket. The socket file MUST end up owned by the service user and the
# `jabali-sockets` group with mode 0660 — nginx (running as www-data) needs
# group access to connect; if mode is 0644 or group ownership is wrong, nginx
# returns 502 and there's no obvious clue what broke.
#
# Two systemd primitives combine to lock this down (per M25 review F-C-3):
#
#   1. RuntimeDirectory=jabali-<name>      (creates /run/jabali-<name>/)
#      RuntimeDirectoryMode=0750            (dir mode)
#      RuntimeDirectoryPreserve=no          (deleted on stop)
#
#      systemd creates the directory before ExecStart with the unit's
#      User=/Group= as the owner. So far so good — the directory is correct.
#
#   2. The SOCKET FILE inside that directory is created by the service itself
#      at listen() time, using the service's umask (typically 0022, which
#      yields a 0644 socket). RuntimeDirectory= does NOT control the socket's
#      mode. Two ways to fix it:
#
#      a) Have the service set socket.mode + socket.group in its OWN config
#         (Kratos and panel-api both expose this). Preferred — atomic with
#         service start, no race window where nginx might connect to a
#         too-permissive socket.
#
#      b) Add `ExecStartPost=/bin/chmod 0660 /run/jabali-<name>/<name>.sock`
#         and `ExecStartPost=/bin/chgrp jabali-sockets ...`. Belt-and-braces.
#         Idempotent. Catches the case where the service config doesn't
#         expose the option (common in e.g. early Bulwark wrappers).
#
#      Steps 2–5 SHIP BOTH. The verify_socket_perms helper below is the
#      install-time assertion that the combined approach actually worked.
#      If the helper trips, the install fails loud rather than letting the
#      operator discover a 502 in production.
#
# Reversibility: each service's runbook entry lists the systemd drop-in that
# reverts the binding to TCP (sets `Environment=` overrides, removes
# RuntimeDirectory=). The drop-in pattern means rollback is a single file in
# /etc/systemd/system/<service>.service.d/ and a `daemon-reload + restart`.
# ----------------------------------------------------------------------------

# Logger fallback: tests source this file standalone and may not have the
# install.sh logger pre-loaded. Define no-color stubs only when the names
# aren't already defined so install.sh's full-color versions win when this is
# sourced from there. `declare -F <name> >/dev/null` returns 0 iff the
# function is already defined.
if ! declare -F _log >/dev/null;  then _log()  { printf '[i] %s\n' "$*"; };  fi
if ! declare -F _ok  >/dev/null;  then _ok()   { printf '[OK] %s\n' "$*"; }; fi
if ! declare -F _warn >/dev/null; then _warn() { printf '[!] %s\n' "$*" >&2; }; fi
if ! declare -F _err >/dev/null;  then _err()  { printf '[X] %s\n' "$*" >&2; }; fi

# verify_socket_perms <path> <expected_user> <expected_group> <expected_mode>
#
# Returns 0 on match, 1 on any drift. Prints the actual + expected values on
# mismatch so the failure log is self-explanatory.
#
# <expected_mode> is the octal mode WITHOUT a leading zero (e.g. "660", not
# "0660") — that's what `stat -c %a` returns. The helper accepts either form
# for ergonomics: a leading 0 is stripped before the comparison.
verify_socket_perms() {
  local path="$1"
  local expect_user="$2"
  local expect_group="$3"
  local expect_mode="${4#0}"  # strip optional leading 0

  if [[ ! -S "$path" ]]; then
    _err "verify_socket_perms: $path is not a socket (or doesn't exist)"
    return 1
  fi

  # %a = mode in octal, %U = owner name, %G = group name. Single stat call so
  # the three reads happen against the same inode (no TOCTOU between reads).
  local actual
  if ! actual="$(stat -c '%a %U %G' "$path" 2>/dev/null)"; then
    _err "verify_socket_perms: stat($path) failed"
    return 1
  fi

  local actual_mode actual_user actual_group
  read -r actual_mode actual_user actual_group <<<"$actual"

  if [[ "$actual_user" != "$expect_user" ]]; then
    _err "verify_socket_perms: $path owner is $actual_user, expected $expect_user"
    return 1
  fi
  if [[ "$actual_group" != "$expect_group" ]]; then
    _err "verify_socket_perms: $path group is $actual_group, expected $expect_group"
    return 1
  fi
  if [[ "$actual_mode" != "$expect_mode" ]]; then
    _err "verify_socket_perms: $path mode is $actual_mode, expected $expect_mode"
    return 1
  fi

  _ok "socket OK: $path ($actual_user:$actual_group $actual_mode)"
  return 0
}

# verify_no_all_interface_binds <port>
#
# Returns 0 if no listener on <port> is bound to a wildcard address
# (0.0.0.0 or [::]). Returns 1 if any wildcard bind is present, with the
# offending lines logged. A port with no listener at all is a pass — this
# helper catches leaks, not absence.
#
# Implementation note: ss -lntp output's address column is "address:port"
# for IPv4 and "[address]:port" for IPv6. The grep matches the explicit
# wildcard strings rather than parsing the column to avoid mis-matching
# "0.0.0.0" appearing in a process name. The literal `:<port>` suffix
# anchors against partial-port matches (e.g. port 8 vs 80).
verify_no_all_interface_binds() {
  local port="$1"
  if [[ -z "$port" ]]; then
    _err "verify_no_all_interface_binds: port argument is required"
    return 1
  fi

  # ss -H suppresses the header line so we don't have to skip it. -l listening,
  # -n numeric, -t tcp.
  local lines
  lines="$(ss -Hlnt "( sport = :$port )" 2>/dev/null || true)"

  if [[ -z "$lines" ]]; then
    _ok "no listener on :$port (nothing to verify)"
    return 0
  fi

  # Match the local-address column. Format: "LISTEN <recv-q> <send-q> <addr>:<port> ..."
  # Wildcard binds appear as "0.0.0.0:<port>" or "[::]:<port>" or "*:<port>"
  # (the last form is rare but ss emits it on some kernels). Use awk so we
  # only inspect column 4 — pid/comm columns can contain arbitrary characters.
  local bad
  bad="$(printf '%s\n' "$lines" | awk -v p=":$port" '
    {
      addr = $4
      if (addr ~ /^0\.0\.0\.0:/ || addr ~ /^\[::\]:/ || addr ~ /^\*:/) {
        print
      }
    }
  ')"

  if [[ -n "$bad" ]]; then
    _err "verify_no_all_interface_binds: wildcard bind on :$port detected:"
    printf '  %s\n' "$bad" >&2
    return 1
  fi

  _ok "no wildcard bind on :$port"
  return 0
}
