#!/bin/bash
# Per-profile smoke runner driven by profile-corpus.yaml.
#
# Build the aa-smoke binary, then for every profile in the corpus
# launch `aa-exec -p <profile> -- aa-smoke <sockets...>`. Exit 0 only
# when every profile passes; first FAIL exits non-zero so the make
# target propagates.
#
# The corpus YAML uses a tiny subset of YAML (top-level `profiles:`
# array with `name:` + `sockets:` fields per entry) — parsed with
# inline awk so we don't pull in a yaml library for one tool.
set -euo pipefail

DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
BIN="$DIR/aa-smoke"

if ! command -v go >/dev/null 2>&1; then
  echo "FATAL: go not in PATH"
  exit 2
fi

if [[ ! -x "$BIN" ]] || [[ "$DIR/main.go" -nt "$BIN" ]]; then
  echo "Building aa-smoke..."
  ( cd "$DIR" && go build -o aa-smoke main.go )
fi

if ! command -v aa-exec >/dev/null 2>&1; then
  echo "FATAL: aa-exec not in PATH (apt install apparmor-utils)"
  exit 2
fi

if [[ ! -f "$DIR/profile-corpus.yaml" ]]; then
  echo "FATAL: $DIR/profile-corpus.yaml missing"
  exit 2
fi

# Awk parses the YAML one entry at a time; emits "<profile>\t<sock>" rows.
ROWS=$(awk '
  /^[ \t]*-[ \t]+name:[ \t]*/ { sub(/^[ \t]*-[ \t]+name:[ \t]*/, ""); profile=$0; next }
  /^[ \t]*sockets:/ { in_sockets=1; next }
  in_sockets && /^[ \t]*-[ \t]+/ {
    sub(/^[ \t]*-[ \t]+/, "")
    printf "%s\t%s\n", profile, $0
    next
  }
  /^[ \t]*[a-zA-Z_]+:/ && !/^[ \t]*sockets:/ { in_sockets=0 }
' "$DIR/profile-corpus.yaml")

if [[ -z "$ROWS" ]]; then
  echo "FATAL: no profile rows parsed from corpus"
  exit 2
fi

FAIL=0
LAST_PROFILE=""
SOCKETS=()

flush() {
  if [[ -z "$LAST_PROFILE" ]] || [[ ${#SOCKETS[@]} -eq 0 ]]; then
    return
  fi
  echo
  echo "=== aa-exec -p $LAST_PROFILE — ${#SOCKETS[@]} sockets ==="
  if ! aa-exec -p "$LAST_PROFILE" -- "$BIN" "${SOCKETS[@]}"; then
    echo "FAIL: profile $LAST_PROFILE blocks at least one required socket"
    FAIL=1
  fi
}

while IFS=$'\t' read -r profile socket; do
  if [[ "$profile" != "$LAST_PROFILE" ]]; then
    flush
    LAST_PROFILE="$profile"
    SOCKETS=()
  fi
  SOCKETS+=("$socket")
done <<< "$ROWS"
flush

if [[ "$FAIL" -ne 0 ]]; then
  echo
  echo "aa-smoke: at least one profile failed. See output above."
  exit 1
fi
echo
echo "aa-smoke: all profiles pass."
