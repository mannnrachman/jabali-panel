#!/usr/bin/env bash
# tools/lint-install-sh.sh — phantom-function detector for install.sh.
#
# Scans install.sh for every function-call-shaped token at the START
# of a logical command (line start modulo whitespace, OR after `&& `,
# `|| `, `; `, `do `, `then `, `else `) and verifies the token resolves
# to one of:
#   1. A function defined inside install.sh (matched by ^<name>\(\))
#   2. A function exported by a sourced shell helper
#      (install/scripts/*.sh referenced from install.sh).
#   3. A real binary on $PATH (deferred to runtime, NOT checked here).
#
# Why: install_snuffleupagus + verify_socket_perms shipped to main as
# phantom calls during routine refactors and the next `jabali update`
# crashed mid-deploy with "command not found". CI failure at PR time
# beats the same crash on a customer host.
#
# Filtering: only report identifiers matching the internal-helper
# naming conventions (install_*, verify_*, ensure_*, configure_*,
# setup_*, write_*, render_*, build_*, bootstrap_*, restore_*,
# reload_*, wait_for_*) so we don't false-positive on every binary
# the script invokes.
set -euo pipefail

SCRIPT="${1:-install.sh}"
if [[ ! -f "$SCRIPT" ]]; then
  echo "lint-install-sh: $SCRIPT not found" >&2
  exit 2
fi

REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"

# ---- 1. functions DEFINED in install.sh ------------------------------------
mapfile -t defined_in_main < <(
  grep -E '^[a-z_][a-zA-Z0-9_]*\(\)' "$SCRIPT" \
    | sed -E 's/^([a-z_][a-zA-Z0-9_]*)\(\).*/\1/'
)

# ---- 2. functions EXPORTED by sourced helper files ------------------------
mapfile -t helper_files < <(
  grep -oE 'install/scripts/[A-Za-z0-9_./-]+\.sh' "$SCRIPT" | sort -u
)
defined_in_helpers=()
for hf in "${helper_files[@]}"; do
  full="$REPO_ROOT/$hf"
  [[ -f "$full" ]] || continue
  while IFS= read -r fn; do
    defined_in_helpers+=("$fn")
  done < <(
    grep -E '^[a-z_][a-zA-Z0-9_]*\(\)' "$full" \
      | sed -E 's/^([a-z_][a-zA-Z0-9_]*)\(\).*/\1/'
  )
done

# Combined definition set.
declare -A KNOWN
for f in "${defined_in_main[@]}" "${defined_in_helpers[@]}"; do
  KNOWN["$f"]=1
done

# ---- 3. extract every CALL-shaped token from install.sh -------------------
# Must (a) appear at the start of a logical command, and (b) match the
# install.sh-internal naming convention. (a) prevents false positives
# from variable references like ${JABALI_PANEL_ADDR}; (b) prevents
# false positives from common binaries (apt-get, install, mkdir, ...).
INTERNAL_PREFIXES='install_|verify_|ensure_|configure_|setup_|write_|render_|build_|bootstrap_|restore_|reload_|wait_for_'

mapfile -t calls_in_script < <(
  awk '
    /^\s*#/ { next }                # skip pure-comment lines
    /^\s*[A-Z_][A-Z_0-9]*=/ { next } # skip env-var assignments
    {
      sub(/^[ \t]+/, "")
      sub(/^if[ \t]+!?[ \t]*/, "")
      sub(/^then[ \t]+/, "")
      sub(/^else[ \t]+/, "")
      sub(/^do[ \t]+/, "")
      if (match($0, /^[a-z_][a-zA-Z0-9_]*/)) {
        print substr($0, RSTART, RLENGTH)
      }
      n = split($0, parts, /[ \t]*(&&|\|\||;)[ \t]*/)
      for (i = 2; i <= n; i++) {
        if (match(parts[i], /^[a-z_][a-zA-Z0-9_]*/)) {
          print substr(parts[i], RSTART, RLENGTH)
        }
      }
    }
  ' "$SCRIPT"
)

# Also scan Go sources under panel-api/cmd/server/ for the
# `source install.sh && install_<fn>` pattern that update.go uses.
# Without this, a function deleted from install.sh but still called
# from update.go (the install_snuffleupagus + verify_socket_perms
# regression class) wouldn't be caught here — the call site is in
# Go, not bash.
mapfile -t calls_in_go < <(
  # update.go uses fmt.Sprintf-style concatenation:
  #   "source "+installSh+" && install_snuffleupagus"
  # so the install.sh path isn't a literal in the file. Match
  # the suffix `&& <fn>` in any Go string under cmd/server/.
  grep -hroE '&& [a-z_][a-zA-Z0-9_]+"' \
    "$REPO_ROOT/panel-api/cmd/server/" 2>/dev/null \
    | sed -E 's/.*&& //; s/".*//' \
    | sort -u
)

mapfile -t calls < <(
  printf '%s\n' "${calls_in_script[@]}" "${calls_in_go[@]}" \
    | grep -E "^($INTERNAL_PREFIXES)[a-zA-Z0-9_]+" \
    | sort -u
)

# ---- 4. report --------------------------------------------------------------
missing=()
for c in "${calls[@]}"; do
  if [[ -n "${KNOWN[$c]:-}" ]]; then
    continue
  fi
  missing+=("$c")
done

if [[ ${#missing[@]} -eq 0 ]]; then
  echo "lint-install-sh: OK — no phantom function calls in $SCRIPT"
  exit 0
fi

echo "lint-install-sh: PHANTOM FUNCTION CALLS in $SCRIPT" >&2
echo "These names look like install.sh-internal helpers but are NOT" >&2
echo "defined in $SCRIPT or any sourced install/scripts/*.sh file:" >&2
echo "" >&2
for m in "${missing[@]}"; do
  printf '  %-40s' "$m" >&2
  line=$(grep -nE '(^|[^A-Za-z0-9_])'"$m"'($|[^A-Za-z0-9_])' "$SCRIPT" | head -1 | cut -d: -f1)
  printf '  (first ref: %s:%s)\n' "$SCRIPT" "$line" >&2
done
echo "" >&2
echo "Either define the function in $SCRIPT, source a helper file" >&2
echo "that exports it, or rename the call site." >&2
exit 1
