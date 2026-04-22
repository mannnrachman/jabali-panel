#!/usr/bin/env bash
# install/tests/test_socket_helpers.sh — unit tests for the M25 socket helpers.
#
# Self-contained: sources install/scripts/socket-helpers.sh directly and
# exercises each function against controlled fixtures (a real Unix socket via
# `nc -lU`, an actual listener on a high port via `python3 -c`, etc.). No
# external test framework — plain bash + `if`/`exit`.
#
# Run from repo root:
#     bash install/tests/test_socket_helpers.sh
#
# Exit code 0 = all tests passed. Non-zero = at least one assertion failed;
# the failing test's name is on stderr.

set -euo pipefail

SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" >/dev/null && pwd)"
HELPERS="$SCRIPT_DIR/../scripts/socket-helpers.sh"

if [[ ! -f "$HELPERS" ]]; then
  printf '[FAIL] cannot find %s — run from repo root\n' "$HELPERS" >&2
  exit 1
fi

# Capture logger output into a per-test buffer so we can assert on the
# error message text. Defining these BEFORE sourcing the helpers means
# the helpers' `declare -F _log >/dev/null && skip` guard won't override.
TEST_OUTPUT=""
_log()  { TEST_OUTPUT+="[i] $*"$'\n'; }
_ok()   { TEST_OUTPUT+="[ok] $*"$'\n'; }
_warn() { TEST_OUTPUT+="[warn] $*"$'\n'; }
_err()  { TEST_OUTPUT+="[err] $*"$'\n'; }

# shellcheck source=../scripts/socket-helpers.sh
source "$HELPERS"

PASS_COUNT=0
FAIL_COUNT=0
FAILED_TESTS=()

assert_pass() {
  local name="$1"
  PASS_COUNT=$((PASS_COUNT + 1))
  printf '\033[1;32m[PASS]\033[0m %s\n' "$name"
}

assert_fail() {
  local name="$1" reason="$2"
  FAIL_COUNT=$((FAIL_COUNT + 1))
  FAILED_TESTS+=("$name")
  printf '\033[1;31m[FAIL]\033[0m %s — %s\n' "$name" "$reason" >&2
  if [[ -n "$TEST_OUTPUT" ]]; then
    printf '       buffered output:\n'
    printf '       %s\n' "${TEST_OUTPUT//$'\n'/$'\n'       }" >&2
  fi
}

# Each test is a function. Conventions:
#   - Reset TEST_OUTPUT="" at the start.
#   - Use `if function-call; then ... else ... fi` to capture exit codes
#     under set -e — direct invocation would abort the test runner.
#   - Use [[ "$TEST_OUTPUT" == *"substring"* ]] to assert log content.

# A scratch directory we clean up at the end. mktemp -d gives 0700 perms by
# default which makes test sockets uniquely owned by the test runner.
SCRATCH="$(mktemp -d /tmp/jabali-m25-test.XXXXXX)"
cleanup() { rm -rf "$SCRATCH"; }
trap cleanup EXIT

# create_socket_with_perms <path> <mode> — make a real Unix socket file with
# the given mode. Uses python3 because `nc -lU` doesn't accept a mode arg
# and we don't want to fork+chmod-with-race. python3 is a base-package
# install dep already (preflight ensures it).
create_socket_with_perms() {
  local path="$1" mode="$2"
  python3 -c "
import socket, os, sys
p = sys.argv[1]
m = int(sys.argv[2], 8)
old = os.umask(0)
try:
    s = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
    s.bind(p)
    os.chmod(p, m)
    s.close()
finally:
    os.umask(old)
" "$path" "$mode"
}

# ---------- verify_socket_perms tests -------------------------------------

test_verify_socket_perms_passes_on_match() {
  TEST_OUTPUT=""
  local sock="$SCRATCH/match.sock"
  create_socket_with_perms "$sock" "660"
  local me my_group
  me="$(id -un)"; my_group="$(id -gn)"
  if verify_socket_perms "$sock" "$me" "$my_group" "660"; then
    assert_pass "verify_socket_perms passes on exact match"
  else
    assert_fail "verify_socket_perms passes on exact match" "helper rejected a valid socket"
  fi
}

test_verify_socket_perms_accepts_leading_zero() {
  TEST_OUTPUT=""
  local sock="$SCRATCH/leadingzero.sock"
  create_socket_with_perms "$sock" "660"
  local me my_group
  me="$(id -un)"; my_group="$(id -gn)"
  if verify_socket_perms "$sock" "$me" "$my_group" "0660"; then
    assert_pass "verify_socket_perms accepts leading-zero mode (0660)"
  else
    assert_fail "verify_socket_perms accepts leading-zero mode (0660)" "rejected 0660 form"
  fi
}

test_verify_socket_perms_rejects_wrong_mode() {
  TEST_OUTPUT=""
  local sock="$SCRATCH/wrongmode.sock"
  create_socket_with_perms "$sock" "644"
  local me my_group
  me="$(id -un)"; my_group="$(id -gn)"
  if verify_socket_perms "$sock" "$me" "$my_group" "660" 2>/dev/null; then
    assert_fail "verify_socket_perms rejects wrong mode" "helper accepted mode 644 when expected 660"
  else
    if [[ "$TEST_OUTPUT" == *"mode is 644, expected 660"* ]]; then
      assert_pass "verify_socket_perms rejects wrong mode (with diagnostic)"
    else
      assert_fail "verify_socket_perms rejects wrong mode" "missing diagnostic; got: $TEST_OUTPUT"
    fi
  fi
}

test_verify_socket_perms_rejects_missing_file() {
  TEST_OUTPUT=""
  if verify_socket_perms "$SCRATCH/does-not-exist.sock" root root 660 2>/dev/null; then
    assert_fail "verify_socket_perms rejects missing file" "helper passed for non-existent path"
  else
    if [[ "$TEST_OUTPUT" == *"is not a socket"* ]]; then
      assert_pass "verify_socket_perms rejects missing file (with diagnostic)"
    else
      assert_fail "verify_socket_perms rejects missing file" "missing diagnostic; got: $TEST_OUTPUT"
    fi
  fi
}

test_verify_socket_perms_rejects_regular_file() {
  TEST_OUTPUT=""
  local f="$SCRATCH/regular-file"
  : >"$f"
  if verify_socket_perms "$f" root root 660 2>/dev/null; then
    assert_fail "verify_socket_perms rejects regular file" "helper passed for non-socket path"
  else
    assert_pass "verify_socket_perms rejects regular file"
  fi
}

# ---------- verify_no_all_interface_binds tests ---------------------------

# start_python_listener <bind_addr> <port>
# Backgrounds a short-lived python listener; sets the global LISTENER_PID
# so the test can kill it. We don't use $(...) because backgrounded
# processes in command-substitution subshells get SIGHUP'd on subshell
# exit on some bash configurations, killing the listener before the helper
# under test can observe it. Returns 0 on listener-up, 1 on timeout.
LISTENER_PID=""
start_python_listener() {
  local addr="$1" port="$2"
  python3 -c "
import socket, sys, time
addr, port = sys.argv[1], int(sys.argv[2])
s = socket.socket(socket.AF_INET6 if ':' in addr else socket.AF_INET, socket.SOCK_STREAM)
s.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
s.bind((addr, port))
s.listen(1)
time.sleep(5)
" "$addr" "$port" &
  LISTENER_PID=$!
  # Give the kernel a moment to materialize the listener so ss can see it.
  for _ in 1 2 3 4 5 6 7 8 9 10; do
    if ss -Hlnt "( sport = :$port )" 2>/dev/null | grep -q ":$port"; then
      return 0
    fi
    sleep 0.1
  done
  kill "$LISTENER_PID" 2>/dev/null || true
  LISTENER_PID=""
  return 1
}

stop_listener() {
  if [[ -n "$LISTENER_PID" ]]; then
    kill "$LISTENER_PID" 2>/dev/null || true
    wait "$LISTENER_PID" 2>/dev/null || true
    LISTENER_PID=""
  fi
}

test_verify_no_all_interface_binds_passes_on_no_listener() {
  TEST_OUTPUT=""
  # Pick an obscure high port unlikely to clash with anything else on this host.
  local port=58127
  if verify_no_all_interface_binds "$port"; then
    assert_pass "verify_no_all_interface_binds passes when nothing listens"
  else
    assert_fail "verify_no_all_interface_binds passes when nothing listens" "helper failed on empty port"
  fi
}

test_verify_no_all_interface_binds_passes_on_loopback_only() {
  TEST_OUTPUT=""
  local port=58128
  if ! start_python_listener 127.0.0.1 "$port"; then
    assert_fail "verify_no_all_interface_binds passes on loopback-only" "could not start fixture listener"
    return
  fi

  if verify_no_all_interface_binds "$port"; then
    assert_pass "verify_no_all_interface_binds passes when only 127.0.0.1 binds"
  else
    assert_fail "verify_no_all_interface_binds passes on loopback-only" "helper rejected a 127.0.0.1 bind"
  fi
  stop_listener
}

test_verify_no_all_interface_binds_rejects_wildcard_v4() {
  TEST_OUTPUT=""
  local port=58129
  if ! start_python_listener 0.0.0.0 "$port"; then
    assert_fail "verify_no_all_interface_binds rejects 0.0.0.0" "could not start fixture listener"
    return
  fi

  if verify_no_all_interface_binds "$port" 2>/dev/null; then
    assert_fail "verify_no_all_interface_binds rejects 0.0.0.0" "helper passed on a wildcard v4 bind"
  else
    if [[ "$TEST_OUTPUT" == *"wildcard bind on :$port"* ]]; then
      assert_pass "verify_no_all_interface_binds rejects 0.0.0.0 (with diagnostic)"
    else
      assert_fail "verify_no_all_interface_binds rejects 0.0.0.0" "missing diagnostic; got: $TEST_OUTPUT"
    fi
  fi
  stop_listener
}

test_verify_no_all_interface_binds_rejects_wildcard_v6() {
  TEST_OUTPUT=""
  local port=58130
  if ! start_python_listener "::" "$port"; then
    # IPv6 may be disabled on the test host — skip cleanly rather than fail.
    printf '\033[1;33m[SKIP]\033[0m verify_no_all_interface_binds rejects [::] — IPv6 unavailable\n'
    return
  fi

  if verify_no_all_interface_binds "$port" 2>/dev/null; then
    assert_fail "verify_no_all_interface_binds rejects [::]" "helper passed on a wildcard v6 bind"
  else
    if [[ "$TEST_OUTPUT" == *"wildcard bind on :$port"* ]]; then
      assert_pass "verify_no_all_interface_binds rejects [::] (with diagnostic)"
    else
      assert_fail "verify_no_all_interface_binds rejects [::]" "missing diagnostic; got: $TEST_OUTPUT"
    fi
  fi
  stop_listener
}

test_verify_no_all_interface_binds_rejects_empty_port() {
  TEST_OUTPUT=""
  if verify_no_all_interface_binds "" 2>/dev/null; then
    assert_fail "verify_no_all_interface_binds rejects empty port arg" "helper accepted empty port"
  else
    assert_pass "verify_no_all_interface_binds rejects empty port arg"
  fi
}

# ---------- runner --------------------------------------------------------

main() {
  printf '\033[1;34m[i]\033[0m Running M25 socket-helper tests against %s\n' "$HELPERS"

  test_verify_socket_perms_passes_on_match
  test_verify_socket_perms_accepts_leading_zero
  test_verify_socket_perms_rejects_wrong_mode
  test_verify_socket_perms_rejects_missing_file
  test_verify_socket_perms_rejects_regular_file

  test_verify_no_all_interface_binds_passes_on_no_listener
  test_verify_no_all_interface_binds_passes_on_loopback_only
  test_verify_no_all_interface_binds_rejects_wildcard_v4
  test_verify_no_all_interface_binds_rejects_wildcard_v6
  test_verify_no_all_interface_binds_rejects_empty_port

  printf '\n\033[1;34m[i]\033[0m Results: %d passed, %d failed\n' "$PASS_COUNT" "$FAIL_COUNT"
  if (( FAIL_COUNT > 0 )); then
    printf '\033[1;31m[FAIL]\033[0m failing tests:\n' >&2
    printf '  %s\n' "${FAILED_TESTS[@]}" >&2
    exit 1
  fi
  printf '\033[1;32m[OK]\033[0m all M25 socket-helper tests passed\n'
}

main "$@"
