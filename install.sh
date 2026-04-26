#!/usr/bin/env bash
#
# Jabali Panel installer — Phase 1 scope.
#
# What it does on a fresh Debian 13 or Ubuntu 24.04 root shell:
#   1. Installs base OS packages (git, curl, ca-certificates, build-essential).
#   2. Installs Go 1.25.1 into /usr/local/go (idempotent).
#   3. Creates a `jabali` system user (no login) + /opt/jabali-panel state dir.
#   4. Clones (or pulls) https://git.linux-hosting.co.il/shukivaknin/jabali2
#      into /opt/jabali-panel. If the repo is private, pass a Gitea token via
#      JABALI_GITEA_TOKEN env var or the first positional arg.
#   5. Builds panel-api and installs the binary at /usr/local/bin/jabali-panel.
#   6. Writes + starts the `jabali-panel.service` systemd unit bound to
#      127.0.0.1:8443 (configurable via PANEL_ADDR in /etc/jabali/panel.env).
#   7. Smoke-tests GET /health.
#
# Later phases (2+) will extend this file to provision MariaDB, build the
# React SPA, install nginx, wire SSL, etc. For now this is deliberately
# scoped to what Phase 1 actually ships.
#
# Usage (public repo):
#   curl -fsSL https://git.linux-hosting.co.il/shukivaknin/jabali2/raw/branch/main/install.sh | bash
#
# Flags (all optional, can be combined):
#   --hostname <fqdn>  Server hostname; skips the TTY prompt. Equivalent
#                      to setting JABALI_HOSTNAME. --hostname=<fqdn> also works.
#   --token <gitea>    Private-repo access token. Equivalent to
#                      setting JABALI_GITEA_TOKEN.
#   --debug            Verbose mode: disable the _spin progress spinner
#                      (stream every wrapped command's stdout+stderr live
#                      so you can see exactly where apt / systemctl / curl
#                      is stalled), and enable `set -x` with a line-tagged
#                      PS4 so every shell command is traced. Equivalent to
#                      setting JABALI_DEBUG=1. Use when install.sh appears
#                      to hang — the last xtrace line names the stalled cmd.
#
# Examples:
#   curl -fsSL <...>/install.sh | bash -s -- --hostname=panel.example.com
#   curl -fsSL <...>/install.sh | bash -s -- --hostname panel.example.com --token <GITEA_TOKEN>
#
# Legacy: `bash -s -- <GITEA_TOKEN>` (positional token) still works.

# ---------- locale: pin to C.UTF-8 (MUST run before anything else) --------
# Operators SSH in with their own LANG (often a locale not yet generated on
# the target host — e.g. LANG=he_IL.UTF-8). Perl-using apt postinst scripts
# then spam "Setting locale failed" warnings and fall back to "C", which is
# fine behaviourally but noisy. C.UTF-8 is always available on glibc (no
# locale-gen needed) and gives UTF-8 I/O. Unset every LC_* variant so perl
# doesn't retry the un-generated locale chain. Run this BEFORE `set -e` so
# a hostile env var (e.g. LC_ALL unset to empty) can't trip the script on
# its first line.
unset LANGUAGE LC_CTYPE LC_NUMERIC LC_COLLATE LC_TIME LC_MESSAGES LC_MONETARY LC_ADDRESS LC_IDENTIFICATION LC_MEASUREMENT LC_PAPER LC_TELEPHONE LC_NAME
export LANG=C.UTF-8
export LC_ALL=C.UTF-8
# Keep apt from prompting for debconf mid-run.
export DEBIAN_FRONTEND=noninteractive

set -euo pipefail

# ---------- fail-loud: ERR trap -------------------------------------------
# set -e exits on the first non-zero command. The default behavior prints
# nothing — whatever step failed looks identical to a clean exit, and the
# operator sees only the previous step's success log. This trap prints the
# line number + failing command + exit code on any non-zero exit in the
# script, including sub-shells. Don't use _err() yet (logger is defined
# further down and bash loads top-to-bottom); printf inline so the trap
# works regardless of which section triggers it.
trap '__rc=$?; printf "\033[1;31m[jabali-install]\033[0m install.sh exited with code %d at line %d: %s\n" "$__rc" "$LINENO" "$BASH_COMMAND" >&2' ERR

# ---------- config (override via env) ---------------------------------------

REPO_URL="${JABALI_REPO_URL:-https://git.linux-hosting.co.il/shukivaknin/jabali2.git}"
REPO_BRANCH="${JABALI_REPO_BRANCH:-main}"
REPO_DIR="${JABALI_REPO_DIR:-/opt/jabali-panel}"
GO_VERSION="${JABALI_GO_VERSION:-1.25.1}"
GO_ROOT="${JABALI_GO_ROOT:-/usr/local/go}"
SERVICE_USER="${JABALI_SERVICE_USER:-jabali}"
SERVICE_NAME="${JABALI_SERVICE_NAME:-jabali-panel}"
# Default binds all interfaces. This is intentional: during development and
# testing we want the panel reachable over the LAN without needing nginx.
# In production, flip this to 127.0.0.1:8443 and put nginx in front so TLS
# termination and rate limiting happen at the proxy (blueprint §5.1).
# M25 Step 4: default bind is the Unix socket. nginx terminates TLS on
# :8443 and proxies to /run/jabali-panel/api.sock (see
# install/nginx/jabali-panel-vhost.conf.tmpl + ADR-0050). Pre-M25 the
# default was 0.0.0.0:8443 — leaving that default here meant fresh
# installs seeded config.toml + the env file with a TCP bind, and the
# Step 4 in-place migration had to sweep it back out. Keeping the
# override so operators who really need TCP (debug, split-host) can
# still set JABALI_PANEL_ADDR=127.0.0.1:8443 explicitly.
PANEL_ADDR="${JABALI_PANEL_ADDR:-unix:/run/jabali-panel/api.sock}"
BIN_PATH="/usr/local/bin/jabali-panel"
AGENT_BIN_PATH="/usr/local/bin/jabali-agent"
AGENT_SOCKET="/run/jabali/agent.sock"
AGENT_SERVICE_NAME="jabali-agent"
ENV_FILE="/etc/jabali/panel.env"

# ---------- CLI flag parsing ------------------------------------------------
#
# We support --hostname and --token as named flags, and keep the legacy
# positional arg ($1 = gitea token) working by deferring it until after flag
# parsing. This way `bash -s -- --hostname=foo` and the old
# `bash -s -- <TOKEN>` both do the right thing.

_cli_hostname=""
_cli_token=""
_cli_debug=""
_cli_uninstall=""
_cli_yes=""
_positional=()
while [[ $# -gt 0 ]]; do
  case "$1" in
    --hostname=*) _cli_hostname="${1#*=}"; shift ;;
    --hostname)   _cli_hostname="${2:-}"; shift 2 ;;
    --token=*)    _cli_token="${1#*=}"; shift ;;
    --token)      _cli_token="${2:-}"; shift 2 ;;
    --debug)      _cli_debug=1; shift ;;
    --uninstall)  _cli_uninstall=1; shift ;;
    --yes|-y)     _cli_yes=1; shift ;;
    --)           shift; while [[ $# -gt 0 ]]; do _positional+=("$1"); shift; done ;;
    --*)          printf 'install.sh: unknown flag: %s\n' "$1" >&2; exit 64 ;;
    *)            _positional+=("$1"); shift ;;
  esac
done

# --hostname CLI arg wins over JABALI_HOSTNAME env; re-export so downstream
# functions (notably prompt_server_settings) pick it up via the same env var.
if [[ -n "$_cli_hostname" ]]; then
  JABALI_HOSTNAME="$_cli_hostname"
  export JABALI_HOSTNAME
fi

# --token precedence: CLI flag > JABALI_GITEA_TOKEN env > legacy positional.
GITEA_TOKEN="${_cli_token:-${JABALI_GITEA_TOKEN:-${_positional[0]:-}}}"

# --debug: CLI flag > JABALI_DEBUG env. Exported so any sub-shell scripts
# this installer invokes can honour it too. When set, _spin below skips
# the spinner + log-capture and streams stdout+stderr live, AND we enable
# `set -x` with a PS4 that tags every trace line with the source file +
# line number + function so the last trace line before a stall names
# exactly what command is waiting.
JABALI_DEBUG="${_cli_debug:-${JABALI_DEBUG:-}}"
export JABALI_DEBUG

# Per-run log file — always on, independent of --debug. Captures every
# _log/_ok/_warn/_err/_die line AND every _spin wrapped command's output,
# so a post-mortem after install stalls or errors doesn't depend on
# scrollback. Filename is timestamped so reruns don't clobber. Lives in
# /root because install.sh already requires root (preflight() asserts
# EUID==0); if touch fails (fallback for weird jails / testing) we try
# /tmp and emit a warning later. Mode 0600 — the log may echo hostnames,
# IPs, and package lists but never secrets (we redact/avoid passwords).
LOG_FILE="/root/jabali_install-$(date +%Y-%m-%d_%H-%M-%S).log"
if ! touch "$LOG_FILE" 2>/dev/null; then
  LOG_FILE="/tmp/jabali_install-$(date +%Y-%m-%d_%H-%M-%S).log"
  touch "$LOG_FILE" 2>/dev/null || LOG_FILE=""
fi
if [[ -n "$LOG_FILE" ]]; then
  chmod 0600 "$LOG_FILE" 2>/dev/null || true
fi

if [[ -n "$JABALI_DEBUG" ]]; then
  # Dim + line-tagged so xtrace output is visually distinct from the
  # _log/_ok/etc. column. ${FUNCNAME[0]:-main} names the caller function;
  # 'main' when xtrace fires at top level.
  #
  # Script name is hardcoded "install.sh" rather than
  # ${BASH_SOURCE##*/} because under `curl | bash`, BASH_SOURCE is
  # unset (bash reads the script from stdin, no filename), and pattern-
  # substitution on an unset array under `set -u` errors mid-xtrace
  # with "BASH_SOURCE: unbound variable" on the NEXT statement after
  # `set -x`. The `${VAR:-default}` trick doesn't help here because
  # `##*/` and `:-` can't compose in one expansion.
  export PS4='\033[2m+ install.sh:${LINENO}:${FUNCNAME[0]:-main}() \033[0m'
  # When a log file is open, tee xtrace to both terminal-stderr AND the
  # file so hangs are diagnosable post-mortem. BASH_XTRACEFD accepts a
  # single fd; process-sub gives us both destinations via tee.
  if [[ -n "$LOG_FILE" ]]; then
    exec {_XTRACE_FD}> >(tee -a "$LOG_FILE" >&2)
    export BASH_XTRACEFD=$_XTRACE_FD
  fi
  set -x
fi

# ---------- tiny logger -----------------------------------------------------

# _log_to_file mirrors every logger line into $LOG_FILE with an ISO
# timestamp prefix — so a grep through the log after an install can
# reconstruct the timing. No-op when LOG_FILE is empty (touch failed
# during early bootstrap on a non-root/weird jail). Uses plain echo
# redirection rather than `tee` to avoid spawning per-line.
_log_to_file() {
  [[ -n "${LOG_FILE:-}" ]] || return 0
  printf '%s %s\n' "$(date -Iseconds)" "$*" >> "$LOG_FILE" 2>/dev/null || true
}
_log()  { printf '\033[1;34m[i]\033[0m %s\n' "$*"; _log_to_file "[i] $*"; }
_ok()   { printf '\033[1;32m[✓]\033[0m %s\n' "$*"; _log_to_file "[✓] $*"; }
_warn() { printf '\033[1;33m[!]\033[0m %s\n' "$*" >&2; _log_to_file "[!] $*"; }
# _err prints in red on stderr — callers still control exit behavior.
# M18's configure_disk_quota relied on this silently; define it once
# so any future caller has a matching pair to _warn.
_err()  { printf '\033[1;31m[✗]\033[0m %s\n' "$*" >&2; _log_to_file "[✗] $*"; }
_die()  { printf '\033[1;31m[✗]\033[0m %s\n' "$*" >&2; _log_to_file "[✗] $*"; exit 1; }

# Announce where logs are going so the operator can tail -f in another
# shell if the install stalls. Printed via _log so it's itself captured.
if [[ -n "${LOG_FILE:-}" ]]; then
  _log "install log: $LOG_FILE (includes every step + wrapped command output)"
else
  _warn "could not open install log file — post-mortem only via scrollback"
fi

# _flush_spin_log appends a wrapped command's captured output into the
# main $LOG_FILE with a header so the post-mortem log reads top-to-bottom
# as a sequence of {step, output} blocks. No-op when LOG_FILE is empty
# or when the captured log has nothing to show.
_flush_spin_log() {
  local label="$1" log="$2"
  [[ -n "${LOG_FILE:-}" && -s "$log" ]] || return 0
  {
    printf '\n### %s ###\n' "$label"
    cat "$log"
  } >> "$LOG_FILE" 2>/dev/null || true
}

# _spin runs the given command with stdout+stderr captured to a temp log
# and a live spinner + elapsed counter on the terminal. On success, the
# captured output is flushed into $LOG_FILE for post-mortem diagnostics
# and an _ok line prints. On failure, the last 60 captured lines dump to
# stderr too so the operator sees them in scrollback, then the original
# exit code propagates. Usage: _spin "label" cmd args…
#
# Non-TTY stdout (CI, tee'd logs) falls back to a simple start/end pair
# with no spinner so the scrollback stays readable.
_spin() {
  local label="$1"; shift
  local log; log="$(mktemp /tmp/jabali-spin.XXXXXX.log)"

  # --debug / JABALI_DEBUG: skip the spinner, stream child output live so
  # hangs surface immediately (the default path swallows stdout+stderr
  # into $log and only reveals them on failure — perfect for clean
  # installs, useless when apt/systemctl/curl is stalled mid-run and
  # you want to watch the last line the stuck child printed). `tee -a`
  # mirrors the live stream into $LOG_FILE so the post-mortem still has
  # everything even though no separate capture happens.
  #
  # rc capture: `cmd | tee || true` would resolve `true` LAST and reset
  # PIPESTATUS to (0) — masking apt failures. Capture inside the `||`
  # clause where PIPESTATUS still reflects the failing pipeline, BEFORE
  # any subsequent simple command can rewrite it.
  if [[ -n "${JABALI_DEBUG:-}" ]]; then
    _log "$label…"
    local rc=0
    if [[ -n "${LOG_FILE:-}" ]]; then
      "$@" 2>&1 | tee -a "$LOG_FILE" || rc="${PIPESTATUS[0]}"
    else
      "$@" || rc=$?
    fi
    if [[ $rc -ne 0 ]]; then
      _err "$label FAILED (exit $rc)"
      rm -f "$log"
      exit "$rc"
    fi
    _ok "$label"
    rm -f "$log"
    return 0
  fi

  if [[ ! -t 1 ]]; then
    _log "$label…"
    if ! "$@" >"$log" 2>&1; then
      local rc=$?
      _err "$label FAILED (exit $rc) — tail of log:"
      tail -n 60 "$log" >&2
      _flush_spin_log "$label" "$log"
      rm -f "$log"
      exit "$rc"
    fi
    _flush_spin_log "$label" "$log"
    _ok "$label"
    rm -f "$log"
    return 0
  fi

  # Braille spinner — each frame is two glyphs wide. Array form is
  # required: bash's ${var:i:1} does BYTE slicing, which shreds
  # multi-byte UTF-8. Frames chosen for a smooth left-to-right sweep.
  local -a spinners=('⢎ ' '⠎⠁' '⠊⠑' '⠈⠱' ' ⡱' '⢀⡰' '⢄⡠' '⢆⡀')
  local n=${#spinners[@]}
  local i=0
  local start; start=$(date +%s)

  # Paint the first frame BEFORE forking the command. Sub-100ms commands
  # (warm apt cache, already-installed packages) would otherwise exit
  # before the loop's first `kill -0` check and the user would see only
  # the final [✓] line with no spinner at all. This guarantees at least
  # one spinner frame prints for every _spin call.
  #
  # Bracketed spinner mirrors the [✓]/[i]/[!]/[✗] column the logger uses
  # — when the process finishes, _ok overwrites the same column with
  # [✓], so the eye tracks the status glyph in one fixed place.
  printf '\033[1;36m[%s]\033[0m %s (0s)' "${spinners[i++ % n]}" "$label"

  "$@" >"$log" 2>&1 &
  local pid=$!
  while kill -0 "$pid" 2>/dev/null; do
    sleep 0.1
    local elapsed=$(( $(date +%s) - start ))
    printf '\r\033[K\033[1;36m[%s]\033[0m %s (%ds)' \
      "${spinners[i++ % n]}" "$label" "$elapsed"
  done
  wait "$pid"; local rc=$?
  printf '\r\033[K'

  if [[ $rc -ne 0 ]]; then
    _err "$label FAILED (exit $rc) — tail of log:"
    tail -n 60 "$log" >&2
    _flush_spin_log "$label" "$log"
    rm -f "$log"
    exit "$rc"
  fi
  _flush_spin_log "$label" "$log"
  _ok "$label"
  rm -f "$log"
}

# ---------- banner ----------------------------------------------------------
# Prints the jabali ASCII art at install start. Uses ANSI colour (yellow)
# for visibility without being garish. Unicode block characters require
# a UTF-8 terminal — every modern ssh/console has this by default.
print_banner() {
  printf '\033[1;33m'
  cat <<'BANNER'
      ▀██▀         ▀██              ▀██   ██
       ██   ▄▄▄▄    ██ ▄▄▄   ▄▄▄▄    ██  ▄▄▄
       ██  ▀▀ ▄██   ██▀  ██ ▀▀ ▄██   ██   ██
       ██  ▄█▀ ██   ██    █ ▄█▀ ██   ██   ██
   ██ ▄█▀  ▀█▄▄▀█▀  ▀█▄▄▄▀  ▀█▄▄▀█▀ ▄██▄ ▄██▄
    ▀▀▀
      J A B A L I   P A N E L   ·   v0.2.10
         Linux Web Hosting Control Panel
BANNER
  printf '\033[0m\n'
}

# ---------- preflight -------------------------------------------------------

preflight() {
  _log "preflight checks"

  [[ $EUID -eq 0 ]] || _die "must run as root (sudo bash install.sh)"

  if [[ -f /etc/os-release ]]; then
    # shellcheck disable=SC1091
    . /etc/os-release
    case "${ID:-}" in
      debian|ubuntu) _ok "OS: $PRETTY_NAME" ;;
      *) _warn "untested OS: ${PRETTY_NAME:-unknown}. Continuing anyway." ;;
    esac
  else
    _warn "no /etc/os-release; continuing blind"
  fi

  local arch
  arch="$(uname -m)"
  case "$arch" in
    x86_64)  GO_ARCH="amd64" ;;
    aarch64) GO_ARCH="arm64" ;;
    *)       _die "unsupported arch: $arch" ;;
  esac
  export GO_ARCH
}

# ---------- step 0.5: server identity prompts -------------------------------
#
# Capture hostname, public IPs, and nameserver names before any install
# step runs. Values are exported so write_config_file can seed config.toml
# and the app can read them on first boot. Idempotent: if the existing
# config.toml already contains [server].hostname, the prompts are skipped
# so `install.sh` is safe to re-run for updates.

# Read the primary interface IPs straight from the kernel. We pick the
# interface that owns the default route and take its first global-scope
# address. This matches what the panel will serve customers with and
# behaves sensibly behind NAT (returns the LAN IP; operators correct
# via the admin Server Settings page if the server actually sits behind
# 1:1 NAT with a different public IP).
_detect_main_iface() {
  ip route show default 2>/dev/null | awk '/^default/ {print $5; exit}'
}

_detect_public_ipv4() {
  local iface
  iface="$(_detect_main_iface)"
  if [[ -n "$iface" ]]; then
    ip -4 -o addr show dev "$iface" scope global 2>/dev/null \
      | awk '{print $4}' | cut -d/ -f1 | head -n1
    return 0
  fi
  # No default route — take any global IPv4.
  ip -4 -o addr show scope global 2>/dev/null | awk '{print $4}' | cut -d/ -f1 | head -n1
}

_detect_public_ipv6() {
  local iface
  iface="$(_detect_main_iface)"
  if [[ -n "$iface" ]]; then
    # -preferred drops deprecated/tentative addresses so we never pick a
    # stale SLAAC temp that's about to expire.
    ip -6 -o addr show dev "$iface" scope global -preferred 2>/dev/null \
      | awk '{print $4}' | cut -d/ -f1 | head -n1
    return 0
  fi
  ip -6 -o addr show scope global 2>/dev/null | awk '{print $4}' | cut -d/ -f1 | head -n1
}

prompt_server_settings() {
  local config_file="/etc/jabali-panel/config.toml"

  # If --hostname (or JABALI_HOSTNAME env) was passed, apply it at the OS
  # layer immediately — even on re-runs where config.toml already has a
  # hostname and we skip the prompt below. Without this, a second install
  # pass with --hostname=new-name silently ignored the flag because the
  # early-return fired before line 306's hostnamectl call.
  if [[ -n "${JABALI_HOSTNAME:-}" ]]; then
    local _hostname_regex='^[a-zA-Z0-9][a-zA-Z0-9.-]*[a-zA-Z0-9]$'
    if [[ ! "$JABALI_HOSTNAME" =~ $_hostname_regex ]]; then
      _die "invalid JABALI_HOSTNAME: '$JABALI_HOSTNAME' (use letters/digits/dots/hyphens)"
    fi
    local _cur_hostname
    _cur_hostname="$(hostname 2>/dev/null || echo '')"
    if [[ "$_cur_hostname" != "$JABALI_HOSTNAME" ]]; then
      _log "applying --hostname: $_cur_hostname → $JABALI_HOSTNAME"
      hostnamectl set-hostname "$JABALI_HOSTNAME" 2>/dev/null || \
        _warn "hostnamectl set-hostname failed (container without CAP_SYS_ADMIN?) — /etc/hostname may be stale"
    fi
    if ! grep -q "[[:space:]]${JABALI_HOSTNAME}\([[:space:]]\|$\)" /etc/hosts 2>/dev/null; then
      # Best-effort loopback entry so `hostname -f` resolves locally.
      printf '127.0.1.1\t%s\n' "$JABALI_HOSTNAME" >> /etc/hosts
    fi
  fi

  if [[ -f "$config_file" ]] && grep -q '^[[:space:]]*hostname[[:space:]]*=' "$config_file"; then
    _log "server settings already configured in $config_file — skipping prompt"
    # Re-export for downstream use so write_config_file is a no-op on re-run.
    JABALI_SERVER_CONFIGURED=1
    export JABALI_SERVER_CONFIGURED
    return 0
  fi

  local sys_hostname detected_ipv4 detected_ipv6
  sys_hostname="$(hostname -f 2>/dev/null || hostname 2>/dev/null || echo '')"

  _log "detecting primary interface IPv4…"
  detected_ipv4="$(_detect_public_ipv4 || true)"
  if [[ -z "$detected_ipv4" ]]; then
    _die "could not auto-detect an IPv4 address. Set JABALI_PUBLIC_IPV4 and re-run."
  fi
  _ok "primary IPv4: $detected_ipv4"

  _log "detecting primary interface IPv6 (optional)…"
  detected_ipv6="$(_detect_public_ipv6 || true)"
  if [[ -n "$detected_ipv6" ]]; then
    _ok "primary IPv6: $detected_ipv6"
  else
    _log "no IPv6 detected — skipping (zones won't get AAAA records)"
  fi

  # `curl | bash` consumes stdin for the script itself, so `read` would
  # hit EOF instantly. Fix: read from /dev/tty (the controlling terminal)
  # if one exists. If it doesn't — CI / cloud-init / no TTY at all —
  # fall back to env-var overrides / auto-detected defaults.
  #
  # Note: `[[ -r /dev/tty ]]` lies on non-interactive SSH (the device
  # node exists and looks readable to the test, but `exec 3</dev/tty`
  # fails with "No such device or address" because the session has no
  # controlling terminal). So we don't pre-test — we try the exec
  # directly inside an `if`, which neutralises errexit and lets us
  # fall through to the stdin-TTY branch on failure.
  local input_fd
  if exec 3</dev/tty 2>/dev/null; then
    input_fd=3
  elif [[ -t 0 ]]; then
    input_fd=0
  else
    input_fd=""
  fi

  local inp_hostname inp_ipv4 inp_ipv6 inp_ns1_name inp_ns1_ip inp_ns2_name inp_ns2_ip

  # IPs always come from detection / env override — never prompted.
  inp_ipv4="${JABALI_PUBLIC_IPV4:-$detected_ipv4}"
  inp_ipv6="${JABALI_PUBLIC_IPV6:-$detected_ipv6}"

  # If the hostname was pre-supplied (via --hostname flag or JABALI_HOSTNAME
  # env), skip the prompt entirely — even when a TTY is available. This
  # enables non-interactive provisioning (Ansible, CI images, etc.).
  local _hostname_regex='^[a-zA-Z0-9][a-zA-Z0-9.-]*[a-zA-Z0-9]$'
  if [[ -n "${JABALI_HOSTNAME:-}" ]]; then
    if [[ ! "$JABALI_HOSTNAME" =~ $_hostname_regex ]]; then
      _die "invalid JABALI_HOSTNAME: '$JABALI_HOSTNAME' (use letters/digits/dots/hyphens)"
    fi
    inp_hostname="$JABALI_HOSTNAME"
    _ok "using hostname from flag/env: $inp_hostname"
    # Close the TTY FD even though we didn't read from it.
    [[ "$input_fd" == "3" ]] && exec 3<&-
  elif [[ -z "$input_fd" ]]; then
    _warn "no TTY available — using auto-detected defaults + env vars."
    _warn "override hostname via --hostname flag or JABALI_HOSTNAME env"
    inp_hostname="$sys_hostname"
    if [[ ! "$inp_hostname" =~ $_hostname_regex ]]; then
      _die "no TTY and no --hostname given (detected: '$inp_hostname')"
    fi
  else
    # Structured preamble so the operator knows exactly what this
    # hostname controls before typing it. Printed to /dev/tty along
    # with the prompt itself so bash's stderr buffering (an issue
    # under `curl | bash`) can't swallow any of it. Falls back to
    # stdout if /dev/tty is unavailable (shouldn't happen since we
    # already proved we have a TTY via exec 3</dev/tty above, but
    # the guard is cheap).
    {
      printf '\n'
      printf 'Enter the fully qualified domain name (FQDN) for this server.\n'
      printf 'This name will be used for:\n'
      printf '  - System hostname (hostnamectl set-hostname)\n'
      printf '  - Panel URL (https://<hostname>:8443)\n'
      printf '  - Mail server config (stalwart + per-domain vhosts)\n'
      printf '  - Nameserver records (ns1.<hostname>, ns2.<hostname>)\n'
      printf '\n'
      printf 'Current hostname: \033[1m%s\033[0m\n' "$sys_hostname"
      printf 'Server IPv4:      \033[1m%s\033[0m\n' "$inp_ipv4"
      if [[ -n "$inp_ipv6" ]]; then
        printf 'Server IPv6:      \033[1m%s\033[0m\n' "$inp_ipv6"
      fi
      printf '\n'
    } > /dev/tty 2>/dev/null || {
      printf '\n'
      printf 'Current hostname: %s\n' "$sys_hostname"
      printf 'Server IPv4:      %s\n' "$inp_ipv4"
      [[ -n "$inp_ipv6" ]] && printf 'Server IPv6:      %s\n' "$inp_ipv6"
      printf '\n'
    }

    while true; do
      # Write the prompt directly to /dev/tty, bypassing stdout/stderr
      # entirely. `read -p` and `printf >&2` both failed to render this
      # line under `curl | bash` on Debian 13 — likely bash's own
      # block-buffering of stderr when the parent pipe (curl) is still
      # live. Writing to /dev/tty hits the same device the user is
      # looking at with zero intermediaries.
      printf "Enter hostname [%s]: " "$sys_hostname" > /dev/tty 2>/dev/null \
        || printf "Enter hostname [%s]: " "$sys_hostname"
      read -r -u "$input_fd" inp_hostname || true
      inp_hostname="${inp_hostname:-$sys_hostname}"
      [[ "$inp_hostname" =~ $_hostname_regex ]] && break
      _warn "invalid hostname; use letters/digits/dots/hyphens"
    done

    # Close the TTY FD so we don't leak it to child processes.
    [[ "$input_fd" == "3" ]] && exec 3<&-
  fi

  # NS names + IPs are auto-derived from the hostname — no prompt.
  # Both nameservers get the same IPv4 at install time; the operator
  # later points ns2 at a separate server via the admin Server
  # Settings page, which triggers a zone re-push automatically.
  inp_ns1_name="ns1.${inp_hostname}"
  inp_ns1_ip="${inp_ipv4}"
  inp_ns2_name="ns2.${inp_hostname}"
  inp_ns2_ip="${inp_ipv4}"

  # Apply hostname at the OS layer now so later steps see the right name.
  hostnamectl set-hostname "$inp_hostname" 2>/dev/null || true
  if ! grep -q "[[:space:]]${inp_hostname}\([[:space:]]\|$\)" /etc/hosts 2>/dev/null; then
    printf '%s\t%s\n' "$inp_ipv4" "$inp_hostname" >> /etc/hosts
  fi

  # Export for write_config_file. Not using a file because we write to
  # /etc/jabali-panel/config.toml later in the install flow anyway.
  JABALI_SRV_HOSTNAME="$inp_hostname"
  JABALI_SRV_IPV4="$inp_ipv4"
  JABALI_SRV_IPV6="$inp_ipv6"
  JABALI_SRV_NS1_NAME="$inp_ns1_name"
  JABALI_SRV_NS1_IPV4="$inp_ns1_ip"
  JABALI_SRV_NS2_NAME="$inp_ns2_name"
  JABALI_SRV_NS2_IPV4="$inp_ns2_ip"
  JABALI_SERVER_CONFIGURED=0
  export JABALI_SRV_HOSTNAME JABALI_SRV_IPV4 JABALI_SRV_IPV6 \
         JABALI_SRV_NS1_NAME JABALI_SRV_NS1_IPV4 \
         JABALI_SRV_NS2_NAME JABALI_SRV_NS2_IPV4 \
         JABALI_SERVER_CONFIGURED

  _ok "captured server identity: ${inp_hostname} (${inp_ipv4})"
}

# ---------- step 1: base packages -------------------------------------------

install_base_packages() {
  _log "installing all system packages in one batch"
  export DEBIAN_FRONTEND=noninteractive

  # Re-runs on a host whose previous install crashed mid-way leave
  # /etc/apt/sources.list.d/sury-php.list (and the matching
  # NodeSource list) behind. The bootstrap apt update below would
  # then re-fetch their indexes — and Sury's Fastly edge returns 418
  # to flagged datacenter IPs, killing the whole install before
  # _install_sury_source has a chance to write the UA workaround.
  #
  # Wipe the stale third-party lists upfront so the bootstrap update
  # only touches the distro mirror (no 418 risk). The repos are
  # re-added immediately below by _install_sury_source /
  # _install_nodesource_source with the UA workaround in place.
  rm -f /etc/apt/sources.list.d/sury-php.list \
        /etc/apt/sources.list.d/nodesource.list

  # Bootstrap: `gpg` (from gnupg) + `curl` + `ca-certificates` must be
  # present BEFORE we add third-party repos (Sury, NodeSource) and verify
  # their GPG keys. Minimal LXC containers often ship without gnupg. Two
  # apt runs total (this bootstrap + the big install below) is still a
  # huge win over the pre-consolidation 6 calls.
  _spin "apt update (bootstrap)" \
    apt-get update -qq
  _spin "apt install bootstrap (gnupg + ca-certificates + curl)" \
    apt-get install -y -qq --no-install-recommends gnupg ca-certificates curl

  # Third-party repos added BEFORE the big install so one `apt-get update`
  # sees them and one `apt-get install` resolves everything together. Each
  # adder is idempotent (bails out if the source file already exists).
  _install_sury_source
  _install_nodesource_source

  _spin "apt update (with Sury + NodeSource)" \
    apt-get update -qq

  # PowerDNS's postinst would restart pdns before its MySQL backend is
  # configured (fails loudly with exit 99 + a systemctl status dump). Drop
  # a policy-rc.d that tells dpkg to skip service starts during this
  # install — every service in the batch (nginx, php-fpm, pdns) is
  # explicitly enabled+started later in its own step, so "don't auto-
  # start" is harmless across the board.
  local policy_rc=/usr/sbin/policy-rc.d
  local policy_rc_preexisted=0
  if [[ -e "$policy_rc" ]]; then
    policy_rc_preexisted=1
    mv "$policy_rc" "${policy_rc}.jabali-bak"
  fi
  cat > "$policy_rc" <<'POLICYEOF'
#!/bin/sh
# Temporarily installed by jabali-panel install.sh during the one-shot
# package batch. Tells dpkg to skip service start during install — every
# service is explicitly enabled+started later.
exit 101
POLICYEOF
  chmod 0755 "$policy_rc"

  # Sury's PHP extension packaging drifts between versions (8.5 ships
  # OPcache inside -common instead of as a standalone -opcache package).
  # Probe apt-cache for each optional extension per PHP version and
  # include only what's actually available.
  local php_versions="${JABALI_PHP_VERSIONS:-8.5}"
  local -a php_extensions=()
  local version
  for version in $php_versions; do
    php_extensions+=("php${version}-fpm" "php${version}-cli")
    local ext
    for ext in mysql mbstring zip gd curl xml intl bcmath opcache; do
      if apt-cache show "php${version}-${ext}" >/dev/null 2>&1; then
        php_extensions+=("php${version}-${ext}")
      else
        _log "php${version}-${ext} not in apt sources — skipping (bundled elsewhere or unavailable)"
      fi
    done
  done

  # One big install. Downstream functions (install_nginx, _install_php_version,
  # install_node, install_powerdns, setup_certbot) short-circuit on
  # `command -v` / package-present checks now that the packages land here.
  _spin "apt install system packages (this is the long one)" \
    apt-get install -y -qq --no-install-recommends \
      git curl ca-certificates build-essential tar bzip2 unzip openssl gnupg \
      mariadb-server mariadb-client \
      php-cli php-mysql php-curl php-xml php-mbstring php-gd php-zip \
      composer \
      rsync acl \
      systemd-resolved \
      quota quotatool xfsprogs \
      nginx \
      certbot python3-certbot-nginx \
      nodejs \
      pdns-server pdns-backend-mysql pdns-recursor \
      bind9-dnsutils \
      ufw yq \
      redis-server redis-tools \
      "${php_extensions[@]}"

  # Undo the policy-rc.d trap regardless of exit path above (set -e would
  # have left the trap in place — restore is best-effort but ordered so
  # the original file comes back if one existed).
  rm -f "$policy_rc"
  if [[ "$policy_rc_preexisted" == "1" ]]; then
    mv "${policy_rc}.jabali-bak" "$policy_rc"
  fi

  # M6.3 Debian packaging fact-check (2026-04-22): pdns-server and
  # pdns-recursor both run as `pdns:pdns` on Debian — the recursor
  # package does NOT create its own `pdns-recursor` user/group. Our
  # recursor.conf below sets `setuid=pdns setgid=pdns` to match, and
  # recursor.forwards is chowned root:pdns so the daemon can read it.
  # The earlier hard-fail check against a `pdns-recursor` group was
  # wrong — it killed every clean install because the group never
  # existed. `pdns` group is guaranteed by pdns-server's postinst
  # (pdns-server is in the same apt batch above).
  if ! getent group pdns >/dev/null; then
    _die "pdns group missing after apt-install — pdns-server postinst failed; run 'apt-get install -f' and re-run install.sh"
  fi

  # Make systemd-resolved actually usable by the panel's DNS Resolvers
  # feature. Historically the installer just apt-installed the package
  # and left state untouched "so the admin's existing DNS isn't
  # disrupted" — but on a dedicated jabali-panel host there is no
  # pre-existing DNS-manager to preserve, and the effect of the
  # hands-off stance was that clicking "Save Resolvers" in the UI
  # appeared to succeed (drop-in written to disk) while doing nothing
  # useful (nobody reads the drop-in because resolved isn't running).
  #
  # So: normalize to "unmasked + enabled + running" on every install.
  # Only rewire /etc/resolv.conf if it's a plain regular file today —
  # if it's already a symlink, another tool (resolvconf, NetworkManager,
  # or a prior systemd-resolved setup) owns it and we must not fight
  # that. Idempotent across reinstalls.
  local resolved_state
  resolved_state="$(systemctl is-enabled systemd-resolved.service 2>/dev/null || true)"

  if [[ "$resolved_state" == "masked" ]]; then
    _log "unmasking systemd-resolved (was masked; image default or prior admin action)"
    systemctl unmask systemd-resolved.service
    resolved_state="disabled"
  fi

  if [[ "$resolved_state" != "enabled" ]] || ! systemctl is-active --quiet systemd-resolved.service; then
    _log "enabling + starting systemd-resolved"
    if ! systemctl enable --now systemd-resolved.service; then
      _warn "systemd-resolved failed to start — panel DNS Resolvers page will be non-functional until fixed manually (check 'journalctl -u systemd-resolved')"
    fi
  fi

  # Pre-seed the panel DNS drop-in when the host has no upstream
  # configured via /etc/resolv.conf AND no upstream advertised on any
  # link in resolvectl. Happens on Debian 13 minimal where resolv.conf
  # ships pre-symlinked to the stub but no link ever pushes DNS (static
  # IP install, or the QEMU/LXC DHCP dropped the DNS option). Without
  # this, any resolved restart later in install.sh (disable_llmnr does
  # one) exposes the "stub with zero upstream" state and every curl in
  # the rest of the script SERVFAILs.
  #
  # Only seeds when jabali.conf is missing — if the admin already wrote
  # one via the panel UI we do not clobber it.
  if systemctl is-active --quiet systemd-resolved.service; then
    local panel_dropin_early="/etc/systemd/resolved.conf.d/jabali.conf"
    if [[ ! -f "$panel_dropin_early" ]]; then
      # Active-resolution probe: does a well-known hostname actually
      # resolve right now? Cheaper + more reliable than parsing
      # `resolvectl status` (which has exit-code + output-format quirks
      # across systemd versions and can kill the script under
      # `set -euo pipefail`). If getent fails, we know any curl later
      # in install.sh will also fail — seed the fallback drop-in.
      if ! getent hosts deb.debian.org >/dev/null 2>&1; then
        _warn "no upstream DNS resolves (deb.debian.org lookup failed) — seeding ${panel_dropin_early} with Cloudflare + Quad9 (override via Admin → DNS)"
        install -d -m 0755 /etc/systemd/resolved.conf.d
        cat > "$panel_dropin_early" <<'EARLYDNS'
# Managed by jabali-panel — edits via /jabali-admin/settings → DNS.
# install.sh found no working upstream DNS and seeded these public
# defaults so curl/apt steps later in install.sh don't SERVFAIL.
[Resolve]
DNS=1.1.1.1 9.9.9.9
EARLYDNS
        chmod 0644 "$panel_dropin_early"
        systemctl restart systemd-resolved.service 2>/dev/null || true
        # Give resolved a beat to accept the drop-in before the next
        # step hits the network.
        sleep 1
      fi
    fi
  fi

  # Hand /etc/resolv.conf over to systemd-resolved's stub so queries
  # actually traverse the drop-in the panel writes. Gated on:
  #   1. resolv.conf is a plain file (not already a symlink — symlink
  #      means another manager owns it; don't stomp)
  #   2. systemd-resolved started successfully above (checking is-active
  #      as the cheapest post-start health probe)
  if [[ ! -L /etc/resolv.conf && -e /etc/resolv.conf ]] \
     && systemctl is-active --quiet systemd-resolved.service; then

    # Before flipping the symlink, migrate the admin's existing DNS
    # config into resolved so the host doesn't go dark. If /etc/resolv.conf
    # has (say) corporate DNS at 10.0.0.1 + search corp.example.com,
    # a raw symlink flip would point all lookups at resolved, which
    # has no upstreams configured → every query SERVFAILs until the
    # admin visits the panel UI.
    #
    # Write harvested values to /etc/systemd/resolved.conf.d/jabali.conf
    # (the panel's own drop-in) — NOT a separate migrated.conf file —
    # so the panel UI shows Source: drop-in with the admin's previous
    # upstreams pre-filled, giving them a one-click point to modify.
    # Skip if jabali.conf already exists so re-running install.sh on a
    # host where the admin has already saved via the UI doesn't clobber
    # their panel-managed config.
    local panel_dropin="/etc/systemd/resolved.conf.d/jabali.conf"
    if [[ ! -f "$panel_dropin" ]]; then
      # Harvest nameservers: exclude only 127.0.0.53 (self-reference
      # once we symlink to the stub). Preserve everything else including
      # 127.0.0.1 (local dnsmasq/unbound) and RFC 1918 addresses
      # (corporate resolvers).
      local migrated_ns migrated_search
      migrated_ns="$(awk '/^nameserver[[:space:]]+/{print $2}' /etc/resolv.conf \
                     | grep -v '^127\.0\.0\.53$' \
                     | paste -sd' ' -)"
      # Take first search/domain directive (resolv.conf's older 'domain'
      # keyword is equivalent to a single-entry 'search').
      migrated_search="$(awk '/^(search|domain)[[:space:]]+/{print $2; exit}' /etc/resolv.conf)"

      # Fallback: if the host's /etc/resolv.conf had no harvestable
      # upstream (empty file, comments-only, or only 127.0.0.53),
      # seed the drop-in with Cloudflare + Quad9. Without this, the
      # symlink flip below points /etc/resolv.conf at a resolved stub
      # that has ZERO upstreams configured and the host goes dark —
      # exactly the "lost all DNS after install" failure we hit on
      # Debian 13 minimal images.
      local seed_source="migrated"
      if [[ -z "$migrated_ns" ]]; then
        migrated_ns="1.1.1.1 9.9.9.9"
        seed_source="fallback"
        _warn "no upstream harvested from /etc/resolv.conf — seeding panel drop-in with Cloudflare + Quad9 defaults (override via Admin → DNS)"
      fi

      _log "writing panel DNS drop-in (${seed_source}): ${migrated_ns}${migrated_search:+ (search: ${migrated_search})}"
      install -d -m 0755 /etc/systemd/resolved.conf.d
      {
        echo "# Managed by jabali-panel — edits via /jabali-admin/settings → DNS."
        if [[ "$seed_source" == "migrated" ]]; then
          echo "# Seeded by install.sh from the host's previous /etc/resolv.conf"
          echo "# so the host's DNS stayed working when install.sh handed"
          echo "# /etc/resolv.conf over to systemd-resolved's stub."
        else
          echo "# install.sh found no usable upstream in the host's previous"
          echo "# /etc/resolv.conf and seeded these public defaults so the"
          echo "# host didn't go dark after the symlink flip below."
        fi
        echo "# The admin can modify these upstreams via the panel UI at any"
        echo "# time; changes land in this same file."
        echo "[Resolve]"
        echo "DNS=${migrated_ns}"
        [[ -n "$migrated_search" ]] && echo "Domains=${migrated_search}"
      } > "$panel_dropin"
      chmod 0644 "$panel_dropin"
      systemctl restart systemd-resolved.service 2>/dev/null || true
    fi

    _log "linking /etc/resolv.conf → /run/systemd/resolve/stub-resolv.conf (was plain file)"
    ln -sf /run/systemd/resolve/stub-resolv.conf /etc/resolv.conf

    # Post-flip sanity probe. If DNS is broken after our changes, the
    # admin needs to know BEFORE we move on to 700 more lines of apt/
    # systemd work that might try to reach package registries. Warn
    # loudly but don't die — they can still fix it manually via
    # /etc/systemd/resolved.conf.d/jabali.conf.
    if ! getent hosts deb.debian.org >/dev/null 2>&1; then
      _warn "DNS still broken after resolved setup: 'getent hosts deb.debian.org' failed."
      _warn "Check: resolvectl status; cat /etc/systemd/resolved.conf.d/jabali.conf"
    fi
  fi

  _ok "base packages ready"
}

# Note: systemd-resolved is installed, unmasked, enabled, started, and
# (when /etc/resolv.conf was previously a plain file) has
# /etc/resolv.conf pointed at its stub so the panel's DNS Resolvers
# page works end-to-end on a fresh install. Hosts where /etc/resolv.conf
# is a symlink to another manager's output are left untouched to avoid
# fighting that manager.

# ---------- step 1d: M18 — cgroups v2 probe + disk quota + /tmp tmpfs -------
#
# Three idempotent setup steps that make the M18 per-user limits
# enforcement surfaces available:
#
# 1. Assert cgroups v2 unified hierarchy is the one in use. Debian 13's
#    default, but a host with a custom kernel command-line could have
#    systemd.unified_cgroup_hierarchy=0 which breaks every slice drop-in
#    we emit. Detect now, fail loud.
#
# 2. POSIX user quota on /home. Only runs on fresh hosts where we're
#    adding the mount option for the first time — on existing hosts
#    with a live /home we refuse to remount (would kill running FPM
#    workers). Branches by filesystem type: ext4 is a fstab edit +
#    quotacheck + quotaon; xfs also needs xfs_quota enable; btrfs/zfs
#    fail loud with the upgrade-path message.
#
# 3. /tmp on tmpfs with a size cap. Prevents a single user from filling
#    the host disk via /tmp bypassing their home quota. Default 1 GB,
#    configurable via JABALI_TMP_SIZE env.
configure_cgroups_v2() {
  local fstype
  fstype="$(stat -fc %T /sys/fs/cgroup 2>/dev/null || echo '')"
  if [[ "$fstype" != "cgroup2fs" ]]; then
    _err "cgroups v2 unified hierarchy is not active (/sys/fs/cgroup is $fstype)."
    _err "Boot with systemd.unified_cgroup_hierarchy=1 or remove the override."
    exit 1
  fi
  _ok "cgroups v2 unified hierarchy active"
}

# configure_disk_quota sets up POSIX user quota on /home. Idempotent,
# branches on filesystem type, prompts (TTY) or fails loud (unattended)
# when it can't make progress.
configure_disk_quota() {
  local home_mount home_fs
  # Find the mount /home lives on.
  home_mount="$(stat -c%m /home 2>/dev/null || echo /)"
  # Use findmnt for the precise fstype — `stat -fc %T` returns the
  # composite "ext2/ext3" label for the entire ext family because the
  # kernel's statfs f_type values are shared. findmnt asks the mount
  # table directly and returns "ext4" / "ext3" / "ext2" / "xfs"
  # exactly. Fall back to stat if findmnt isn't available (rare on
  # Debian 13).
  home_fs="$(findmnt -no FSTYPE "$home_mount" 2>/dev/null || stat -fc %T /home 2>/dev/null || echo unknown)"
  _log "quota probe: /home is on mount $home_mount (fs=$home_fs)"

  # Filesystem support matrix — ADR-0032 §2.
  # NB: `stat -fc %T` on Debian 13 reports "ext2/ext3" (composite label)
  # for ext3/ext4 filesystems — the kernel's statfs f_type values for
  # ext2/3/4 are merged for historical reasons. Match that label too.
  case "$home_fs" in
    ext4|ext3|ext2|"ext2/ext3")
      # ext4-family works with fstab usrquota + quotacheck + quotaon.
      ;;
    xfs)
      # xfs also works but needs xfs_quota enable after mount.
      ;;
    btrfs|zfs|tmpfs|ramfs)
      # Unsupported filesystems: warn and skip quota setup. cgroups v2
      # enforcement still works (cpu / memory / io / tasks) — we just
      # don't block the whole install on a disk-quota issue. The panel's
      # reconciler will log `quota_applied=false` on each apply; operator
      # reads the runbook to migrate /home when convenient.
      _warn "filesystem '$home_fs' on /home does not support POSIX quota; skipping disk-quota setup."
      _warn "cgroups limits (cpu/memory/io/tasks) will still apply. See plans/m18-resource-limits-runbook.md §4 to migrate."
      return 0
      ;;
    *)
      _warn "unknown filesystem '$home_fs' on /home; skipping disk-quota setup (cgroups still active)."
      return 0
      ;;
  esac

  # /home on / is supported (matches cPanel/DirectAdmin behavior). The
  # M18 reconciler only ever calls setquota for hosting UIDs (>=1000); root
  # and system daemons (UID < 1000) are exempt by absence — they never get
  # a setquota call, so EDQUOT can't trip them. ext4 supports online quota
  # via tune2fs -O quota + remount, no offline quotacheck needed.
  #
  # Operator can still opt out by setting JABALI_SKIP_QUOTA=1 in the env
  # before running install.sh (e.g. for hosts where the operator wants to
  # rely on cgroup IO caps only).
  if [[ "${JABALI_SKIP_QUOTA:-0}" == "1" ]]; then
    _warn "JABALI_SKIP_QUOTA=1 — skipping POSIX quota setup (cgroups still active)."
    return 0
  fi

  # Check whether fstab already has usrquota on this mount.
  if grep -E "^[^#]*\s$home_mount\s" /etc/fstab | grep -q "usrquota"; then
    _log "fstab: $home_mount already has usrquota set"
  else
    _log "adding usrquota,grpquota to /etc/fstab entry for $home_mount"
    # Preserve the original line; append ",usrquota,grpquota" to the
    # 4th field (options) for the mount-point row. Uses a unique marker
    # to avoid double-patching on reinstall.
    #
    # Prior awk implementation used `sub(regex, "\\1\\2,usrquota,…")`
    # relying on backreference expansion inside the replacement string.
    # POSIX awk does NOT support backrefs in sub/gsub replacements;
    # gawk supports `\1`…`\9` but only via --posix off and even then
    # treats `\1\2` inside a double-quoted shell string as literal.
    # The old code wrote the literal 10 characters `\1\2,usrquota,grpquota`
    # into fstab line 12, bricking the mount entry (systemd ignored the
    # line, / stayed mounted WITHOUT usrquota, every subsequent
    # quotacheck/quotaon failed with "Mountpoint has no quota enabled").
    #
    # Replacement: split the matched line by field index in awk so we
    # rebuild it explicitly. Preserves original whitespace collapsed to
    # single spaces, which systemd accepts.
    if ! grep -q "# jabali-m18-quota" /etc/fstab; then
      cp -p /etc/fstab /etc/fstab.jabali-m18.bak
      awk -v mnt="$home_mount" '
        !/^#/ && $2 == mnt {
          # $4 = current options field. Append ",usrquota,grpquota".
          $4 = $4 ",usrquota,grpquota"
          print $0 " # jabali-m18-quota"
          next
        }
        { print }
      ' /etc/fstab.jabali-m18.bak > /etc/fstab
      _ok "fstab patched; remount $home_mount for changes to take effect"
    fi
  fi

  # Remount to pick up the new options. We pass usrquota,grpquota on
  # the cmdline explicitly (not just "remount") so the kernel honors
  # them immediately — the fstab path alone depends on the line being
  # parsed cleanly, and any syntax drift would silently leave quota
  # off. Cmdline options are authoritative.
  if ! mount -o remount,usrquota,grpquota "$home_mount" 2>/dev/null; then
    _warn "remount of $home_mount failed (busy). Reboot to apply quota, then re-run this step."
    return 0
  fi

  # Filesystem-specific activation. "xfs" is the only branch that
  # needs the extra xfs_quota enable; the ext* family (including the
  # composite "ext2/ext3" label) falls through to the standard
  # quotacheck + quotaon sequence below.
  if [[ "$home_fs" == "xfs" ]]; then
    # xfs's mount option alone doesn't flip accounting on — need
    # xfs_quota's enable command.
    xfs_quota -x -c "enable -u" "$home_mount" || {
      _err "xfs_quota enable failed on $home_mount"
      exit 1
    }
    _ok "xfs user quota enabled on $home_mount"
  else
    # ext2/ext3/ext4.
    #
    # Two kernel paths:
    #
    # 1. Hidden-inode quota (modern, default on Debian 13 mkfs.ext4 since
    #    enable_quota=true is the /etc/mke2fs.conf default). The `quota`
    #    feature is baked into the superblock at format time, quota
    #    inodes live at fixed reserved inode numbers, and the kernel
    #    keeps accounting inline. No aquota.user file. No quotacheck
    #    scan. `quotaon` simply flips enforcement on — works live on a
    #    busy root filesystem.
    #
    # 2. External-file quota (legacy, pre-Debian-12 or custom mkfs). Uses
    #    aquota.user / aquota.group at the mount root, rebuilt by
    #    quotacheck. Fragile on a busy `/` because quotacheck wants to
    #    scan every inode without concurrent writes; kernel refuses on
    #    live root FS with certain version combos.
    #
    # Detection: tune2fs -l on the backing block device. If the
    # Filesystem features list contains `quota`, use path 1; else
    # fall back to path 2 (works on dedicated /home partitions).
    local block_dev
    block_dev="$(findmnt -no SOURCE "$home_mount" 2>/dev/null || true)"
    local has_sb_quota=0
    if [[ -n "$block_dev" ]] \
        && tune2fs -l "$block_dev" 2>/dev/null \
           | awk -F: '/^Filesystem features:/{print $2}' \
           | tr ' ' '\n' | grep -qw 'quota'; then
      has_sb_quota=1
    fi

    if (( has_sb_quota == 1 )); then
      # Hidden-inode path. quotaon uses the SB feature directly — no
      # aquota.user required. Works on a live `/` because the kernel
      # has been tracking usage since mount time; quotaon just flips
      # the enforce bit.
      if ! quotaon -v "$home_mount" >/dev/null 2>&1; then
        # quotaon returns non-zero when quota is already on some versions —
        # probe to tell "already on" apart from "real failure".
        if quotaon -pu "$home_mount" 2>/dev/null | grep -qi 'is on'; then
          :
        else
          _warn "quotaon $home_mount failed despite SB quota feature; manual intervention required (try 'quotaon -vu $home_mount')"
          return 0
        fi
      fi
      _ok "POSIX user quota active on $home_mount (hidden inodes)"
    else
      # Legacy external-file path. Fragile on busy `/`; reliable on
      # dedicated /home partitions.
      local quota_file="$home_mount/aquota.user"
      [[ "$home_mount" == "/" ]] && quota_file="/aquota.user"
      if [[ ! -f "$quota_file" ]]; then
        _log "building $quota_file via quotacheck -mcug (may take time on large filesystems)"
        if ! quotacheck -mcugF vfsv1 "$home_mount"; then
          _warn "quotacheck failed on $home_mount; quota plumbing left inactive (cgroups still enforce cpu/mem/io/tasks)"
          return 0
        fi
      fi
      if ! quotaon -v "$home_mount" >/dev/null 2>&1; then
        _warn "quotaon $home_mount failed; quota plumbing left inactive"
        return 0
      fi
      _ok "POSIX user quota active on $home_mount (external aquota.user)"
    fi
  fi
}

# configure_tmp_tmpfs mounts /tmp as tmpfs with a size cap so a user
# can't bypass their home quota via /tmp writes. Default 1 GB, override
# via JABALI_TMP_SIZE (passed as a tmpfs-compatible size string, e.g.
# "2G" or "512M").
configure_tmp_tmpfs() {
  local size="${JABALI_TMP_SIZE:-1G}"

  # If /tmp is already tmpfs, nothing to do.
  if [[ "$(stat -fc %T /tmp 2>/dev/null)" == "tmpfs" ]]; then
    _log "/tmp already on tmpfs; leaving as-is"
    return 0
  fi

  # Add fstab entry idempotently; reboot or remount picks it up.
  if ! grep -q "# jabali-m18-tmp" /etc/fstab; then
    _log "adding tmpfs mount for /tmp (size=$size) to /etc/fstab"
    echo "tmpfs /tmp tmpfs rw,nosuid,nodev,size=$size,mode=1777 0 0 # jabali-m18-tmp" >> /etc/fstab
  fi

  # Do NOT remount /tmp automatically on an existing host — running
  # processes often hold open file handles in /tmp (package managers,
  # editors, systemd timers) and remounting would corrupt them. Leave
  # the fstab change for the next reboot.
  _warn "/tmp fstab entry added; reboot to activate tmpfs mount with size=$size cap"
}

# ---------- step 1b: nginx ----------------------------------------------------

install_nginx() {
  # nginx is installed in install_base_packages's one-shot apt batch.
  # This function owns the post-install config (vhost dirs, include
  # line, enable+start). Kept as a separate step so the ordering in
  # main() stays readable and so reinstalls re-run the config checks.
  if ! command -v nginx >/dev/null 2>&1; then
    _die "nginx binary not found — install_base_packages should have installed it"
  fi
  _ok "nginx present ($(nginx -v 2>&1 | awk -F/ '{print $2}'))"

  # Ensure sites-available / sites-enabled dirs exist (some minimal
  # nginx packages skip them).
  install -d -m 0755 /etc/nginx/sites-available
  install -d -m 0755 /etc/nginx/sites-enabled

  # Enable the include if not already present.
  if ! grep -q 'sites-enabled' /etc/nginx/nginx.conf 2>/dev/null; then
    _log "adding sites-enabled include to nginx.conf"
    sed -i '/http {/a \    include /etc/nginx/sites-enabled/*.conf;' /etc/nginx/nginx.conf
  fi

  systemctl enable --quiet nginx
  systemctl start nginx 2>/dev/null || true
}

# ---------- step 1b2: PHP/FPM (multi-version via Sury) -------------------------

_install_sury_source() {
  # Sury GPG fingerprint validation. Source: https://packages.sury.org/php/README.txt
  # Last verified: 2026-04-17 (DPA CA Certificate, Ondřej Surý)
  local SURY_GPG_FINGERPRINT="15058500A0235D97F5D10063B188E2B695BD4743"
  # The launchpad PPA hosts the SAME upstream packages (Ondřej Surý
  # maintains both) signed by the same key, but is served from
  # launchpad.net rather than Fastly — bypassing the datacenter-IP
  # 418 false-positives. We prefer it on Ubuntu and fall back to
  # packages.sury.org for Debian (no PPA there).
  local LP_GPG_FINGERPRINT="14AA40EC0831756756D7F66C4F4EA0AAE5267A6C"

  # Always write the Fastly 418 UA workaround, even when the .list
  # below short-circuits — earlier installs from before this fix
  # landed have the source list but not the apt.conf.d override, and
  # they crash on every apt-get update. Idempotent: writing the same
  # bytes is a noop.
  _install_sury_apt_ua_workaround

  [[ -f /etc/apt/sources.list.d/sury-php.list ]] && { _ok "Sury PHP source already configured"; return; }

  # Derive the distro id + codename without depending on lsb_release
  # (not installed on minimal Debian 13). /etc/os-release is a
  # systemd-era standard and is always present.
  local distro_id codename
  if [[ -r /etc/os-release ]]; then
    # shellcheck disable=SC1091
    distro_id=$(. /etc/os-release && echo "${ID:-}")
    # shellcheck disable=SC1091
    codename=$(. /etc/os-release && echo "${VERSION_CODENAME:-}")
  fi
  [[ -n "$codename" ]] || _die "cannot determine distro codename (no VERSION_CODENAME in /etc/os-release)"

  # Ensure target dir exists on minimal Debian images.
  install -d -m 0755 /usr/share/keyrings

  if [[ "$distro_id" == "ubuntu" ]]; then
    # ppa:ondrej/php — same packages as packages.sury.org, served by
    # launchpad. No Fastly in front, no 418 risk. The launchpad
    # signing key has its own fingerprint distinct from Sury's.
    _log "fetching Ubuntu PPA signing key for ondrej/php"
    curl -fsSL --connect-timeout 15 --max-time 60 \
      "https://keyserver.ubuntu.com/pks/lookup?op=get&search=0x${LP_GPG_FINGERPRINT}" \
      | gpg --no-default-keyring --no-tty --batch --dearmor --yes \
        -o /usr/share/keyrings/sury-php.gpg \
      || _die "failed to fetch ondrej/php signing key from keyserver.ubuntu.com"

    local lp_gpg_out
    if ! lp_gpg_out="$(GNUPGHOME="$(mktemp -d)" gpg --no-default-keyring --no-tty --batch --show-keys /usr/share/keyrings/sury-php.gpg 2>&1)"; then
      _err "gpg --show-keys failed; output was:"
      printf '%s\n' "$lp_gpg_out" >&2
      _die "cannot parse PPA key at /usr/share/keyrings/sury-php.gpg"
    fi
    if ! grep -q "$LP_GPG_FINGERPRINT" <<< "$lp_gpg_out"; then
      _err "gpg parsed the key but the fingerprint doesn't match. gpg output:"
      printf '%s\n' "$lp_gpg_out" >&2
      _die "ondrej/php PPA key fingerprint mismatch. Expected: $LP_GPG_FINGERPRINT"
    fi
    _ok "ondrej/php PPA signing key validated"

    cat > /etc/apt/sources.list.d/sury-php.list <<EOF
deb [signed-by=/usr/share/keyrings/sury-php.gpg] https://ppa.launchpadcontent.net/ondrej/php/ubuntu ${codename} main
EOF
    _ok "added ondrej/php PPA for ${codename} (launchpad mirror — bypasses Fastly)"
    return
  fi

  # Debian: packages.sury.org is the only option. The Fastly 418
  # affects fewer Debian-on-VPS installs in practice; if it bites,
  # the operator is currently the one to debug.
  _log "downloading Sury GPG key (curl: connect 15s, total 60s)"
  curl -fsSL --connect-timeout 15 --max-time 60 \
    https://packages.sury.org/php/apt.gpg -o /usr/share/keyrings/sury-php.gpg \
    || _die "curl failed to fetch Sury GPG key from packages.sury.org — check egress / DNS from this host"

  _log "verifying Sury GPG key fingerprint"
  # Capture gpg output + exit code independently so we can surface both
  # to the operator if anything goes wrong. The `if ! cmd` form disables
  # set -e just for the capture. GNUPGHOME=mktemp skips any ~/.gnupg /
  # gpg-agent startup, which hangs silently on first-gpg invocation
  # inside minimal LXC containers.
  local sury_gpg_out
  if ! sury_gpg_out="$(GNUPGHOME="$(mktemp -d)" gpg --no-default-keyring --no-tty --batch --show-keys /usr/share/keyrings/sury-php.gpg 2>&1)"; then
    _err "gpg --show-keys failed; output was:"
    printf '%s\n' "$sury_gpg_out" >&2
    _die "cannot parse Sury GPG key at /usr/share/keyrings/sury-php.gpg"
  fi
  if ! grep -q "$SURY_GPG_FINGERPRINT" <<< "$sury_gpg_out"; then
    _err "gpg parsed the key but the fingerprint doesn't match. gpg output:"
    printf '%s\n' "$sury_gpg_out" >&2
    _die "Sury GPG key fingerprint mismatch. Expected: $SURY_GPG_FINGERPRINT"
  fi
  _ok "Sury GPG key validated"

  cat > /etc/apt/sources.list.d/sury-php.list <<EOF
deb [signed-by=/usr/share/keyrings/sury-php.gpg] https://packages.sury.org/php/ ${codename} main
EOF
  _ok "added Sury PHP repository for ${codename}"
}

# packages.sury.org sits behind Fastly; the Fastly edge returns HTTP
# 418 ("I'm a teapot") when apt's default User-Agent
# ("Debian APT-HTTP/1.3 (...)") arrives from a flagged datacenter IP.
# Override the User-Agent for Sury fetches only — keep the default
# elsewhere so we don't muddy other repos' bot heuristics. Fastly
# accepts a plain Mozilla string. Also bumps Acquire::Retries so
# transient network blips don't crash the whole install.
#
# Split out from _install_sury_source so it always runs (the source
# function early-returns when the .list exists, but a re-run on a
# half-installed host still needs this conf written).
_install_sury_apt_ua_workaround() {
  cat > /etc/apt/apt.conf.d/98-jabali-sury-ua.conf <<'APTEOF'
// Workaround Fastly 418 on packages.sury.org (Debian/Ubuntu
// datacenter-IP false positives). Per-host User-Agent overrides
// in apt's syntax are unreliable across versions; setting it
// globally is the only form that consistently works. Other
// archives don't care what the apt client identifies as.
Acquire::http::User-Agent "Mozilla/5.0 (X11; Linux x86_64) jabali-panel-installer";
Acquire::https::User-Agent "Mozilla/5.0 (X11; Linux x86_64) jabali-panel-installer";
Acquire::Retries "3";
APTEOF
}

_install_php_version() {
  local version="$1"
  # PHP packages (php<v>-fpm, php<v>-cli, optional extensions) are
  # installed in install_base_packages's one-shot apt batch. This
  # function owns the per-version post-install config: placeholder
  # pool, FPM mask, default-pool disable.
  if ! command -v "php${version}" >/dev/null 2>&1; then
    _die "php${version} binary not found — install_base_packages should have installed it (check JABALI_PHP_VERSIONS=\"${JABALI_PHP_VERSIONS:-8.5}\")"
  fi
  _ok "PHP ${version} present"

  local pool_file="/etc/php/${version}/fpm/pool.d/www.conf"
  [[ -f "$pool_file" ]] && { mv "$pool_file" "${pool_file}.disabled"; _log "disabled default pool for PHP ${version}"; }

  # Install a placeholder pool so php-fpm can start with no hosting
  # users yet. Without it, an empty pool.d/ causes FPM init to fail
  # ("No pool defined"). Inlined via heredoc because install_php runs
  # before clone_or_update_repo — we don't yet have the repo tree to
  # copy from. A copy also exists at install/php/_jabali-placeholder.conf
  # for reference; the heredoc here is the source of truth the installer
  # actually uses.
  cat > "/etc/php/${version}/fpm/pool.d/_jabali-placeholder.conf" <<'PLACEHOLDER_EOF'
; Placeholder pool installed by install.sh so php-fpm can start on a
; fresh host with no hosting users yet. No-op ondemand pool listening
; on an unused loopback socket. Safe to leave in place. Moot once
; slices plan step 6 masks the global php<v>-fpm.service in favor of
; per-user masters (jabali-fpm@<user>.service).

[_jabali_placeholder]
user = www-data
group = www-data
listen = /run/php/php-fpm-jabali-placeholder.sock
listen.owner = www-data
listen.group = www-data
listen.mode = 0600
pm = ondemand
pm.max_children = 1
pm.process_idle_timeout = 10s
PLACEHOLDER_EOF
  chmod 0644 "/etc/php/${version}/fpm/pool.d/_jabali-placeholder.conf"
  _ok "installed placeholder pool for PHP ${version}"


  # Mask the distro's global php<v>-fpm.service — per ADR-0025 we run
  # one FPM master per hosting user (jabali-fpm@<user>.service) inside
  # the per-user systemd slice, and a dedicated jabali-fpm@pma.service
  # for phpMyAdmin. The global unit must never run: it reads every
  # .conf in /etc/php/<v>/fpm/pool.d/ (including jabali-pma.conf and
  # jabali-<user>.conf), so on any apt transaction its postinst would
  # restart it and race the per-user masters for their UDS sockets,
  # leaving dpkg in a permanently half-configured state.
  #
  # apt's postinst unconditionally enables + starts the service, so
  # mask AFTER the package install has run and reset-failed any
  # residual failed state from a prior half-configured boot.
  systemctl reset-failed "php${version}-fpm.service" 2>/dev/null || true
  systemctl disable --now --quiet "php${version}-fpm.service" 2>/dev/null || true
  systemctl mask --quiet "php${version}-fpm.service"
  _ok "PHP ${version} installed; global php${version}-fpm.service masked (per-user jabali-fpm@<user>.service takes over)"
}

install_php() {
  _log "configuring PHP/FPM (packages installed in base batch; this runs per-version post-install config)"
  # Default install is PHP 8.5 (current stable). Sury supports 7.4–8.5;
  # set JABALI_PHP_VERSIONS to install additional versions side-by-side,
  # e.g. JABALI_PHP_VERSIONS="7.4 8.2 8.5" bash install.sh
  local php_versions="${JABALI_PHP_VERSIONS:-8.5}"
  local version
  for version in $php_versions; do
    _install_php_version "$version"
  done
}


# ---------- systemd slices: jabali root + user container ----------------------

# Install the top-of-hierarchy slice units and the FPM template service unit.
# Must run AFTER clone_or_update_repo because the unit files and shim scripts
# live under $REPO_DIR. No per-user provisioning yet (that happens in step 3).
install_jabali_slices() {
  _log "installing jabali systemd slices and FPM template"

  install -d -m 0755 /usr/local/libexec/jabali
  install -m 0755 "$REPO_DIR/install/systemd/fpm-pre-start" /usr/local/libexec/jabali/fpm-pre-start
  install -m 0755 "$REPO_DIR/install/systemd/fpm-exec" /usr/local/libexec/jabali/fpm-exec

  install -m 0644 "$REPO_DIR/install/systemd/jabali.slice" /etc/systemd/system/jabali.slice
  install -m 0644 "$REPO_DIR/install/systemd/jabali-user.slice" /etc/systemd/system/jabali-user.slice
  install -m 0644 "$REPO_DIR/install/systemd/jabali-fpm@.service" /etc/systemd/system/jabali-fpm@.service

  systemctl daemon-reload
  systemctl start jabali.slice jabali-user.slice

  _ok "jabali slices installed"
}

# Install the FPM pool config template. Must run AFTER
# clone_or_update_repo because the template file lives under $REPO_DIR.
# The agent reads this path at runtime via php.pool.apply.
install_php_pool_template() {
  mkdir -p /etc/jabali-panel
  install -d -m 0755 -o root -g root /etc/jabali-panel/fpm
  install -d -m 0755 -o root -g root /etc/jabali-panel/user-phpver
  local template_src="$REPO_DIR/install/php/jabali-php-pool.conf.tmpl"
  local template_dst="/etc/jabali-panel/php-pool.conf.tmpl"
  if [[ ! -f "$template_src" ]]; then
    _die "pool template missing at $template_src (is the repo clone complete?)"
  fi
  install -m 0644 "$template_src" "$template_dst"
  _ok "installed pool config template at $template_dst"
}

# ---------- step 1c: disabled page -------------------------------------------

install_disabled_page() {
  _log "installing branded disabled page"

  # Create the directory with proper permissions
  install -d -m 0755 /var/www/jabali-disabled

  # Write the disabled page HTML, idempotent via install(1)
  install -m 0644 /dev/stdin /var/www/jabali-disabled/index.html <<'EOF'
<!DOCTYPE html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width,initial-scale=1">
  <title>Site Disabled</title>
  <style>
    body { font-family: system-ui, -apple-system, Segoe UI, Roboto, sans-serif; max-width: 640px; margin: 4rem auto; padding: 0 1.25rem; color: #222; line-height: 1.5; }
    h1 { color: #d32f2f; margin-bottom: 0.25em; }
    .muted { color: #666; margin-top: 0; }
  </style>
</head>
<body>
  <h1>Site Disabled</h1>
  <p class="muted">This site has been disabled by its owner. Please check back later.</p>
</body>
</html>
EOF

  _ok "disabled page installed at /var/www/jabali-disabled/"
}

# ---------- step 1d: Node.js 22 LTS (for panel-ui) --------------------------

_install_nodesource_source() {
  # Idempotent NodeSource repo add. Called from install_base_packages
  # before the one-shot apt batch so nodejs resolves against
  # deb.nodesource.com/node_22.x instead of Debian's older nodejs.
  [[ -f /etc/apt/sources.list.d/nodesource.list ]] && { _ok "NodeSource repo already configured"; return; }

  _log "adding NodeSource repo for Node.js 22 (curl: connect 15s, total 60s)"
  install -d -m 0755 /etc/apt/keyrings
  # Fetch → tmp file so a network error surfaces distinctly from a gpg
  # parsing error. Same hang/diagnostic story as _install_sury_source.
  local ns_armored
  ns_armored="$(mktemp)"
  curl -fsSL --connect-timeout 15 --max-time 60 \
    https://deb.nodesource.com/gpgkey/nodesource-repo.gpg.key -o "$ns_armored" \
    || _die "curl failed to fetch NodeSource GPG key from deb.nodesource.com — check egress / DNS from this host"
  local ns_gpg_out
  if ! ns_gpg_out="$(GNUPGHOME="$(mktemp -d)" gpg --no-default-keyring --no-tty --batch --dearmor --yes -o /etc/apt/keyrings/nodesource.gpg "$ns_armored" 2>&1)"; then
    _err "gpg --dearmor failed on NodeSource key; output was:"
    printf '%s\n' "$ns_gpg_out" >&2
    _die "cannot dearmor NodeSource key"
  fi
  rm -f "$ns_armored"
  chmod 0644 /etc/apt/keyrings/nodesource.gpg
  echo 'deb [signed-by=/etc/apt/keyrings/nodesource.gpg] https://deb.nodesource.com/node_22.x nodistro main' \
    >/etc/apt/sources.list.d/nodesource.list
  _ok "NodeSource repo configured"
}

install_node() {
  # nodejs is installed in install_base_packages's one-shot apt batch
  # (NodeSource repo added by _install_nodesource_source before the
  # install). This function is now just a version-check + log.
  if ! command -v node >/dev/null 2>&1; then
    _die "node binary not found — install_base_packages should have installed it"
  fi
  local cur_major
  cur_major="$(node -v | sed -E 's/^v([0-9]+).*/\1/')"
  if [[ "$cur_major" -lt 22 ]]; then
    _warn "Node $cur_major is older than expected v22 — NodeSource repo may not have taken effect"
  fi
  _ok "Node $(node -v) / npm $(npm -v) present"
}

# ---------- step 2.5: MariaDB DB + scoped user ------------------------------

provision_mariadb() {
  _log "provisioning MariaDB database + user"

  systemctl enable --quiet --now mariadb
  # Wait briefly for the socket to appear on a freshly-installed box.
  for i in 1 2 3 4 5; do
    if mariadb -e 'SELECT 1' >/dev/null 2>&1; then break; fi
    sleep 1
  done
  if ! mariadb -e 'SELECT 1' >/dev/null 2>&1; then
    _die "MariaDB unreachable via unix_socket auth as root"
  fi

  local db_name="jabali_panel"
  local db_user="jabali_panel_app"
  local pw_file="/etc/jabali/db-password"

  if [[ -f "$pw_file" ]]; then
    _ok "DB password already generated at $pw_file"
  else
    _log "generating DB password → $pw_file"
    install -d -m 0750 -o root -g "$SERVICE_USER" "$(dirname "$pw_file")"
    umask 077
    openssl rand -hex 32 >"$pw_file"
    chmod 0640 "$pw_file"
    chown root:"$SERVICE_USER" "$pw_file"
  fi
  local db_pass
  db_pass="$(cat "$pw_file")"

  # Create DB and user. Privileges are scoped to the panel's own DB — the
  # panel user has no rights over customer-hosted databases that will live
  # on the same MariaDB instance.
  mariadb -e "
    CREATE DATABASE IF NOT EXISTS \`${db_name}\`
      CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci;
    CREATE USER IF NOT EXISTS '${db_user}'@'localhost' IDENTIFIED BY '${db_pass}';
    ALTER USER '${db_user}'@'localhost' IDENTIFIED BY '${db_pass}';
    GRANT SELECT, INSERT, UPDATE, DELETE, CREATE, DROP, INDEX, ALTER,
          REFERENCES, LOCK TABLES, TRIGGER
      ON \`${db_name}\`.* TO '${db_user}'@'localhost';
    FLUSH PRIVILEGES;
  "

  # Expose the DSN via /etc/jabali/panel.env so the service picks it up.
  # M25 Step 6: switch from TCP loopback (127.0.0.1:3306) to MariaDB's
  # Debian-default Unix socket /var/run/mysqld/mysqld.sock. The
  # @unix(...) form is the native go-sql-driver/mysql syntax — no
  # mysql:// URL prefix needed (and prefix would break: net/url.Parse
  # rejects parens in host). Existing dsn.go ToDriverDSN passes native
  # form through unchanged.
  #
  # `skip-networking` now drops in via install_mariadb_skip_networking()
  # below — every MariaDB consumer on this host (panel-api, Kratos,
  # pdns, phpMyAdmin SSO) has been flipped to /var/run/mysqld/mysqld.sock.
  # The my.cnf knob closes TCP :3306 entirely; the unix socket remains
  # the sole ingress. Rollback: remove the drop-in file and restart.
  local dsn="${db_user}:${db_pass}@unix(/var/run/mysqld/mysqld.sock)/${db_name}?parseTime=true&charset=utf8mb4&loc=UTC"

  # Rewrite the line without sed (DSNs contain `&` which sed would expand
  # as the matched text). We strip any existing DATABASE_URL line and
  # append a fresh one.
  local tmp
  tmp="$(mktemp --tmpdir jabali-env.XXXXXX)"
  grep -v '^DATABASE_URL=' "$ENV_FILE" >"$tmp" || true
  echo "DATABASE_URL=${dsn}" >>"$tmp"
  install -m 0640 -o root -g "$SERVICE_USER" "$tmp" "$ENV_FILE"
  rm -f "$tmp"

  _ok "MariaDB provisioned: DB=${db_name}, user=${db_user}"
}

# ---------- step 2.5b: MariaDB skip-networking (M25.1) ----------------------
#
# Drops a tiny drop-in conf that tells mariadbd NOT to bind TCP :3306.
# Everything on this host — panel-api, Kratos, pdns, phpMyAdmin SSO —
# already connects via /var/run/mysqld/mysqld.sock. Closing the TCP
# listener shrinks the attack surface to the unix socket's POSIX ACL.
#
# Why a drop-in under mariadb.conf.d/ rather than editing 50-server.cnf:
# 50-server.cnf is package-owned. Every apt upgrade that ships a new
# 50-server.cnf would either clobber our edit or prompt dpkg for
# manual resolution. Drop-ins survive upgrades unchanged.
#
# Rollback (if some future service needs TCP again): `trash
# /etc/mysql/mariadb.conf.d/99-jabali-skip-networking.cnf && systemctl
# restart mariadb`. That re-opens :3306 without touching the
# package-owned config.
install_mariadb_skip_networking() {
  local dropin="/etc/mysql/mariadb.conf.d/99-jabali-skip-networking.cnf"
  local desired=$'# Managed by jabali install.sh — M25.1. Do NOT hand-edit.\n# Closes MariaDB TCP :3306; every consumer on this host reaches MariaDB\n# via /var/run/mysqld/mysqld.sock. Removing this file re-opens TCP.\n[mysqld]\nskip-networking\n'

  if [[ -f "$dropin" ]] && cmp -s <(printf '%s' "$desired") "$dropin"; then
    _log "MariaDB skip-networking drop-in already current"
    return
  fi

  _log "installing MariaDB skip-networking drop-in → $dropin"
  local tmp
  tmp="$(mktemp --tmpdir jabali-mariadb-dropin.XXXXXX)"
  printf '%s' "$desired" >"$tmp"
  install -m 0644 -o root -g root "$tmp" "$dropin"
  rm -f "$tmp"

  systemctl restart mariadb

  # Wait for socket to come back up before returning — downstream
  # steps (kratos migrations, pdns start) will fail if we race the
  # restart.
  local i
  for i in 1 2 3 4 5 6 7 8 9 10; do
    if mariadb -e 'SELECT 1' >/dev/null 2>&1; then break; fi
    sleep 1
  done
  if ! mariadb -e 'SELECT 1' >/dev/null 2>&1; then
    _die "MariaDB did not come back up after skip-networking drop-in; rollback: trash $dropin && systemctl restart mariadb"
  fi

  # Defensive: verify :3306 is actually gone. If mariadb ignored the
  # drop-in (wrong path, syntax error), fail loud rather than silently
  # leaving an open TCP port.
  if ss -tlnH 'sport = :3306' | grep -q LISTEN; then
    _die "MariaDB still LISTENs on :3306 after drop-in — skip-networking did not take effect"
  fi
  _ok "MariaDB TCP :3306 closed (skip-networking active)"
}

# ---------- step 2.5: Redis (notification dispatcher + future WP cache) ------
#
# ADR-0056 + ADR-0059. Unix-socket-only Redis at /run/redis/redis.sock,
# mode 0660, group jabali-sockets (same pattern as every other service
# under ADR-0050). AOF on (dispatcher queue survives restart).
# 128 MB maxmemory with allkeys-lru (safe for both dispatcher queue
# and future WP object-cache).
#
# db 0 → panel-api notification dispatcher
# db 1 → reserved for future WordPress object-cache
#
# Package redis-server is installed in install_base_packages' one-shot
# apt batch; this runs post-install config only.
install_redis() {
  _log "configuring Redis (package installed in base batch; this runs post-install config)"

  if ! command -v redis-cli >/dev/null 2>&1; then
    _die "redis-cli binary not found — install_base_packages should have installed redis-server + redis-tools"
  fi

  # Debian's /etc/redis/redis.conf ends with `include /etc/redis/redis.conf.d/*.conf`
  # from redis 7.x. Verify presence + patch once if the distro variant
  # doesn't ship that include — defensive rather than clever.
  local main_conf="/etc/redis/redis.conf"
  if [[ ! -f "$main_conf" ]]; then
    _die "$main_conf missing — redis-server install did not create the config"
  fi
  if ! grep -qE '^[[:space:]]*include[[:space:]]+/etc/redis/redis\.conf\.d/\*\.conf' "$main_conf"; then
    _log "patching $main_conf to include /etc/redis/redis.conf.d/*.conf"
    printf '\n# Added by jabali install.sh — load drop-ins.\ninclude /etc/redis/redis.conf.d/*.conf\n' >> "$main_conf"
  fi

  install -d -m 0755 -o root -g root /etc/redis/redis.conf.d

  local dropin="/etc/redis/redis.conf.d/10-jabali-socket.conf"
  local desired=$'# Managed by jabali install.sh — M14 / ADR-0059. Do NOT hand-edit.\n# Unix socket only; no TCP listener (port 0 disables TCP). Mode 0660 with\n# group jabali-sockets lets panel-api + future WP-cache clients\n# connect while keeping the socket out of reach of unrelated users.\nport 0\nunixsocket /run/redis/redis.sock\nunixsocketperm 660\n\n# Persistence: AOF on with everysec fsync. Lets the notification\n# dispatcher queue survive systemctl restart redis-server.\nappendonly yes\nappendfsync everysec\n\n# Bounded memory with safe eviction. 128MB is the starting floor; WP\n# cache load may warrant a higher-numbered drop-in later.\nmaxmemory 128mb\nmaxmemory-policy allkeys-lru\n'

  local restart_needed=0
  if [[ ! -f "$dropin" ]] || ! cmp -s <(printf '%s' "$desired") "$dropin"; then
    _log "installing Redis drop-in → $dropin"
    local tmp
    tmp="$(mktemp --tmpdir jabali-redis-dropin.XXXXXX)"
    printf '%s' "$desired" >"$tmp"
    install -m 0644 -o root -g root "$tmp" "$dropin"
    rm -f "$tmp"
    restart_needed=1
  else
    _log "Redis drop-in already current"
  fi

  # systemd drop-in: RuntimeDirectory=redis gives Redis its own
  # /run/redis/ on every boot. Belt-and-suspenders chmod/chgrp match
  # ADR-0050 F-C-3. SupplementaryGroups=jabali-sockets so Redis can
  # set group-write on the socket file when it creates it (primary
  # group stays `redis` for AOF file permissions).
  install -d -m 0755 -o root -g root /etc/systemd/system/redis-server.service.d
  local unit_dropin="/etc/systemd/system/redis-server.service.d/10-jabali-socket.conf"
  # Flip the service's primary Group= from stock `redis` to
  # `jabali-sockets`. This cascades cleanly:
  #   - systemd creates /run/redis as redis:jabali-sockets (matching the
  #     service's User:Group), mode 0750 → redis owner rwx, jabali-
  #     sockets members rx (traverse), others blocked. panel-api (uid
  #     jabali, gid jabali-sockets) can open the dir.
  #   - redis process egid = jabali-sockets → the socket it creates
  #     inherits group = jabali-sockets automatically. `unixsocketperm 660`
  #     in the conf drop-in sets the mode. No ExecStartPost chgrp/chmod
  #     dance needed.
  #   - redis still owns /var/lib/redis and /var/log/redis by UID, so
  #     flipping the primary group doesn't break its data-dir access
  #     (file access on owner-match ignores egid).
  # Earlier iterations tried ExecStartPost chgrp with the `+` prefix but
  # systemd re-asserts RuntimeDirectory ownership after the hook runs,
  # so the dir reverted to redis:redis every restart. Setting Group=
  # at the service level makes the systemd-managed ownership correct
  # on its own.
  local unit_desired=$'# Managed by jabali install.sh — M14 / ADR-0059. Do NOT hand-edit.\n[Service]\nGroup=jabali-sockets\nRuntimeDirectory=redis\nRuntimeDirectoryMode=0750\n'

  if [[ ! -f "$unit_dropin" ]] || ! cmp -s <(printf '%s' "$unit_desired") "$unit_dropin"; then
    _log "installing Redis systemd drop-in → $unit_dropin"
    local tmp
    tmp="$(mktemp --tmpdir jabali-redis-unit.XXXXXX)"
    printf '%s' "$unit_desired" >"$tmp"
    install -m 0644 -o root -g root "$tmp" "$unit_dropin"
    rm -f "$tmp"
    systemctl daemon-reload
    restart_needed=1
  else
    _log "Redis systemd drop-in already current"
  fi

  # Make sure the jabali-sockets group exists (M25 install creates it,
  # but we may run before that wave on fresh installs if ordering
  # changes in the future). Idempotent.
  if ! getent group jabali-sockets >/dev/null; then
    _log "creating jabali-sockets system group (M25 boundary; ADR-0050)"
    groupadd --system jabali-sockets
  fi

  systemctl enable redis-server >/dev/null 2>&1 || true
  if [[ "$restart_needed" == "1" ]]; then
    systemctl restart redis-server
  else
    systemctl start redis-server
  fi

  # Ping via the socket; fail loud if Redis didn't actually come up on
  # the expected path (wrong config, SELinux, etc.).
  local i
  for i in 1 2 3 4 5 6 7 8 9 10; do
    if redis-cli -s /run/redis/redis.sock ping 2>/dev/null | grep -q PONG; then
      break
    fi
    sleep 1
  done
  if ! redis-cli -s /run/redis/redis.sock ping 2>/dev/null | grep -q PONG; then
    _die "Redis did not respond to PING on /run/redis/redis.sock — check 'journalctl -u redis-server'"
  fi

  # Verify no TCP listener. Same invariant check as MariaDB's
  # skip-networking step — config ingest errors are easier to catch
  # here than debug at runtime.
  if ss -tlnH 'sport = :6379' | grep -q LISTEN; then
    _die "Redis still LISTENs on :6379 — port 0 directive didn't take effect"
  fi

  # Verify socket permissions match ADR-0059 contract.
  local mode owner group
  read -r mode owner group < <(stat -c '%a %U %G' /run/redis/redis.sock)
  if [[ "$mode" != "660" ]]; then
    _warn "Redis socket mode is $mode (expected 660) — ExecStartPost hook may have raced; fix with 'chmod 0660 /run/redis/redis.sock'"
  fi
  if [[ "$group" != "jabali-sockets" ]]; then
    _die "Redis socket group is $group (expected jabali-sockets) — ExecStartPost chgrp did not run"
  fi

  _ok "Redis listening on unix socket /run/redis/redis.sock mode 0660 ${owner}:${group}"
}

# ---------- step 2.6: PowerDNS authoritative nameserver ----------------------

install_powerdns() {
  _log "configuring PowerDNS (packages installed in base batch; this runs post-install config)"

  # pdns-server + pdns-backend-mysql are installed in install_base_packages's
  # one-shot apt batch. The policy-rc.d trap that prevents pdns from
  # auto-starting before its MySQL backend is wired up ALSO lives in
  # install_base_packages — the trap wraps the entire batch so every
  # service defers its start to its own config function (here, for pdns).

  if ! dpkg -s pdns-server >/dev/null 2>&1; then
    _die "pdns-server not installed — install_base_packages should have installed it"
  fi

  # The config directory for our env/cred files must exist before we
  # try to write into it. The panel's own config.toml lives here too;
  # write_config_file would normally create it, but install_powerdns
  # runs first.
  mkdir -p /etc/jabali-panel
  chmod 0755 /etc/jabali-panel

  # The Debian package drops a default /etc/powerdns/pdns.d/*.conf that
  # wires up the bind backend. We don't want that — replace the whole
  # conf directory with our own minimal config pointing at the MySQL
  # backend + our dedicated database.
  local conf_d="/etc/powerdns/pdns.d"
  mkdir -p "$conf_d"
  find "$conf_d" -maxdepth 1 -type f -name '*.conf' -delete

  # Credentials for the pdns DB user. Generated once, stored in
  # /etc/jabali-panel/pdns.env so the panel-api can read the same
  # password when it opens a connection.
  local pdns_env_file="/etc/jabali-panel/pdns.env"
  local pdns_password
  if [[ -f "$pdns_env_file" ]] && grep -q '^PDNS_DB_PASSWORD=' "$pdns_env_file"; then
    pdns_password="$(. "$pdns_env_file"; printf '%s' "$PDNS_DB_PASSWORD")"
    _log "reusing existing PowerDNS DB password from $pdns_env_file"
  else
    pdns_password="$(openssl rand -hex 24)"
    install -m 0640 -o root -g "$SERVICE_USER" /dev/null "$pdns_env_file"
    cat > "$pdns_env_file" <<PDNSEOF
# PowerDNS database credentials. Generated by install.sh.
# Consumed by the panel-api reconciler and by pdns.conf below.
PDNS_DB_NAME=jabali_pdns
PDNS_DB_USER=jabali_pdns
PDNS_DB_PASSWORD=${pdns_password}
PDNSEOF
    chmod 0640 "$pdns_env_file"
    _ok "generated PowerDNS DB password → $pdns_env_file"
  fi

  # Provision the jabali_pdns database + user. Idempotent: CREATE
  # DATABASE IF NOT EXISTS; CREATE USER IF NOT EXISTS.
  mariadb -uroot <<SQL
CREATE DATABASE IF NOT EXISTS jabali_pdns CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci;
CREATE USER IF NOT EXISTS 'jabali_pdns'@'localhost' IDENTIFIED BY '${pdns_password}';
ALTER USER 'jabali_pdns'@'localhost' IDENTIFIED BY '${pdns_password}';
GRANT ALL PRIVILEGES ON jabali_pdns.* TO 'jabali_pdns'@'localhost';
FLUSH PRIVILEGES;
SQL

  # Load PowerDNS's native schema (domains, records, supermasters,
  # comments, domainmetadata, cryptokeys, tsigkeys). File ships with the
  # pdns-backend-mysql package; path has been stable for years.
  local schema_file
  if [[ -f /usr/share/pdns-backend-mysql/schema/schema.mysql.sql ]]; then
    schema_file=/usr/share/pdns-backend-mysql/schema/schema.mysql.sql
  elif [[ -f /usr/share/doc/pdns-backend-mysql/schema.mysql.sql ]]; then
    schema_file=/usr/share/doc/pdns-backend-mysql/schema.mysql.sql
  else
    schema_file="$(find /usr/share -name 'schema.mysql.sql' -path '*pdns*' 2>/dev/null | head -n1)"
  fi
  if [[ -z "$schema_file" || ! -f "$schema_file" ]]; then
    _die "can't find PowerDNS MySQL schema; aborting. Check pdns-backend-mysql install."
  fi

  # Only load if the domains table isn't already present (idempotent).
  local table_exists
  table_exists="$(mariadb -uroot -Ns -e "SELECT COUNT(*) FROM information_schema.tables WHERE table_schema='jabali_pdns' AND table_name='domains';")"
  if [[ "$table_exists" == "0" ]]; then
    _log "loading PowerDNS schema from $schema_file"
    mariadb -uroot jabali_pdns < "$schema_file"
    _ok "PowerDNS schema loaded"
  else
    _log "PowerDNS schema already present in jabali_pdns — skipping reload"
  fi

  # Write pdns.conf. Single file, minimal surface.
  #
  # Idempotency (M6.3): render to $pdns_conf.new first; if byte-identical
  # to the live file, skip the write + service restart. This keeps a
  # re-run of install.sh on an already-converged host silent (no DNS
  # service bounce, no config rewrite, journal clean).
  local pdns_conf=/etc/powerdns/pdns.d/01-jabali-mysql.conf
  local pdns_conf_new="${pdns_conf}.new"

  # Enumerate every global-scope IPv4 + IPv6 on the host. 'scope global'
  # automatically excludes 127.0.0.0/8 (which is host scope) and
  # fe80::/10 (link scope). Sorted for deterministic output so the .new
  # vs live file comparison stays stable across re-runs that didn't
  # actually change the IP layout. IPv6 addresses get bracketed for
  # pdns's "addr:port" parser.
  local pdns_local_addresses
  # paste -sd takes one char per separator + cycles when given more,
  # so use a single comma here and let pdns trim whitespace itself.
  pdns_local_addresses="$({
    ip -4 -o addr show scope global 2>/dev/null \
      | awk '{split($4,a,"/"); print a[1] ":53"}'
    ip -6 -o addr show scope global 2>/dev/null \
      | awk '{split($4,a,"/"); print "[" a[1] "]:53"}'
    printf '127.0.0.1:5300\n[::1]:5300\n'
  } | sort -u | paste -sd ',' -)"
  if [[ -z "$pdns_local_addresses" ]]; then
    # Defensive: should never happen because the loopback entries are
    # always emitted, but guard against an unexpected empty value
    # producing an invalid pdns.conf line.
    _die "pdns local-address enumeration produced empty string"
  fi
  cat > "$pdns_conf_new" <<PDNSCONF
# Managed by Jabali Panel install.sh. Hand edits will be overwritten
# the next time install.sh runs.
launch=gmysql
# M25 Step 6: dial MariaDB over its Debian-default Unix socket. PDNS's
# gmysql backend honors gmysql-socket for client-mode connections; when
# set, host/port are ignored. Lower latency than TCP loopback and
# (post-skip-networking, M25.1) the only available path. Keeping
# host/port out entirely is intentional — having both sometimes confuses
# packagings that don't try the socket first.
gmysql-socket=/var/run/mysqld/mysqld.sock
gmysql-dbname=jabali_pdns
gmysql-user=jabali_pdns
gmysql-password=${pdns_password}

# DNSSEC (M15, ADR-0057): enable the gmysql-backed DNSSEC data path.
# Without this, pdnsutil secure-zone errors out with "no DNSSEC capable
# backends". The schema already provisions cryptokeys / tsigkeys /
# domainmetadata tables for exactly this path.
gmysql-dnssec=yes

# Split-port binding (M6.3, ADR-0047): port 53 on the host's public IP
# keeps serving authoritative queries from the open internet, while
# loopback moves to port 5300 — reserved for pdns-recursor to forward
# local queries into. pdns-recursor owns 127.0.0.1:53 + [::1]:53, which
# is what systemd-resolved points at via zz-jabali-recursor.conf.
#
# Every entry lists port explicitly. pdns-server defaults local-port to
# 53 when unspecified — DO NOT rely on that default here; a future
# port flip would otherwise break silently on only part of the binds.
# Syntax pinned to pdns-server 4.9+ (Debian 13 default package).
#
# We deliberately do NOT bind 0.0.0.0: systemd-resolved's stub listens
# on 127.0.0.53:53 and recursor listens on 127.0.0.1:53, so any
# wildcard bind would collide with one of them (EADDRINUSE). Instead
# enumerate every global-scope IP on the host (IPv4 + IPv6) at install
# time and bind each on :53 explicitly. fe80::/10 + 127/8 + ::1 are
# excluded by 'scope global'. Operators who add IPs after install
# re-run install.sh (or jabali update -f triggers it) to widen.
local-address=${pdns_local_addresses}

# socket-dir is intentionally not set — Debian's pdns.service has
# RuntimeDirectory=powerdns which auto-creates /run/powerdns with the
# right ownership. Overriding socket-dir here collides with pdns's own
# attempt to create the directory (it fails under LXC drop-ins).

# Per-zone AXFR allow-lists and NOTIFY targets are managed via the
# panel's domainmetadata table (ALLOW-AXFR-FROM and ALSO-NOTIFY kinds).
# The global allow-axfr-ips is left empty — PowerDNS denies AXFR by
# default, and per-zone metadata takes precedence.
disable-axfr-rectify=no
PDNSCONF

  # Idempotency: if .new matches live, skip write + restart.
  local pdns_changed=0
  if [[ -f "$pdns_conf" ]] && cmp -s "$pdns_conf" "$pdns_conf_new"; then
    rm -f "$pdns_conf_new"
    _log "pdns.conf already current; skipping write + restart"
  else
    mv "$pdns_conf_new" "$pdns_conf"
    chmod 0640 "$pdns_conf"
    chown root:pdns "$pdns_conf" 2>/dev/null || true
    pdns_changed=1
  fi

  systemctl enable pdns >/dev/null 2>&1 || true
  if [[ "$pdns_changed" == "1" ]]; then
    _log "restarting pdns (config changed)"
    systemctl restart pdns
    # Quick sanity probe — if pdns isn't running after restart something
    # is broken and install.sh should fail fast rather than continue past it.
    sleep 2
    if ! systemctl is-active --quiet pdns; then
      systemctl status pdns --no-pager || true
      _die "pdns failed to start; check 'journalctl -u pdns' for details"
    fi
  elif ! systemctl is-active --quiet pdns; then
    # Unchanged config but service isn't running (crashed, disabled, etc.).
    # Start it without a restart so we don't bounce a working service, but
    # do converge the "service should be active" invariant.
    _log "pdns config unchanged but service inactive — starting"
    systemctl start pdns
    sleep 2
    systemctl is-active --quiet pdns \
      || _die "pdns failed to start; check 'journalctl -u pdns' for details"
  fi
  _ok "PowerDNS running on ${pdns_local_addresses} (authoritative + recursor forward target on :5300)"
}

# ---------- step 2.6c: pdns-recursor for local self-resolution (M6.3) -------
#
# pdns-recursor owns loopback :53 (both v4+v6). It forwards authoritative
# zones that the panel owns into pdns-server at 127.0.0.1:5300 via
# /etc/powerdns/recursor.forwards (one line per zone, reconciler-owned),
# and recurses everything else to public upstream (1.1.1.1, 9.9.9.9).
#
# systemd-resolved's stub at 127.0.0.53:53 forwards into this recursor via
# the zz-jabali-recursor.conf drop-in (written below). Net effect: every
# /etc/resolv.conf-based resolver call (glibc NSS, every app) goes
# stub → recursor → either authoritative (for panel zones) or public
# (for everything else).
#
# Security: allow-from is explicitly loopback-only. Debian's package
# default (127.0.0.0/8 + RFC1918) would open the resolver to every
# container on an LXC bridge. install.sh hard-fails if the rendered
# local-address or allow-from drift away from loopback.
#
# See docs/adr/0047-pdns-recursor-local-self-resolution.md for the full
# decision record and plans/m6.3-pdns-recursor.md for the plan.
install_pdns_recursor() {
  _log "configuring pdns-recursor"

  # Package must already be installed via install_base_packages' apt batch.
  # (The getent-group hard-fail earlier catches postinst failures.)
  if ! dpkg -s pdns-recursor >/dev/null 2>&1; then
    _die "pdns-recursor not installed — install_base_packages should have installed it"
  fi

  # --- recursor.conf ------------------------------------------------
  local rec_conf=/etc/powerdns/recursor.conf
  local rec_conf_new="${rec_conf}.new"

  # Managed-header in line 1 is the "did install.sh write this?" marker
  # downstream idempotency guards test against.
  cat > "$rec_conf_new" <<'RECCONF'
# Managed by jabali-panel install.sh (M6.3). Hand edits will be overwritten
# on the next install.sh run. See docs/adr/0047-pdns-recursor-local-self-resolution.md
local-address=127.0.0.1, ::1
local-port=53

# Amplification defense (ADR-0047): loopback-only. NARROWER than the
# Debian package default (127.0.0.0/8 + RFC1918) because LXC bridge
# interfaces live in RFC1918 ranges and the default would expose the
# resolver to every co-tenant container.
allow-from=127.0.0.0/8, ::1/128

# Per-zone forward-to-authoritative file. One line per zone, format:
#   <zone>=127.0.0.1:5300
# Reconciler-owned via panel-agent's pdns.recursor_add_zone /
# pdns.recursor_remove_zone commands (atomic write + strict validator
# + rec_control reload-zones + SOA post-probe + rollback-on-fail).
# Never hand-edit on a live host; use `jabali pdns backfill --yes`
# if a stale state needs reconverging.
forward-zones-file=/etc/powerdns/recursor.forwards

# Everything else recurses through public upstream. We DO NOT chain
# through jabali.conf's DNS= — that config lives in systemd-resolved
# and is only consulted by the stub, not by the recursor.
forward-zones-recurse=.=1.1.1.1;9.9.9.9

# DNSSEC: off. systemd-resolved validates DNSSEC upstream already.
# Doubling up costs CPU per query for no security benefit on a
# single-host panel.
dnssec=off

# Conservative defaults for a single-tenant panel. max-cache-entries
# tuned to 50000 to hold a few thousand domains' worth of NXDOMAIN
# + short-TTL answers without thrashing.
threads=2
max-cache-entries=50000
quiet=yes
loglevel=4
setuid=pdns
setgid=pdns
RECCONF

  # Pre-install validator (hard-fail): confirm the just-rendered
  # local-address + allow-from are loopback-only. This is a defense
  # against someone (operator, future install.sh edit, merge accident)
  # widening the bind without realizing the blast radius.
  local rec_local_address rec_allow_from
  rec_local_address="$(awk -F= '/^local-address=/{print $2; exit}' "$rec_conf_new" | tr -d '[:space:]')"
  rec_allow_from="$(awk -F= '/^allow-from=/{print $2; exit}' "$rec_conf_new" | tr -d '[:space:]')"
  # Split on commas and verify every entry.
  local IFS_save="$IFS"
  IFS=','
  local addr
  for addr in $rec_local_address; do
    case "$addr" in
      127.0.0.1|::1) : ;;
      *) IFS="$IFS_save"; _die "recursor.conf local-address contains non-loopback '$addr' — would expose open resolver publicly" ;;
    esac
  done
  for addr in $rec_allow_from; do
    case "$addr" in
      127.0.0.0/8|"::1/128") : ;;
      *) IFS="$IFS_save"; _die "recursor.conf allow-from contains non-loopback CIDR '$addr' — amplification defense breach" ;;
    esac
  done
  IFS="$IFS_save"

  # Idempotency: skip rewrite + restart if live file is byte-identical.
  local recursor_changed=0
  if [[ -f "$rec_conf" ]] && cmp -s "$rec_conf" "$rec_conf_new"; then
    rm -f "$rec_conf_new"
    _log "recursor.conf already current; skipping write + restart"
  else
    mv "$rec_conf_new" "$rec_conf"
    chmod 0644 "$rec_conf"
    chown root:root "$rec_conf"
    recursor_changed=1
  fi

  # --- recursor.forwards (seed empty; reconciler owns content) ------
  # Group `pdns` (not `pdns-recursor`) — the recursor process runs as
  # `pdns:pdns` per setuid/setgid in recursor.conf above, matching the
  # Debian package's user model (pdns-server and pdns-recursor share
  # the `pdns` account — pdns-recursor does NOT create its own user).
  local rec_forwards=/etc/powerdns/recursor.forwards
  if [[ ! -f "$rec_forwards" ]]; then
    install -m 0640 -o root -g pdns /dev/null "$rec_forwards"
    _log "seeded empty /etc/powerdns/recursor.forwards (reconciler populates)"
  fi

  # --- systemd ordering drop-in: pdns-recursor After=pdns ----------
  install -d -m 0755 /etc/systemd/system/pdns-recursor.service.d
  local rec_after_dropin=/etc/systemd/system/pdns-recursor.service.d/10-jabali-after.conf
  local rec_after_dropin_new="${rec_after_dropin}.new"
  cat > "$rec_after_dropin_new" <<'AFTEREOF'
# Managed by jabali-panel install.sh (M6.3, ADR-0047). Ensures
# pdns-recursor doesn't start before pdns-server — recursor forwards
# authoritative zones into pdns:5300, so starting recursor first would
# hit connection-refused until pdns comes up.
[Unit]
After=pdns.service
Wants=pdns.service
AFTEREOF
  if [[ -f "$rec_after_dropin" ]] && cmp -s "$rec_after_dropin" "$rec_after_dropin_new"; then
    rm -f "$rec_after_dropin_new"
  else
    mv "$rec_after_dropin_new" "$rec_after_dropin"
    chmod 0644 "$rec_after_dropin"
    recursor_changed=1
    _log "wrote pdns-recursor.service.d/10-jabali-after.conf"
  fi

  # --- ExecStart override for --enable-old-settings ---------------
  # PowerDNS Recursor 5.2 (Debian 13 trixie ships 5.2.8) flipped the
  # config-file default from old-style `key=value` to YAML. Our
  # recursor.conf above is still key=value, and without this flag the
  # daemon dies at startup with:
  #   error="invalid type: string \"local-address=127.0.0.1, ::1
  #   local-port=53\", expected struct Recursorsettings at line 3"
  #   Old-style settings syntax not enabled by default anymore.
  #   Use YAML or enable with --enable-old-settings on the command line
  # systemd then auto-restarts forever (restart_counter climbs into
  # the thousands), and `systemctl restart` blocks on stabilisation.
  # The flag is the official escape hatch until we do the YAML
  # conversion (tracked as an M6.3 follow-up).
  #
  # Approach: drop-in that blanks + replaces ExecStart (first `=`
  # empties the directive, second `=` sets the replacement — standard
  # systemd override idiom). Source ExecStart copied verbatim from
  # upstream's /usr/lib/systemd/system/pdns-recursor.service plus
  # `--enable-old-settings`.
  local rec_exec_dropin=/etc/systemd/system/pdns-recursor.service.d/20-jabali-old-settings.conf
  local rec_exec_dropin_new="${rec_exec_dropin}.new"
  cat > "$rec_exec_dropin_new" <<'EXECEOF'
# Managed by jabali-panel install.sh (M6.3). pdns-recursor 5.2 made
# YAML the default config format; we still emit old-style key=value
# in /etc/powerdns/recursor.conf. --enable-old-settings keeps the
# old parser until a later M6.3.x converts our config to YAML. See
# docs/adr/0047-pdns-recursor-local-self-resolution.md for context.
[Service]
ExecStart=
ExecStart=/usr/sbin/pdns_recursor --daemon=no --write-pid=no --disable-syslog --log-timestamp=no --enable-old-settings
EXECEOF
  if [[ -f "$rec_exec_dropin" ]] && cmp -s "$rec_exec_dropin" "$rec_exec_dropin_new"; then
    rm -f "$rec_exec_dropin_new"
  else
    mv "$rec_exec_dropin_new" "$rec_exec_dropin"
    chmod 0644 "$rec_exec_dropin"
    recursor_changed=1
    _log "wrote pdns-recursor.service.d/20-jabali-old-settings.conf"
  fi

  # --- zz-jabali-recursor.conf drop-in for systemd-resolved --------
  #
  # Alphabetically AFTER the panel-UI-managed jabali.conf. Per
  # systemd-resolved.conf(5): "Setting this variable to an empty list
  # (as in DNS=) resets the list of servers to the empty list, all prior
  # assignments will be cleared." So `DNS=` (reset) + `DNS=127.0.0.1`
  # makes 127.0.0.1 the only resolver, regardless of what jabali.conf
  # contributed. install.sh gates on resolvectl showing DNS Servers:
  # 127.0.0.1 only — if the merge semantics differ, install fails
  # loudly so the fallback (consolidate into jabali.conf) can be
  # invoked per the M6.3 plan.
  install -d -m 0755 /etc/systemd/resolved.conf.d
  local resolved_dropin=/etc/systemd/resolved.conf.d/zz-jabali-recursor.conf
  local resolved_dropin_new="${resolved_dropin}.new"
  cat > "$resolved_dropin_new" <<'RESOLVEDEOF'
# Managed by jabali-panel install.sh (M6.3, ADR-0047). Do not hand-edit:
# the panel DNS Resolvers UI continues to manage
# /etc/systemd/resolved.conf.d/jabali.conf (admin upstream DNS); this
# file layers on top alphabetically-last to force every /etc/resolv.conf
# query through the local pdns-recursor at 127.0.0.1, which forwards
# panel-authoritative zones to pdns-server:5300 and recurses everything
# else to public upstream.
[Resolve]
DNS=
DNS=127.0.0.1
FallbackDNS=1.1.1.1 9.9.9.9
DNSSEC=no
RESOLVEDEOF

  local resolved_changed=0
  if [[ -f "$resolved_dropin" ]] && cmp -s "$resolved_dropin" "$resolved_dropin_new"; then
    rm -f "$resolved_dropin_new"
  else
    mv "$resolved_dropin_new" "$resolved_dropin"
    chmod 0644 "$resolved_dropin"
    chown root:root "$resolved_dropin"
    resolved_changed=1
    _log "wrote resolved.conf.d/zz-jabali-recursor.conf"
  fi

  # --- systemd-resolved After=pdns-recursor drop-in ----------------
  install -d -m 0755 /etc/systemd/system/systemd-resolved.service.d
  local resolved_after_dropin=/etc/systemd/system/systemd-resolved.service.d/10-jabali-after.conf
  local resolved_after_dropin_new="${resolved_after_dropin}.new"
  cat > "$resolved_after_dropin_new" <<'RESAFTEREOF'
# Managed by jabali-panel install.sh (M6.3, ADR-0047). Ensures
# systemd-resolved doesn't start before pdns-recursor — the stub
# at 127.0.0.53 forwards to 127.0.0.1:53 (recursor), so a live stub
# with a dead recursor means every DNS query SERVFAILs.
[Unit]
After=pdns-recursor.service
Wants=pdns-recursor.service
RESAFTEREOF
  if [[ -f "$resolved_after_dropin" ]] && cmp -s "$resolved_after_dropin" "$resolved_after_dropin_new"; then
    rm -f "$resolved_after_dropin_new"
  else
    mv "$resolved_after_dropin_new" "$resolved_after_dropin"
    chmod 0644 "$resolved_after_dropin"
    resolved_changed=1
    _log "wrote systemd-resolved.service.d/10-jabali-after.conf"
  fi

  # --- daemon-reload only if ordering drop-ins changed --------------
  if [[ "$recursor_changed" == "1" || "$resolved_changed" == "1" ]]; then
    systemctl daemon-reload
  fi

  # --- start + restart sequence ------------------------------------
  # Order: recursor FIRST (so resolved has something to forward to).
  # Use restart-if-changed / start-if-inactive, never a blind restart.
  #
  # Every systemctl call below is wrapped in `timeout 30` — bare
  # systemctl restart BLOCKS until the unit stabilises, which means a
  # bad config + Restart=on-failure loop (as happened with the 5.2
  # YAML-default incident) hangs the install script indefinitely
  # instead of surfacing the error. 30s is well above any legitimate
  # start time for these units; if we hit it, something is wrong and
  # we dump the journal + _die so the operator sees the real cause.
  timeout 30 systemctl enable pdns-recursor >/dev/null 2>&1 || true
  if [[ "$recursor_changed" == "1" ]]; then
    _log "restarting pdns-recursor (config changed)"
    timeout 30 systemctl restart pdns-recursor \
      || { journalctl -u pdns-recursor --no-pager -n 50 >&2
           _die "pdns-recursor restart failed or timed out (30s); see journal above"; }
  elif ! systemctl is-active --quiet pdns-recursor; then
    _log "starting pdns-recursor (was inactive)"
    timeout 30 systemctl start pdns-recursor \
      || { journalctl -u pdns-recursor --no-pager -n 50 >&2
           _die "pdns-recursor start failed or timed out (30s); see journal above"; }
  fi
  sleep 1
  systemctl is-active --quiet pdns-recursor \
    || { journalctl -u pdns-recursor --no-pager -n 50 >&2; _die "pdns-recursor failed to start; see journal above"; }

  if [[ "$resolved_changed" == "1" ]]; then
    _log "restarting systemd-resolved (drop-in changed)"
    timeout 30 systemctl restart systemd-resolved \
      || { journalctl -u systemd-resolved --no-pager -n 50 >&2
           _die "systemd-resolved restart failed or timed out (30s); see journal above"; }
    sleep 1
    systemctl is-active --quiet systemd-resolved \
      || { journalctl -u systemd-resolved --no-pager -n 50 >&2
           _die "systemd-resolved failed to restart; see journal above"; }
  fi

  # --- post-install probe matrix -----------------------------------
  # Fail the install rather than shipping a half-working DNS chain.
  #
  # Probes retry with backoff: a freshly-restarted pdns-recursor's first
  # recursive query can take several seconds (cold cache, root hint
  # fetch, upstream round-trip to 1.1.1.1). A single 3s shot is brittle
  # — legitimate cold starts were flunking the probe. 8 tries × 2s
  # backoff covers ~15s of startup cost while still failing loud if
  # the chain is genuinely broken.
  _probe_dns() {
    local addr="$1" host="$2" attempt
    for attempt in 1 2 3 4 5 6 7 8; do
      if dig +short +timeout=3 +tries=1 "@${addr}" "$host" 2>/dev/null | grep -qE '^[0-9.]+$'; then
        return 0
      fi
      sleep 2
    done
    return 1
  }

  # Probe 1: stub → recursor → public. Proves the full chain end-to-end.
  if ! _probe_dns 127.0.0.53 deb.debian.org; then
    _die "resolved→recursor→public chain broken (dig @127.0.0.53 deb.debian.org failed after 8 retries). Check 'journalctl -u pdns-recursor -u systemd-resolved'."
  fi

  # Probe 2: recursor → public directly. Isolates recursor from stub.
  if ! _probe_dns 127.0.0.1 deb.debian.org; then
    _die "recursor→public chain broken (dig @127.0.0.1 deb.debian.org failed after 8 retries). Check recursor logs."
  fi

  # Probe 3: drop-in merge sanity. resolvectl MUST show DNS Servers:
  # with 127.0.0.1 only. If jabali.conf's DNS=1.1.1.1 9.9.9.9 bleeds
  # through, the man-page claim about DNS= reset semantics doesn't hold
  # on this system — abort so the operator can switch to the fallback
  # (consolidate jabali.conf into zz-jabali-recursor.conf) per the
  # M6.3 plan.
  # The `|| true` suffixes below defend against a set -e + pipefail + SIGPIPE
  # interaction: awk's `exit` closes stdout before resolvectl finishes writing
  # its multi-line status block, resolvectl is SIGPIPE'd (exit 141), pipefail
  # surfaces 141, and — because these are bare assignments, not `local var=…`
  # which would mask the command-substitution exit — set -e kills the script
  # silently (no _die, no _ok, no trap output). Saw it on 10.0.3.13 with
  # systemd 257 / resolvectl ~258.3. The `|| true` keeps the assignment
  # happy; the subsequent `case` on $dns_servers is the real gate.
  local dns_servers
  dns_servers="$(resolvectl status 2>/dev/null | awk '/^ *DNS Servers:/{sub(/^ *DNS Servers: */,""); print; exit}')" || true
  if [[ -z "$dns_servers" ]]; then
    # Older systemd: "Current DNS Server:" one-liner, or global-only view.
    # Fall back to `resolvectl dns` which returns the merged list.
    dns_servers="$(resolvectl dns 2>/dev/null | awk '/^Global:/{print $2; exit}')" || true
  fi
  # Accept "127.0.0.1" exactly, OR "127.0.0.1 127.0.0.1" (some systemd
  # versions list per-interface views that repeat the loopback line).
  # Reject anything else — any 1.1.1.1 / 9.9.9.9 / interface-upstream
  # in the DNS Servers line means our reset didn't take globally.
  case "$dns_servers" in
    "127.0.0.1"|"127.0.0.1 127.0.0.1") : ;;
    *) _die "resolvectl shows DNS Servers='$dns_servers' — expected '127.0.0.1' only. zz-jabali-recursor.conf merge isn't producing the expected state; see plans/m6.3-pdns-recursor.md §Step 2 fallback (consolidate jabali.conf into zz-jabali-recursor.conf; panel UI edits FallbackDNS= instead of DNS=)." ;;
  esac

  _ok "pdns-recursor running on 127.0.0.1:53 — stub + recursor + public chain verified"
}

# ---------- step 2.6b: bootstrap the panel's own hostname zone --------------
#
# User domains created via the panel declare ns1.<hostname> / ns2.<hostname>
# as their authoritative nameservers. For anyone delegating to those NS
# records to actually reach our PowerDNS, PowerDNS must be authoritative
# for <hostname> itself — otherwise `host ns1.<hostname>` returns REFUSED
# and the whole DNS infrastructure is broken from day one.
#
# We create the zone exactly once at install time with the minimum record
# set PowerDNS needs to serve itself: SOA, NS×2, A for the hostname, and
# A for each NS name. On subsequent install.sh runs the zone is left
# alone — an admin may have edited it via the panel UI and re-installing
# shouldn't clobber their customizations. To refresh defaults, delete the
# zone manually and re-run install.sh.
#
# We use direct SQL INSERTs rather than a `jabali` CLI call because this
# phase runs before build_backend — there's no jabali binary yet. The
# PDNS schema has been stable for years; the column set here matches
# what panel-agent/internal/pdns/client.go upserts for user domains.
bootstrap_pdns_self_zone() {
  local hostname="$JABALI_SRV_HOSTNAME"
  local ipv4="$JABALI_SRV_IPV4"
  local ipv6="${JABALI_SRV_IPV6:-}"
  local ns1_name="$JABALI_SRV_NS1_NAME"
  local ns1_ipv4="$JABALI_SRV_NS1_IPV4"
  local ns2_name="$JABALI_SRV_NS2_NAME"
  local ns2_ipv4="$JABALI_SRV_NS2_IPV4"

  if [[ -z "$hostname" || -z "$ipv4" || -z "$ns1_name" || -z "$ns2_name" ]]; then
    _warn "bootstrap_pdns_self_zone: server settings env vars missing; skipping"
    return 0
  fi

  # Sanity-warn (don't fail) on non-routable identities. An admin running
  # a lab/dev install with hostname=jabali-panel.local gets a working
  # PDNS but the NS delegation will only work on hosts that explicitly
  # resolve through this PDNS — it won't work from public resolvers.
  case "$hostname" in
    *.local|*.localdomain|localhost)
      _warn "hostname '$hostname' ends in a non-routable TLD — public NS delegation will not work"
      ;;
  esac
  if [[ "$ipv4" =~ ^(10\.|172\.(1[6-9]|2[0-9]|3[01])\.|192\.168\.|127\.) ]]; then
    _warn "IPv4 '$ipv4' is a private/loopback range — public NS delegation will not reach this host"
  fi

  # Idempotent check: if the domain row exists, leave everything alone.
  local existing_id
  existing_id="$(mariadb -uroot -Ns jabali_pdns -e \
    "SELECT id FROM domains WHERE name = '$(_sql_escape "$hostname")';" 2>/dev/null || true)"
  if [[ -n "$existing_id" ]]; then
    _log "self-zone '$hostname' already exists in jabali_pdns (id=$existing_id); leaving untouched"
    return 0
  fi

  _log "bootstrapping PowerDNS self-zone '$hostname' (SOA + NS + A × 3${ipv6:+ + AAAA × 3})"

  # Build the SQL as a heredoc. We can't interpolate arbitrary admin
  # input directly into SQL without escaping, but these values came from
  # prompt_server_settings which validates them as RFC-1123 hostnames /
  # IP addresses. Still, run each through _sql_escape as defense in depth.
  local h_esc ipv4_esc ns1_esc ns1_ipv4_esc ns2_esc ns2_ipv4_esc ipv6_esc
  h_esc="$(_sql_escape "$hostname")"
  ipv4_esc="$(_sql_escape "$ipv4")"
  ns1_esc="$(_sql_escape "$ns1_name")"
  ns1_ipv4_esc="$(_sql_escape "$ns1_ipv4")"
  ns2_esc="$(_sql_escape "$ns2_name")"
  ns2_ipv4_esc="$(_sql_escape "$ns2_ipv4")"
  ipv6_esc="$(_sql_escape "$ipv6")"

  # SOA content: primary-ns hostmaster.<hostname> serial refresh retry expire minimum
  # Matches RFC 1035 SOA RDATA; 300s min TTL for faster negative caching recovery.
  local soa_content="$ns1_esc hostmaster.$h_esc 1 86400 7200 604800 300"

  mariadb -uroot jabali_pdns <<SQL
INSERT INTO domains (name, type) VALUES ('$h_esc', 'NATIVE');
SET @zid = LAST_INSERT_ID();
INSERT INTO records (domain_id, name, type, content, ttl, prio, disabled, auth) VALUES
  (@zid, '$h_esc',     'SOA', '$soa_content', 3600, 0, 0, 1),
  (@zid, '$h_esc',     'NS',  '$ns1_esc',     3600, 0, 0, 1),
  (@zid, '$h_esc',     'NS',  '$ns2_esc',     3600, 0, 0, 1),
  (@zid, '$h_esc',     'A',   '$ipv4_esc',    300,  0, 0, 1),
  (@zid, '$ns1_esc',   'A',   '$ns1_ipv4_esc',300,  0, 0, 1),
  (@zid, '$ns2_esc',   'A',   '$ns2_ipv4_esc',300,  0, 0, 1);
SQL

  # AAAA records only if IPv6 is configured. Separate statement so the
  # common IPv4-only case doesn't pay for a conditional in the heredoc.
  if [[ -n "$ipv6" ]]; then
    mariadb -uroot jabali_pdns <<SQL
SET @zid = (SELECT id FROM domains WHERE name = '$h_esc');
INSERT INTO records (domain_id, name, type, content, ttl, prio, disabled, auth) VALUES
  (@zid, '$h_esc',   'AAAA', '$ipv6_esc', 300, 0, 0, 1),
  (@zid, '$ns1_esc', 'AAAA', '$ipv6_esc', 300, 0, 0, 1),
  (@zid, '$ns2_esc', 'AAAA', '$ipv6_esc', 300, 0, 0, 1);
SQL
  fi

  # Tell pdns to drop its cache for this zone so subsequent queries see
  # the new records immediately. NOTIFY also pings any configured slaves;
  # with type=NATIVE and no slaves configured, this is a pure cache poke.
  # Ignore exit — pdns_control may not be on PATH on minimal Debian
  # installs, and the SQL rows are committed either way; the next
  # scheduled reload (or pdns restart) will pick them up.
  pdns_control notify "$hostname" >/dev/null 2>&1 || true

  _ok "self-zone '$hostname' created in jabali_pdns"
}

# install_panel_primary_domain auto-registers the panel hostname as a
# first-class email-enabled domain so `/webmail` bounces to a working
# Bulwark instance on fresh installs and `admin@<hostname>` is a viable
# mailbox. The real work — idempotent INSERT/UPDATE/no-op decision tree,
# ULID generation, at-most-one enforcement — is in the Go CLI
# (`jabali-panel panel-primary ensure`). install.sh only has to:
#   1. Verify hostname is set (fatal if not — no working mail without it).
#   2. Verify the pdns self-zone exists (FK assertion — reconciler later
#      writes DNS records scoped to this zone).
#   3. Invoke the CLI.
#
# See ADR-0048 for the design rationale and plans/m6.4-panel-hostname-
# mail-domain.md Step 3 for the full task list.
install_panel_primary_domain() {
  if [[ -z "${JABALI_SRV_HOSTNAME:-}" ]]; then
    _die "JABALI_SRV_HOSTNAME not set — cannot configure panel primary mail domain"
  fi

  # Self-zone FK assertion. `install_panel_primary_domain` inserts rows
  # into jabali_panel.domains, and the reconciler will later insert DNS
  # records into jabali_pdns that reference a zone keyed by hostname.
  # If bootstrap_pdns_self_zone hasn't run, those later inserts fail at
  # reconciler tick time with FK violations. Catch it here, not there.
  local pdns_zone_id
  pdns_zone_id="$(mariadb -uroot -Ns jabali_pdns -e \
    "SELECT id FROM domains WHERE name = '$(_sql_escape "$JABALI_SRV_HOSTNAME")';" 2>/dev/null || true)"
  if [[ -z "$pdns_zone_id" ]]; then
    _die "pdns self-zone '$JABALI_SRV_HOSTNAME' not found — bootstrap_pdns_self_zone must run before install_panel_primary_domain; check main() ordering"
  fi

  _log "ensuring panel-primary domain row for $JABALI_SRV_HOSTNAME"
  if "$BIN_PATH" panel-primary ensure --hostname "$JABALI_SRV_HOSTNAME"; then
    _ok "panel-primary domain ensured for $JABALI_SRV_HOSTNAME"
  else
    # Non-fatal — the CLI may defer when no admin user exists yet. That
    # message is already logged by the CLI. On next install.sh run (e.g.
    # after the operator completes admin bootstrap), the CLI will INSERT
    # the row. A hard failure (DB down, config missing) would have
    # returned non-zero; we do NOT _die because defer-on-no-admin is
    # a valid path.
    _warn "panel-primary ensure reported non-success; review output above"
  fi
}

# Minimal SQL string escaper: replaces ' with '' and strips backslashes
# that MariaDB would otherwise interpret in string literals. Not a
# general-purpose escaper — adequate for hostname / IPv4 / IPv6 values
# that have already passed RFC-1123 / netip.ParseAddr validation earlier
# in prompt_server_settings. Defense in depth, not primary trust.
_sql_escape() {
  # shellcheck disable=SC2001
  printf '%s' "$1" | sed -e "s/'/''/g" -e 's/\\//g'
}

# ---------- step 2.7: Certbot (Let's Encrypt SSL) ---------------------------
setup_certbot() {
  _log "configuring Certbot (packages installed in base batch; this runs post-install config)"

  # certbot + python3-certbot-nginx are installed in install_base_packages's
  # one-shot apt batch. This function owns the letsencrypt directory
  # layout the agent + nginx both expect.
  if ! command -v certbot &>/dev/null; then
    _die "certbot binary not found — install_base_packages should have installed it"
  fi

  local version
  version="$(certbot --version 2>/dev/null | head -n1)"
  _ok "Certbot present: $version"

  # Pre-create the letsencrypt directories with correct ownership.
  # The panel-agent will write certificates here; nginx may also read them.
  mkdir -p /etc/letsencrypt/{archive,live,renewal}
  chmod 0755 /etc/letsencrypt
  chmod 0755 /etc/letsencrypt/{archive,live,renewal}

  _ok "Certbot ready for SSL certificate management"
}

# ---------- step 2: Go toolchain --------------------------------------------

install_go() {
  if [[ -x "$GO_ROOT/bin/go" ]]; then
    local cur
    cur="$("$GO_ROOT/bin/go" version | awk '{print $3}')"
    if [[ "$cur" == "go$GO_VERSION" ]]; then
      _ok "Go $GO_VERSION already installed at $GO_ROOT"
      return
    fi
    _log "replacing existing Go ($cur) with $GO_VERSION"
    rm -rf "$GO_ROOT"
  fi

  _log "installing Go $GO_VERSION ($GO_ARCH)"
  local tarball="/tmp/go${GO_VERSION}.linux-${GO_ARCH}.tar.gz"
  curl -fsSL -o "$tarball" \
    "https://go.dev/dl/go${GO_VERSION}.linux-${GO_ARCH}.tar.gz"
  tar -C /usr/local -xzf "$tarball"
  rm -f "$tarball"

  # Make `go` available for interactive shells.
  cat >/etc/profile.d/jabali-go.sh <<'EOF'
export PATH="/usr/local/go/bin:$PATH"
EOF
  chmod 0644 /etc/profile.d/jabali-go.sh

  _ok "Go installed: $("$GO_ROOT/bin/go" version)"
}

# ---------- step 3: service user + dirs -------------------------------------

ensure_user_and_dirs() {
  if ! id "$SERVICE_USER" >/dev/null 2>&1; then
    _log "creating system user '$SERVICE_USER'"
    useradd --system --home-dir "$REPO_DIR" --shell /usr/sbin/nologin --groups www-data \
      --comment "Jabali Panel service user" "$SERVICE_USER"
  else
    _ok "user '$SERVICE_USER' exists"
    # Ensure service user is in www-data group so it can stat
    # per-user FPM sockets under /run/php/jabali-<user>/ (mode 0750).
    usermod -aG www-data "$SERVICE_USER" 2>/dev/null || true
  fi

  # systemd-journal group lets the panel-api ssh.login event source tail
  # the sshd journal without elevating to root. Group exists on every
  # systemd distro; ignore failure on the rare init that doesn't ship it.
  if getent group systemd-journal >/dev/null 2>&1; then
    usermod -aG systemd-journal "$SERVICE_USER" 2>/dev/null || true
  fi

  install -d -m 0755 -o "$SERVICE_USER" -g "$SERVICE_USER" "$REPO_DIR"
  install -d -m 0750 -o "$SERVICE_USER" -g "$SERVICE_USER" "$(dirname "$ENV_FILE")"
  install -d -m 0700 -o "$SERVICE_USER" -g "$SERVICE_USER" /var/lib/jabali/backups
  install -d -m 0700 -o "$SERVICE_USER" -g "$SERVICE_USER" /var/lib/jabali/restore
  # M28 — operator-uploaded panel logos. Owned by the service user so
  # the POST /admin/settings/branding/logo handler can mkdir + atomic
  # rename on upload. 0755 so nginx (proxied GET falls back to panel-
  # api anyway, but keep it world-readable for future direct serving).
  install -d -m 0755 -o "$SERVICE_USER" -g "$SERVICE_USER" /var/lib/jabali-panel
  install -d -m 0755 -o "$SERVICE_USER" -g "$SERVICE_USER" /var/lib/jabali-panel/branding
}

# ---------- M25 step 1: jabali-sockets group --------------------------------
#
# `jabali-sockets` is the cross-service group that gates connect(2) on every
# Unix-domain backend socket M25 introduces (Kratos admin, Kratos public,
# panel-api, Bulwark webmail). nginx (running as www-data) is a member so it
# can reach those sockets; the panel and webmail service users are members so
# the sockets they create end up in the right group.
#
# `jabali-mail` is intentionally NOT a member. Stalwart talks to its own ports
# (SMTP, IMAP, JMAP) — it doesn't consume our internal HTTP sockets, and the
# group should only carry users that genuinely need socket reach. See
# install/scripts/socket-helpers.sh for the RuntimeDirectory + ExecStartPost
# pattern Steps 2–5 layer on top.
#
# Idempotent: re-running on an existing install is a no-op (group already
# exists; usermod -aG silently no-ops when the user is already a member).
# Members not yet created (e.g. jabali-webmail before install_bulwark) are
# skipped this pass; the function is called again later — see main().
ensure_jabali_sockets_group() {
  if ! getent group jabali-sockets >/dev/null 2>&1; then
    _log "creating jabali-sockets system group"
    groupadd --system jabali-sockets
    _ok "jabali-sockets group created"
  fi

  local user added=0
  for user in "$SERVICE_USER" www-data jabali-webmail; do
    if ! getent passwd "$user" >/dev/null 2>&1; then
      # User not provisioned yet — a later main()-flow call picks it up.
      continue
    fi
    if id -nG "$user" | tr ' ' '\n' | grep -qx jabali-sockets; then
      continue
    fi
    usermod -aG jabali-sockets "$user"
    _ok "added $user to jabali-sockets"
    added=$((added + 1))
  done

  if (( added == 0 )); then
    _log "jabali-sockets membership already current"
  fi
}

# ---------- M25 step 1: LLMNR disable ---------------------------------------
#
# LLMNR (Link-Local Multicast Name Resolution) listens on UDP+TCP :5355 and
# is enabled by default on systemd-resolved. We don't use it — datacenter
# DNS resolution flows through pdns-recursor (M6.3) — and it's another
# unauthenticated wire-protocol surface on every interface. Disable via a
# drop-in so operators on LAN-heavy environments can override by writing
# a higher-numbered drop-in (e.g. 99-operator-keep-llmnr.conf).
disable_llmnr() {
  local conf=/etc/systemd/resolved.conf.d/10-jabali-disable-llmnr.conf
  install -d -m 0755 /etc/systemd/resolved.conf.d
  cat >"$conf" <<'EOF'
# Managed by jabali install.sh (M25). Override by adding a higher-numbered
# drop-in like /etc/systemd/resolved.conf.d/99-operator-keep-llmnr.conf
# with [Resolve]\nLLMNR=yes\n if you genuinely need LLMNR on this host.
[Resolve]
LLMNR=no
EOF
  chmod 0644 "$conf"
  systemctl reload-or-restart systemd-resolved 2>/dev/null \
    || _warn "systemd-resolved reload failed; LLMNR config will take effect on next restart"
  _ok "LLMNR disabled via $conf"
}

# ---------- step 4: clone / update repo -------------------------------------

clone_or_update_repo() {
  # Re-verify DNS before reaching for a git remote. Earlier steps in the
  # install (ufw activate, systemd-resolved restart during install_kratos'
  # config flip, crowdsec profile reload) have been observed to drop the
  # recursor → public chain on fresh installs — git clone then SERVFAILs
  # with "Could not resolve host" and under `set -e` aborts silently.
  # Probe the full chain one more time with the same 8×2s retry logic as
  # the post-recursor-install probe so transient restarts don't brick the
  # install.
  local _clone_dns_ok=0
  local attempt
  for attempt in 1 2 3 4 5 6 7 8; do
    if getent hosts "${REPO_HOST:-git.linux-hosting.co.il}" >/dev/null 2>&1; then
      _clone_dns_ok=1
      break
    fi
    sleep 2
  done
  if [[ "$_clone_dns_ok" != "1" ]]; then
    _warn "DNS not resolving for the git remote host — restarting pdns-recursor + systemd-resolved and retrying"
    systemctl restart pdns-recursor 2>/dev/null || true
    sleep 1
    systemctl restart systemd-resolved 2>/dev/null || true
    sleep 2
    if ! getent hosts "${REPO_HOST:-git.linux-hosting.co.il}" >/dev/null 2>&1; then
      _die "cannot resolve $REPO_URL — check 'systemctl status pdns-recursor systemd-resolved' and 'dig @127.0.0.1 <host>'"
    fi
  fi

  # For both clone and fetch, pass the token via a transient credential
  # helper instead of baking it into the saved remote URL. That keeps
  # `git remote -v` and `.git/config` free of secrets.
  local git_args=()
  if [[ -n "$GITEA_TOKEN" ]]; then
    # shellcheck disable=SC2016
    git_args+=(
      -c "credential.helper="
      -c "credential.helper=!f() { echo username=oauth2; echo password=$GITEA_TOKEN; }; f"
    )
  fi

  if [[ -d "$REPO_DIR/.git" ]]; then
    _log "pulling latest $REPO_BRANCH into $REPO_DIR"
    # Self-heal for a classic footgun: operator (or a prior debug
    # session) ran `git fetch`/`git pull` as root inside the repo,
    # silently re-chowning .git/FETCH_HEAD and friends to root. The
    # next install.sh fetch (run as $SERVICE_USER) then dies with
    # "cannot open '.git/FETCH_HEAD': Permission denied". Mirror the
    # fix already in panel-api update.go — re-chown the .git dir
    # before pulling so the install self-heals instead of leaving a
    # half-installed host needing a magic chown from the operator.
    # Scope intentionally narrow: just .git/, so node_modules or
    # other trees that may legitimately be group-owned differently
    # don't get clobbered.
    chown -R "$SERVICE_USER:$SERVICE_USER" "$REPO_DIR/.git"
    # No --quiet: under `set -e` a failed fetch/clone aborts install.sh
    # without any output because --quiet suppresses git's stderr, leaving
    # the operator with a silent exit. Let git's error reach the trace.
    sudo -u "$SERVICE_USER" -H git "${git_args[@]}" -C "$REPO_DIR" fetch origin "$REPO_BRANCH" \
      || _die "git fetch origin $REPO_BRANCH failed (run manually as $SERVICE_USER to see full error)"
    sudo -u "$SERVICE_USER" -H git -C "$REPO_DIR" reset --hard "origin/$REPO_BRANCH" \
      || _die "git reset --hard origin/$REPO_BRANCH failed"
  else
    _log "cloning $REPO_URL into $REPO_DIR"
    sudo -u "$SERVICE_USER" -H git "${git_args[@]}" clone --branch "$REPO_BRANCH" \
      "$REPO_URL" "$REPO_DIR" \
      || _die "git clone $REPO_URL failed (check connectivity, cert trust, and that $REPO_DIR is writable by $SERVICE_USER)"
  fi
  _ok "repo at $(sudo -u "$SERVICE_USER" -H git -C "$REPO_DIR" rev-parse --short HEAD)"
}

# ---------- step 5: build backend -------------------------------------------

# ---------- step 5a: build React SPA -----------------------------------

build_frontend() {
  _log "building panel-ui (npm ci + npm run build)"
  # npm ci needs lock + no partial node_modules. Run as the service user so
  # the node_modules cache sits in the project dir, not /root.
  sudo -u "$SERVICE_USER" -H env \
    HOME="$REPO_DIR" \
    PATH="/usr/bin:/bin" \
    bash -c "cd '$REPO_DIR/panel-ui' && npm ci --no-audit --no-fund --prefer-offline"

  # Wipe Vite's dep pre-bundling cache. When a previous install/update
  # left a node_modules/.vite dir, its cached resolutions for packages
  # like react-dom can point at paths invalidated by the fresh npm ci,
  # and vite build fails with "Failed to resolve entry for package X".
  # Cheap to regenerate (seconds).
  rm -rf "$REPO_DIR/panel-ui/node_modules/.vite"

  sudo -u "$SERVICE_USER" -H env \
    HOME="$REPO_DIR" \
    PATH="/usr/bin:/bin" \
    bash -c "cd '$REPO_DIR/panel-ui' && npm run build"
  _ok "panel-ui built → $REPO_DIR/panel-ui/dist/"
}

build_backend() {
  _log "building panel-api + jabali-agent"
  local version
  version="$(sudo -u "$SERVICE_USER" -H git -C "$REPO_DIR" rev-parse --short HEAD)"

  install -d -m 0755 -o "$SERVICE_USER" -g "$SERVICE_USER" "$REPO_DIR/bin"
  local tmp_panel="$REPO_DIR/bin/jabali-panel.new"
  local tmp_agent="$REPO_DIR/bin/jabali-agent.new"

  # One invocation of go, two binaries — shared module, shared build cache.
  sudo -u "$SERVICE_USER" -H env \
    PATH="$GO_ROOT/bin:/usr/bin:/bin" \
    HOME="$REPO_DIR" \
    GOCACHE="$REPO_DIR/.cache/go-build" \
    GOMODCACHE="$REPO_DIR/.cache/go-mod" \
    bash -c "cd '$REPO_DIR' && \
      go build -trimpath -ldflags '-s -w -X git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/api.Version=$version' -o '$tmp_panel' ./panel-api/cmd/server && \
      go build -trimpath -ldflags '-s -w -X main.version=$version' -o '$tmp_agent' ./panel-agent/cmd/jabali-agent"

  install -m 0755 "$tmp_panel" "$BIN_PATH"
  install -m 0755 "$tmp_agent" "$AGENT_BIN_PATH"
  rm -f "$tmp_panel" "$tmp_agent"

  # Ergonomic alias: `jabali ...` works the same as `jabali-panel ...`.
  # The cobra root command is already named "jabali"; this just saves
  # the "-panel" typing for operators. Symlink is idempotent.
  ln -sf "$BIN_PATH" /usr/local/bin/jabali

  _ok "installed $BIN_PATH (version=$version)"
  _ok "installed $AGENT_BIN_PATH (version=$version)"
  _ok "symlinked /usr/local/bin/jabali -> $BIN_PATH"
}

# ---------- step 6: env file + systemd unit ---------------------------------

write_env_file() {
  if [[ -f "$ENV_FILE" ]]; then
    _ok "env file exists: $ENV_FILE (not overwriting)"
    return
  fi
  local jwt_secret
  jwt_secret="$(openssl rand -hex 32)"
  _log "writing env file: $ENV_FILE (generating JWT_SECRET)"
  cat >"$ENV_FILE" <<EOF
# Jabali Panel — environment for jabali-panel.service
# Generated $(date -Iseconds). Edit as needed, then: systemctl restart $SERVICE_NAME
# Secrets belong here (DATABASE_URL, JWT_SECRET). Non-secret config goes in
# $(dirname "$ENV_FILE")/config.toml.

PANEL_ADDR=$PANEL_ADDR
PANEL_ENV=production
JWT_SECRET=$jwt_secret
EOF
  chmod 0640 "$ENV_FILE"
  chown root:"$SERVICE_USER" "$ENV_FILE"
}

# ---------- step 6a: self-signed TLS cert ------------------------------------

provision_tls_cert() {
  local cert_dir="/etc/jabali/tls"
  local cert_file="$cert_dir/panel.crt"
  local key_file="$cert_dir/panel.key"

  # Grab the machine's hostname and first non-loopback IP for SANs.
  local cn
  cn="$(hostname -f 2>/dev/null || hostname)"
  local ip
  ip="$(hostname -I 2>/dev/null | awk '{print $1}')"

  # M6.4 (ADR-0048): detect hostname drift between a prior install's cert
  # and the current $JABALI_SRV_HOSTNAME / `hostname -f`. If the cert's
  # CN no longer matches, force regeneration — otherwise the admin lands
  # on a cert for the OLD hostname, which every browser will reject.
  # Also detect missing mail.<hostname> SAN on an existing cert (common
  # on upgrade from pre-M6.4 installs).
  local need_regen=0
  if [[ -f "$cert_file" ]]; then
    local cert_cn=""
    cert_cn="$(openssl x509 -in "$cert_file" -noout -subject 2>/dev/null \
      | sed -n 's/.*CN *= *\([^,/]*\).*/\1/p' | tr -d ' ')"
    if [[ -n "$cert_cn" && "$cert_cn" != "$cn" ]]; then
      _warn "panel cert CN=$cert_cn != current hostname $cn — hostname drift, regenerating"
      need_regen=1
    elif ! openssl x509 -in "$cert_file" -noout -ext subjectAltName 2>/dev/null \
        | grep -qE "DNS:mail\.${cn}(,|$)"; then
      _log "panel cert missing mail.${cn} SAN — regenerating"
      need_regen=1
    fi
    if (( need_regen == 1 )); then
      rm -f "$cert_file" "$key_file"
    fi
  fi

  if [[ -f "$cert_file" && -f "$key_file" ]]; then
    _ok "TLS cert exists with mail.${cn} SAN: $cert_file"
  else
    _log "generating self-signed TLS certificate"
    # Dir traversable by www-data so nginx can open the key file below.
    install -d -m 0755 -o root -g root "$cert_dir"

    # M6.4: include mail.<hostname> so the panel-primary domain's
    # Bulwark vhost (served on mail.<panel-hostname>) presents a cert
    # Firefox accepts. Other per-tenant mail vhosts have their own
    # LE cert (M6.1); this SAN is panel-hostname-only.
    local san="DNS:${cn},DNS:mail.${cn},DNS:localhost,IP:127.0.0.1"
    [[ -n "$ip" ]] && san+=",IP:${ip}"

    openssl req -x509 -newkey ec -pkeyopt ec_paramgen_curve:prime256v1 \
      -keyout "$key_file" -out "$cert_file" \
      -days 3650 -nodes \
      -subj "/CN=${cn}/O=Jabali Panel" \
      -addext "subjectAltName=${san}" \
      2>/dev/null

    _ok "self-signed TLS cert created ($cert_file) with SAN ${san}"

    # M6.4: nginx re-reads the cert on next handshake via reload; panel-api
    # is a Go HTTP server that caches the cert in memory at startup and
    # does NOT SIGHUP-reread — full restart required. try-reload-or-restart
    # is the wrong signal for Go servers because reload silently succeeds
    # as a no-op, masking that the cert wasn't rotated. Accept the ~100ms
    # TLS downtime as the cost of cert rotation.
    systemctl reload nginx 2>/dev/null \
      || _warn "nginx reload failed; check 'journalctl -u nginx'"
    # Skip if jabali-panel isn't installed yet (first-time install: cert
    # runs before start_and_verify).
    if systemctl list-unit-files "${SERVICE_NAME}.service" >/dev/null 2>&1; then
      systemctl restart "$SERVICE_NAME" 2>/dev/null \
        || _warn "$SERVICE_NAME restart failed; check 'systemctl status $SERVICE_NAME'"
    fi
  fi

  # Always enforce ownership+mode (even on existing certs, in case an
  # older installer run left them root:jabali 0640, which nginx can't
  # read). Cert is public → 0644 root:root; key is shared between the
  # panel (jabali, supplementary group www-data) and nginx (www-data)
  # via group read.
  chown root:root "$cert_file"
  chmod 0644 "$cert_file"
  chown root:www-data "$key_file"
  chmod 0640 "$key_file"

  # Write TLS paths to env file if not already present.
  if ! grep -q '^TLS_CERT=' "$ENV_FILE" 2>/dev/null; then
    cat >>"$ENV_FILE" <<EOF

# TLS — self-signed; replace with Certbot cert for production.
TLS_CERT=$cert_file
TLS_KEY=$key_file
EOF
  fi
}

# ---------- step 6b: panel-hostname Let's Encrypt webroot + deploy-hook ----
#
# M32 (ADR-0066). These two functions ensure the LE machinery is in
# place on every install. They DO NOT trigger an actual issuance —
# the admin UI's "Use Let's Encrypt" toggle drives that, and the
# routability gate skips lab/dev hostnames silently. So even on a
# .local install, the webroot directory and deploy-hook script exist
# (forward-compat) but stay dormant.

bootstrap_panel_acme_webroot() {
  local webroot="/var/www/jabali-panel-acme"
  if [[ -d "$webroot" ]]; then
    _ok "panel-acme webroot exists: $webroot"
  else
    _log "creating panel-acme webroot at $webroot"
    install -d -m 0750 -o root -g www-data "$webroot"
    _ok "panel-acme webroot ready: $webroot (root:www-data 0750)"
  fi
  # Always enforce ownership/mode in case an older run left it
  # root:root 0755 — nginx (www-data) needs group read+exec to
  # serve the challenge file.
  chown root:www-data "$webroot"
  chmod 0750 "$webroot"
}

install_jabali_panel_cert_hook() {
  local hook_dir="/etc/letsencrypt/renewal-hooks/deploy"
  local hook_dst="${hook_dir}/jabali-panel-cert.sh"
  local hook_src="$REPO_DIR/install/letsencrypt/jabali-panel-cert.sh"

  if [[ ! -f "$hook_src" ]]; then
    _warn "panel-cert deploy-hook source missing at $hook_src — skipping"
    return 0
  fi

  install -d -m 0755 -o root -g root "$hook_dir"
  install -m 0755 -o root -g root "$hook_src" "$hook_dst"
  _ok "panel-cert deploy-hook installed at $hook_dst"
}

write_config_file() {
  local dest="$(dirname "$ENV_FILE")/config.toml"
  local src="$REPO_DIR/config.example.toml"
  if [[ -f "$dest" ]]; then
    # M25 Step 6: in-place migrate the [pdns] dsn from TCP to socket on
    # an existing install. (Step 4 already migrated the panel addr; this
    # block is the analogue for pdns.) Idempotent — if the file already
    # has the unix form (or any other custom value), the grep misses
    # and nothing happens.
    if grep -qE '^\s*dsn\s*=\s*"[^"]*@tcp\(127\.0\.0\.1:3306\)/jabali_pdns' "$dest"; then
      _log "migrating config.toml [pdns] dsn from TCP to unix socket (M25 Step 6)"
      sed -i 's|@tcp(127\.0\.0\.1:3306)/jabali_pdns|@unix(/var/run/mysqld/mysqld.sock)/jabali_pdns|' "$dest"
      _ok "config.toml [pdns] dsn migrated"
    fi
    _ok "config file exists: $dest (not overwriting)"
    return
  fi
  if [[ ! -f "$src" ]]; then
    _warn "no $src; skipping config seed"
    return
  fi
  _log "seeding config file: $dest"
  install -m 0640 -o root -g "$SERVICE_USER" "$src" "$dest"

  # Write the [server] block with runtime + identity keys. The panel reads
  # these on first boot to seed the server_settings DB row; the DB is the
  # source of truth afterwards (see docs/adr/0002). config.example.toml no
  # longer declares [server] itself, so this is the sole writer.
  local srv_env="production"
  [[ "${JABALI_DEV:-0}" == "1" ]] && srv_env="development"
  {
    printf '\n[server]\n'
    # M25 Step 4: panel-api listens on a Unix-domain socket. nginx
    # terminates TLS upstream of us via the jabali-panel-vhost (port
    # 8443). Operators can flip back to TCP by editing this line to
    # `127.0.0.1:8443` and dropping the unit-file Group=jabali-sockets;
    # see plans/m25-unix-sockets-runbook.md for the exact rollback.
    printf 'addr        = "unix:/run/jabali-panel/api.sock"\n'
    printf 'env         = "%s"\n' "$srv_env"
    if [[ "${JABALI_SERVER_CONFIGURED:-1}" == "0" ]]; then
      printf 'hostname    = "%s"\n' "${JABALI_SRV_HOSTNAME}"
      printf 'public_ipv4 = "%s"\n' "${JABALI_SRV_IPV4}"
      printf 'public_ipv6 = "%s"\n' "${JABALI_SRV_IPV6}"
      printf 'ns1_name    = "%s"\n' "${JABALI_SRV_NS1_NAME}"
      printf 'ns1_ipv4    = "%s"\n' "${JABALI_SRV_NS1_IPV4}"
      printf 'ns2_name    = "%s"\n' "${JABALI_SRV_NS2_NAME}"
      printf 'ns2_ipv4    = "%s"\n' "${JABALI_SRV_NS2_IPV4}"
    fi
  } >> "$dest"
  _ok "seeded [server] block in $dest"

  # PowerDNS backend DSN for the reconciler. Reads creds from pdns.env
  # so the two files stay in sync. If prompt_server_settings was
  # skipped (re-run), the env file must already exist.
  if [[ -f "${ENV_FILE%/*}/pdns.env" ]]; then
    # shellcheck disable=SC1091
    . "${ENV_FILE%/*}/pdns.env"
    cat >> "$dest" <<EOF

[pdns]
# MySQL DSN for the PowerDNS backend database. Reconciler opens a
# direct connection here to push zones/records in the same transaction
# as the NOTIFY signal.
dsn = "${PDNS_DB_USER}:${PDNS_DB_PASSWORD}@unix(/var/run/mysqld/mysqld.sock)/${PDNS_DB_NAME}?charset=utf8mb4&parseTime=true"
EOF
    _ok "seeded [pdns] block in $dest"
  fi
}

write_agent_systemd_unit() {
  _log "writing systemd unit: /etc/systemd/system/${AGENT_SERVICE_NAME}.service"
  # The agent runs as root because its whole purpose is to perform
  # privileged operations (create Linux users, manage services, etc).
  # Access control is enforced via socket permissions: RuntimeDirectory
  # creates /run/jabali owned root:jabali 0750, and the agent itself
  # chowns its socket to root:jabali 0660 so only the panel (jabali group)
  # can connect. Hardening knobs that make sense for a root daemon:
  #   - ProtectKernel*/RestrictSUIDSGID/LockPersonality keep the agent
  #     out of kernel and exec-mode bystander state
  #   - NoNewPrivileges stays false because future commands may need
  #     capabilities-aware subprocess spawns (package install etc).
  #
  # ProtectSystem= and ProtectHome= are INTENTIONALLY NOT SET. The agent
  # writes to /etc (nginx confs, /etc/passwd via useradd, /etc/php,
  # /etc/jabali-panel/dkim, /etc/letsencrypt), /home (user web roots,
  # WordPress, ~/.my.cnf), /var (jabali spool dirs, cron), and /opt
  # (phpMyAdmin, wp-cli). ProtectSystem=strict + ProtectHome=yes (as
  # previously configured) silently turned every such write into EROFS
  # and made domain.create, user.create, domain.email_enable,
  # webmail.vhost_apply, php.pool.apply and the nginx-ratelimits
  # reconciler all fail on a fresh install. Filesystem sandboxing
  # fundamentally doesn't fit a daemon whose job IS OS mutation; our
  # access-control boundary is the Unix socket, not the FS namespace.
  local jabali_gid
  jabali_gid="$(getent group "$SERVICE_USER" | cut -d: -f3)"
  [[ -n "$jabali_gid" ]] || _die "can't resolve gid of $SERVICE_USER"

  cat >"/etc/systemd/system/${AGENT_SERVICE_NAME}.service" <<EOF
[Unit]
Description=Jabali Agent (privileged host operations)
After=network-online.target
Wants=network-online.target
# Panel depends on us via Requires= in its unit, so ordering is enforced both ways.

[Service]
Type=simple
User=root
# Group=jabali makes RuntimeDirectory=jabali land as root:jabali (systemd
# always creates the dir matching the service's User:Group). The agent
# still runs with UID=0 so it retains full root for privileged ops — GID
# doesn't gate root. The panel (member of the jabali group) can therefore
# traverse /run/jabali/ and connect to the socket.
Group=$SERVICE_USER
RuntimeDirectory=jabali
RuntimeDirectoryMode=0750
RuntimeDirectoryPreserve=no
ExecStart=$AGENT_BIN_PATH -socket $AGENT_SOCKET -gid $jabali_gid
Restart=on-failure
RestartSec=3
TimeoutStopSec=10

# Hardening for a root daemon. We can't NoNewPrivileges because future
# commands may need to re-exec tooling that escalates (chpasswd, useradd
# etc). See the comment block above the cat << for why ProtectSystem=
# and ProtectHome= are deliberately omitted.
PrivateTmp=yes
ProtectKernelTunables=yes
ProtectKernelModules=yes
ProtectControlGroups=yes
RestrictSUIDSGID=yes
LockPersonality=yes

[Install]
WantedBy=multi-user.target
EOF
}

write_systemd_unit() {
  _log "writing systemd unit: /etc/systemd/system/${SERVICE_NAME}.service"
  cat >"/etc/systemd/system/${SERVICE_NAME}.service" <<EOF
[Unit]
Description=Jabali Panel API
After=network-online.target ${AGENT_SERVICE_NAME}.service redis-server.service
Wants=network-online.target
# Panel hard-requires the agent at boot; without the socket we can't do
# privileged ops. If the agent crashes post-boot the panel stays up —
# individual handlers will return 503 with agent:unavailable.
#
# Redis is a hard dep too (M14 / ADR-0056): the notification dispatcher
# can't run without its stream. systemd will restart panel-api if
# redis-server stops, so the ordering is symmetric with mariadb's.
Requires=${AGENT_SERVICE_NAME}.service redis-server.service

[Service]
Type=simple
User=$SERVICE_USER
# M25 Step 4: panel-api listens on /run/jabali-panel/api.sock owned
# jabali:jabali-sockets so nginx (member of jabali-sockets) can connect.
# The User= stays jabali (privileged ops happen via the agent's separate
# socket); only the Group= flips to expose the listen socket to nginx.
Group=jabali-sockets
# When Group= is set explicitly alongside User=, systemd replaces the
# primary GID and does NOT inherit the user's /etc/passwd primary group
# as supplementary. Without this line panel-api runs as
# jabali:jabali-sockets with no \`jabali\` supplementary, and can't read
# its own EnvironmentFile ($ENV_FILE, root:jabali 0640). See
# install/systemd/jabali-kratos.service for the identical fix reasoning.
SupplementaryGroups=$SERVICE_USER systemd-journal www-data
# /run/jabali-panel — systemd creates owned $SERVICE_USER:$SERVICE_USER 0755
# on service start and tears down on stop. The SSO UDS listener binds
# \${runtime}/sso.sock here; unlike /run/jabali (owned by root, used by
# the privileged agent), /run/jabali-panel is safe for the unprivileged
# panel to write to.
RuntimeDirectory=jabali-panel
# M25 Step 4: 0750 (down from 0755) so non-jabali-sockets users can't list
# /run/jabali-panel/. nginx (www-data) is in jabali-sockets and can still
# traverse via group permission. The api.sock file inside is mode 0660,
# pinned by the panel-api listener helper after net.Listen() returns.
RuntimeDirectoryMode=0750
EnvironmentFile=$ENV_FILE
ExecStart=$BIN_PATH serve
Restart=on-failure
RestartSec=3
TimeoutStopSec=10

# Hardening (minimal but real)
NoNewPrivileges=true
ProtectSystem=strict
ProtectHome=true
PrivateTmp=true
PrivateDevices=true
ProtectKernelTunables=true
ProtectKernelModules=true
ProtectControlGroups=true
RestrictNamespaces=true
RestrictSUIDSGID=true
LockPersonality=true
MemoryDenyWriteExecute=true
ReadWritePaths=$REPO_DIR

[Install]
WantedBy=multi-user.target
EOF

  systemctl daemon-reload
  systemctl enable --quiet "$AGENT_SERVICE_NAME.service"
  systemctl enable --quiet "$SERVICE_NAME.service"
}

# ---------- step 7: start + smoke test --------------------------------------

# ---------- step 6b: seed admin credentials ---------------------------------

seed_admin_env() {
  # If bootstrap vars are already set (e.g. re-run), don't regenerate —
  # the panel's BootstrapAdmin is idempotent and will detect the existing
  # admin row.
  if grep -q '^JABALI_BOOTSTRAP_ADMIN_EMAIL=' "$ENV_FILE" 2>/dev/null; then
    _ok "admin bootstrap vars already in $ENV_FILE"
    return
  fi

  local admin_email="admin@jabali.local"
  local admin_pass
  admin_pass="$(openssl rand -base64 18)"

  _log "seeding admin bootstrap credentials"
  cat >>"$ENV_FILE" <<EOF

# Admin bootstrap (consumed once on first boot, safe to leave).
JABALI_BOOTSTRAP_ADMIN_EMAIL=$admin_email
JABALI_BOOTSTRAP_ADMIN_PASSWORD=$admin_pass
EOF

  # Store the generated password so the final banner can display it.
  JABALI_SEED_EMAIL="$admin_email"
  JABALI_SEED_PASS="$admin_pass"
}

start_and_verify_agent() {
  _log "starting $AGENT_SERVICE_NAME"
  systemctl restart "$AGENT_SERVICE_NAME"

  # Give the socket a moment to appear. Agents boot in <100ms usually but
  # we don't want to race.
  local ok=0
  for i in 1 2 3 4 5 6 7 8 9 10; do
    if [[ -S "$AGENT_SOCKET" ]]; then ok=1; break; fi
    sleep 0.3
  done
  if (( ok == 0 )); then
    _warn "agent socket never appeared; dumping last 20 log lines"
    journalctl -u "$AGENT_SERVICE_NAME" -n 20 --no-pager || true
    _die "$AGENT_SERVICE_NAME did not come up"
  fi

  # Sanity-check: socket must be root:jabali 0660 — anything else and the
  # panel won't be able to connect.
  local sock_perms
  sock_perms="$(stat -c '%a %U:%G' "$AGENT_SOCKET")"
  _ok "agent socket ready ($AGENT_SOCKET, perms=$sock_perms)"
}

start_and_verify() {
  _log "starting $SERVICE_NAME"
  systemctl restart "$SERVICE_NAME"

  _log "waiting for /health"
  # M25 Step 4: panel-api now listens on /run/jabali-panel/api.sock.
  # curl --unix-socket reaches it directly; nginx-via-:8443 also works
  # but adds nothing for a localhost smoke. The socket path matches the
  # config seed in write_config_file().
  #
  # First-run migrations can take a while on a fresh InnoDB (45s+
  # observed). Give the service up to 120s before declaring defeat.
  local ok=0
  local deadline=$((SECONDS + 120))
  while (( SECONDS < deadline )); do
    if curl -fsS -m 2 --unix-socket /run/jabali-panel/api.sock http://panel/health >/tmp/jabali-health.json 2>/dev/null; then
      ok=1; break
    fi
    sleep 1
  done

  if (( ok == 0 )); then
    _warn "health probe failed after 120s; dumping last 40 log lines"
    journalctl -u "$SERVICE_NAME" -n 40 --no-pager || true
    _die "$SERVICE_NAME did not come up"
  fi

  _ok "health OK: $(cat /tmp/jabali-health.json)"
  rm -f /tmp/jabali-health.json

  # M25 Step 4 verification: socket must be jabali:jabali-sockets 0660.
  # (Pre-M25 we also asserted no all-interface bind on :8443 — that check
  # was correct when panel-api itself terminated TLS on :8443. Post-M25,
  # nginx owns :8443 as the public-facing TLS terminator and must bind
  # 0.0.0.0 + [::]: by design. panel-api not being on :8443 is implied
  # by it having successfully bound the unix socket above; asserting
  # nothing-on-8443 would fail on every correct install.)
  if ! verify_socket_perms /run/jabali-panel/api.sock jabali jabali-sockets 660; then
    _die "panel-api socket has wrong perms — see message above"
  fi

  # In-place migration: rewrite an existing config.toml's addr from any
  # TCP form (127.0.0.1:8443, 0.0.0.0:8443, [::]:8443, :8443) to the unix
  # form. The legacy PANEL_ADDR default was 0.0.0.0:8443, so installs
  # seeded before M25 have that — the narrower 127.0.0.1-only match
  # (M25 ship 2026-04-23) missed them and panel-api crash-looped on :8443
  # EADDRINUSE (each restart raced its predecessor's TIME_WAIT close).
  # Guarded by "is currently TCP AND not already unix:" so rerunning on
  # a migrated box is a no-op.
  local panel_config="/etc/jabali-panel/config.toml"
  if [[ -f "$panel_config" ]] \
     && grep -qE '^\s*addr\s*=\s*"[^"]*:8443"' "$panel_config" \
     && ! grep -qE '^\s*addr\s*=\s*"unix:' "$panel_config"; then
    _log "migrating config.toml addr from TCP to unix socket (M25 Step 4)"
    sed -i -E 's|^(\s*addr\s*=\s*)"[^"]*:8443"|\1"unix:/run/jabali-panel/api.sock"|' "$panel_config"
    _ok "config.toml addr migrated"
    _log "restarting $SERVICE_NAME after addr migration"
    systemctl restart "$SERVICE_NAME"
  fi
}

# ---------- step: seed last-built-sha --------------------------------------
# Matches the contract in panel-api/cmd/server/update.go: that file tracks
# the SHA of the last fully-rebuilt + restarted commit so `jabali update`
# can tell "no-op, skip rebuild" from "HEAD moved or a prior build failed
# mid-flow, must rebuild". Fresh install is by definition a successful
# build against the current HEAD, so we seed it here.

seed_last_built_sha() {
  local sha
  sha="$(sudo -u "$SERVICE_USER" git -C "$REPO_DIR" rev-parse HEAD 2>/dev/null || true)"
  if [[ -z "$sha" ]]; then
    _warn "could not resolve HEAD in $REPO_DIR; skipping last-built-sha seed"
    return 0
  fi
  # M28 aligned: panel-api writes operator logos into
  # /var/lib/jabali-panel/branding as $SERVICE_USER, so the parent
  # must be owned by $SERVICE_USER too. install -d on an existing
  # dir still applies -o/-g/-m, so this converges whichever of the
  # two install.sh steps runs last.
  install -d -m 0755 -o "$SERVICE_USER" -g "$SERVICE_USER" /var/lib/jabali-panel
  printf '%s\n' "$sha" >/var/lib/jabali-panel/last-built-sha
  chown "$SERVICE_USER:$SERVICE_USER" /var/lib/jabali-panel/last-built-sha
  chmod 0644 /var/lib/jabali-panel/last-built-sha
  _ok "last-built-sha seeded to ${sha:0:7}"
}

# ---------- step: SSO key generation ----------------------------------------


# ---------- M25 Step 4: nginx panel vhost (TLS terminator on :8443) -----

install_nginx_panel_vhost() {
  _log "installing nginx panel vhost (M25 Step 4 — TLS terminator on :8443)"

  local nginx_sites_dir="/etc/nginx/sites-available"
  local nginx_enabled_dir="/etc/nginx/sites-enabled"
  local panel_vhost_file="${nginx_sites_dir}/jabali-panel.conf"
  local tmpl="${REPO_DIR}/install/nginx/jabali-panel-vhost.conf.tmpl"
  local tls_cert="/etc/jabali/tls/panel.crt"
  local tls_key="/etc/jabali/tls/panel.key"

  if [[ ! -f "$tmpl" ]]; then
    _die "panel vhost template not found at $tmpl"
  fi
  if [[ ! -f "$tls_cert" || ! -f "$tls_key" ]]; then
    _die "TLS cert missing at $tls_cert — provision_tls_cert must run first"
  fi

  # Render the template by substituting ${SSL_CERT_PATH} + ${SSL_KEY_PATH}
  # via sed. envsubst would be cleaner but isn't a dependency we want to
  # add solely for two substitutions.
  sed \
    -e "s|\${SSL_CERT_PATH}|${tls_cert}|g" \
    -e "s|\${SSL_KEY_PATH}|${tls_key}|g" \
    "$tmpl" > "$panel_vhost_file"

  if grep -q '\${' "$panel_vhost_file"; then
    _die "unsubstituted \${VAR} placeholders left in $panel_vhost_file — template drift?"
  fi

  ln -sf "$panel_vhost_file" "${nginx_enabled_dir}/jabali-panel.conf"

  _log "testing nginx configuration"
  if ! nginx -t 2>&1 | grep -q "successful"; then
    nginx -t 2>&1 >&2 || true
    _die "nginx configuration test failed (panel vhost)"
  fi

  _log "reloading nginx"
  systemctl reload nginx || {
    _warn "nginx reload failed; trying restart"
    systemctl restart nginx
  }

  _ok "panel nginx vhost installed: ${panel_vhost_file}"
}

# ---------- step 6.4: nginx default vhost for phpMyAdmin SSO -----

install_nginx_default_vhost() {
  _log "creating default nginx vhost (80 -> 443 redirect, 443 with panel TLS cert)"

  local nginx_sites_dir="/etc/nginx/sites-available"
  local nginx_enabled_dir="/etc/nginx/sites-enabled"
  local default_vhost_file="${nginx_sites_dir}/jabali-default.conf"
  local tls_cert="/etc/jabali/tls/panel.crt"
  local tls_key="/etc/jabali/tls/panel.key"

  # Sanity: the cert must exist (provision_tls_cert runs earlier in main()).
  if [[ ! -f "$tls_cert" || ! -f "$tls_key" ]]; then
    _die "TLS cert missing: $tls_cert — provision_tls_cert must run first"
  fi

  # Default vhost:
  #   - :80 force-redirects everything to https:// (panel is https-only)
  #   - :443 terminates TLS with the panel's self-signed cert and serves
  #     phpMyAdmin at /phpmyadmin/ (panel itself is on :8443, separate).
  _log "writing ${default_vhost_file}"
  cat > "${default_vhost_file}" << VHOSTEOF
# Jabali default vhost. The panel is https-only — port 80 exists purely
# to redirect any stray http request to https. phpMyAdmin is served on
# :443 alongside the panel (panel runs on :8443 directly, phpMyAdmin is
# fronted by nginx here on :443 using the same self-signed cert).

server {
    listen 80 default_server;
    listen [::]:80 default_server;
    server_name _;

    # M32 (ADR-0066): serve LE HTTP-01 challenges for the panel
    # hostname out of /var/www/jabali-panel-acme. The ^~ modifier
    # makes this location take precedence over any future regex
    # locations and over the catch-all return 444 below. Customer
    # domain vhosts have their own ACME location at user-webroot
    # paths and match BEFORE this default block, so this only
    # fires for the panel hostname (and for any stray host that
    # doesn't have its own :80 server block but happens to be
    # validating against this VPS).
    location ^~ /.well-known/acme-challenge/ {
        default_type "text/plain";
        root /var/www/jabali-panel-acme;
        try_files \$uri =404;
    }

    # 444 = close without response. Any HTTP request on a hostname we
    # don't know is silently dropped — no redirect to https because
    # https will just 444 too, and no HTML because we don't want to
    # leak "this server runs nginx" to random scanners. Domains with
    # their own vhost match BEFORE this default block, so this only
    # fires for hosts nginx has no server{} for.
    return 444;
}

server {
    listen 443 ssl default_server;
    listen [::]:443 ssl default_server;
    http2 on;
    server_name _;

    ssl_certificate     ${tls_cert};
    ssl_certificate_key ${tls_key};
    ssl_protocols       TLSv1.2 TLSv1.3;
    ssl_ciphers         HIGH:!aNULL:!MD5;
    ssl_prefer_server_ciphers on;

    access_log /var/log/nginx/default.access.log;
    error_log  /var/log/nginx/default.error.log;

    # phpMyAdmin stays reachable on the panel hostname for admin use.
    # The include's server_name-less /phpmyadmin/ location is matched
    # before the catch-all location / below.
    include /etc/nginx/sites-available/includes/phpmyadmin.conf;

    # M6.4 (ADR-0048): /webmail bounces to the panel-primary domain's
    # Bulwark instance on mail.<hostname>. Target is interpolated here
    # at install.sh render time (heredoc expands \${JABALI_SRV_HOSTNAME});
    # hostname changes propagate on the next install.sh run because the
    # whole default vhost is rewritten unconditionally.
    #
    # No graceful fallback on pre-convergence — the ~30s window where
    # mail.<hostname> isn't yet served is documented in ADR-0048 Decision
    # 4 as acceptable; operators who want a 503 page see M6.4.4 follow-up.
    location = /webmail {
        return 301 https://mail.${JABALI_SRV_HOSTNAME}/;
    }
    location = /webmail/ {
        return 301 https://mail.${JABALI_SRV_HOSTNAME}/;
    }

    # Everything else on an unknown host silently drops. The prior
    # behaviour (try_files on /var/www/html → 403) leaked a default
    # vhost for domains without an SSL cert yet and sent users a
    # confusing "403 Forbidden" with the panel's self-signed cert.
    location / {
        return 444;
    }
}
VHOSTEOF

  _ok "default vhost config written"

  # Debian's default nginx.conf includes both `sites-enabled/*` and (since
  # our install_nginx step) `sites-enabled/*.conf`, so we must ensure the
  # stock `default` symlink is removed — otherwise both the stock default
  # vhost and our new jabali-default.conf bind `listen 80 default_server`
  # and nginx -t fails with "duplicate default server".
  if [[ -L "${nginx_enabled_dir}/default" || -e "${nginx_enabled_dir}/default" ]]; then
    _log "removing stock ${nginx_enabled_dir}/default symlink"
    rm -f "${nginx_enabled_dir}/default"
  fi

  # Create symlink (.conf extension so it's picked up by either include pattern).
  _log "creating symlink ${nginx_enabled_dir}/default.conf -> ${default_vhost_file}"
  ln -sf "${default_vhost_file}" "${nginx_enabled_dir}/default.conf"

  # Test nginx configuration
  _log "testing nginx configuration"
  if ! nginx -t 2>&1 | grep -q "successful"; then
    nginx -t 2>&1 >&2 || true
    _die "nginx configuration test failed"
  fi

  # Reload nginx
  _log "reloading nginx"
  systemctl reload nginx || {
    _warn "nginx reload failed; trying restart"
    systemctl restart nginx
  }

  _ok "default nginx vhost installed and activated"
}


# ---------- step 6.5: phpMyAdmin dedicated FPM pool -----

install_phpmyadmin_fpm_pool() {
  _log "installing dedicated FPM pool for phpMyAdmin"

  local pma_user="www-data"
  local pma_pool="pma"
  local pma_phpver="8.5"
  local pma_root="/opt/phpmyadmin/current"

  # Create version pin for pma pool
  _log "pinning PHP version for pma pool"
  mkdir -p /etc/jabali-panel/user-phpver
  echo "$pma_phpver" > /etc/jabali-panel/user-phpver/pma
  chmod 0644 /etc/jabali-panel/user-phpver/pma
  _ok "PHP version pinned: $pma_phpver"

  # Create pool directory for FPM config
  mkdir -p /etc/php/${pma_phpver}/fpm/pool.d
  chmod 0755 /etc/php/${pma_phpver}/fpm/pool.d

  # Write pool config: jabali-pma.conf
  _log "writing pool config for jabali-pma"
  cat > /etc/php/${pma_phpver}/fpm/pool.d/jabali-pma.conf <<'POOLEOF'
[jabali-pma]
user = www-data
group = www-data
listen = /run/php/jabali-pma/fpm.sock
listen.owner = www-data
listen.group = www-data
listen.mode = 0660
pm = ondemand
pm.max_children = 10
pm.process_idle_timeout = 60s
chdir = /opt/phpmyadmin/current
security.limit_extensions = .php

; phpMyAdmin needs access to its own code, /tmp for sessions, and
; /etc/jabali-panel/sso.key is out of scope — phpMyAdmin only reads
; creds from the UDS SSO validator, never the key itself.
php_admin_value[open_basedir] = /opt/phpmyadmin:/tmp:/var/tmp
POOLEOF
  chmod 0644 /etc/php/${pma_phpver}/fpm/pool.d/jabali-pma.conf
  _ok "pool config written"

  # Write per-pool FPM master config: /etc/jabali-panel/fpm/pma.conf
  _log "writing per-pool FPM master config"
  mkdir -p /etc/jabali-panel/fpm
  cat > /etc/jabali-panel/fpm/pma.conf <<'FPMEOF'
[global]
pid = /run/php/jabali-pma/fpm.pid
error_log = /var/log/php-fpm-pma.log
daemonize = no
include=/etc/php/8.5/fpm/pool.d/jabali-pma.conf
FPMEOF
  chmod 0644 /etc/jabali-panel/fpm/pma.conf
  _ok "per-pool FPM master config written"

  # Pre-create the FPM error log file with www-data ownership
  _log "pre-creating FPM error log"
  if [[ ! -e "/var/log/php-fpm-pma.log" ]]; then
    install -m 0640 -o www-data -g www-data /dev/null /var/log/php-fpm-pma.log
  else
    chown www-data:www-data /var/log/php-fpm-pma.log
    chmod 0640 /var/log/php-fpm-pma.log
  fi
  _ok "FPM error log pre-created"

  # Create systemd drop-in for the FPM service (sets Slice)
  _log "creating systemd drop-in for jabali-fpm@pma.service"
  mkdir -p /etc/systemd/system/jabali-fpm@pma.service.d
  cat > /etc/systemd/system/jabali-fpm@pma.service.d/slice.conf <<'DROPINEOF'
[Service]
User=www-data
Group=www-data
ExecStart=
ExecStart=/usr/sbin/php-fpm8.5 --nodaemonize --fpm-config=/etc/jabali-panel/fpm/pma.conf
SyslogIdentifier=php-fpm-pma
Slice=jabali.slice
DROPINEOF
  chmod 0644 /etc/systemd/system/jabali-fpm@pma.service.d/slice.conf
  _ok "systemd drop-in created"

  # Reload systemd and start the service
  _log "reloading systemd daemon"
  systemctl daemon-reload

  _log "starting and verifying jabali-fpm@pma service"
  systemctl reset-failed jabali-fpm@pma.service 2>/dev/null || true
  systemctl enable jabali-fpm@pma.service
  systemctl restart jabali-fpm@pma.service

  # Poll for the FPM socket. ondemand mode still creates the listening
  # socket at master start, but there's a race between systemctl returning
  # and FPM finishing pool initialization. Observed on LXC containers
  # where FPM takes ~5s after "Started" to bind the socket; 5s of polling
  # (the old budget) was clipping the race. 30s gives cold-start LXC
  # hosts headroom without meaningfully delaying healthy installs
  # (fast hosts break out on tries 1-5).
  local sock="/run/php/jabali-pma/fpm.sock"
  local tries=0
  while (( tries < 120 )); do
    [[ -S "$sock" ]] && break
    sleep 0.25
    tries=$((tries + 1))
  done
  if [[ ! -S "$sock" ]]; then
    _warn "FPM socket $sock was not created — dumping status for diagnosis:"
    systemctl status jabali-fpm@pma.service --no-pager -l | sed 's/^/  /' >&2 || true
    journalctl -u jabali-fpm@pma.service -n 30 --no-pager | sed 's/^/  /' >&2 || true
    _die "jabali-fpm@pma failed to create socket $sock"
  fi

  _ok "phpMyAdmin FPM pool (jabali-pma) installed and running"
}

# ---------- step 6.5: wp-cli provisioning ------------------------------------

install_wp_cli() {
  _log "installing wp-cli"

  # Version pin — must match the checksum file below.
  # Update both when upgrading wp-cli.
  local wp_version="2.12.0"
  local wp_root="/opt/wp-cli"
  local wp_phar="${wp_root}/wp-cli-${wp_version}.phar"
  local wp_link="${wp_root}/current"
  local wp_archive="/tmp/wp-cli-${wp_version}.phar"

  # Idempotency: if already installed, skip download + verify.
  if [[ -f "$wp_phar" && -L "$wp_link" ]]; then
    _ok "wp-cli $wp_version already installed at $wp_root"
    return
  fi

  # Ensure the root directory exists
  mkdir -p "$wp_root"
  chmod 0755 "$wp_root"

  # Download the phar
  _log "downloading wp-cli $wp_version"
  if ! curl -fsSL -o "$wp_archive" \
    "https://github.com/wp-cli/wp-cli/releases/download/v${wp_version}/wp-cli-${wp_version}.phar"; then
    _die "failed to download wp-cli $wp_version phar"
  fi

  # Verify checksum
  _log "verifying wp-cli checksum"
  local expected_sum
  expected_sum="$(grep -v '^#' "${REPO_DIR}/install/wp-cli.sha256" | awk '{print $1}')"
  local actual_sum
  actual_sum="$(sha256sum "$wp_archive" | awk '{print $1}')"
  if [[ "$expected_sum" != "$actual_sum" ]]; then
    rm -f "$wp_archive"
    _die "wp-cli checksum mismatch: expected $expected_sum, got $actual_sum"
  fi
  _ok "checksum verified"

  # Move to permanent location
  _log "installing wp-cli to $wp_root"
  mv "$wp_archive" "$wp_phar"
  chmod 0755 "$wp_phar"

  # Create symlink for easy access
  rm -f "$wp_link"
  ln -s "$wp_phar" "$wp_link"

  # Create symlink in /usr/local/bin
  rm -f /usr/local/bin/wp
  ln -s "$wp_link" /usr/local/bin/wp

  _ok "wp-cli extracted and symlinked"
}

# ---------- step 7: phpMyAdmin + SSO support --------------------------------

install_phpmyadmin() {
  _log "installing phpMyAdmin with SSO support"

  # Version pin — must match the checksum file below.
  # Update both when upgrading phpMyAdmin.
  local pma_version="5.2.3"
  local pma_root="/opt/phpmyadmin"
  local pma_extract="${pma_root}/phpMyAdmin-${pma_version}-all-languages"
  local pma_link="${pma_root}/current"
  local pma_archive="/tmp/phpMyAdmin-${pma_version}-all-languages.tar.gz"

  # Idempotency: if already extracted, skip the download + extract.
  if [[ -d "$pma_extract" && -L "$pma_link" ]]; then
    _ok "phpMyAdmin $pma_version already installed at $pma_root"
    # Still need to ensure config.inc.php and sso.php are in place
    # (they may have been missing in an older install run).
  else
    # Ensure the root directory exists
    mkdir -p "$pma_root"
    chmod 0755 "$pma_root"

    # Download the tarball. files.phpmyadmin.net's CDN occasionally
    # closes the TLS connection mid-transfer (OpenSSL errno 0,
    # "unexpected eof while reading"). --retry covers that class of
    # transient failure; --retry-all-errors (curl 7.71+) means we also
    # retry on HTTP 5xx and non-network glitches. --max-time 300 caps
    # total wall-time so the installer doesn't stall forever on a
    # dead upstream.
    _log "downloading phpMyAdmin $pma_version"
    if ! curl -fsSL --retry 5 --retry-delay 3 --retry-all-errors \
         --max-time 300 -o "$pma_archive" \
         "https://files.phpmyadmin.net/phpMyAdmin/${pma_version}/phpMyAdmin-${pma_version}-all-languages.tar.gz"; then
      _die "failed to download phpMyAdmin $pma_version tarball after 5 retries — check network / upstream status at https://www.phpmyadmin.net/downloads/"
    fi

    # Verify checksum
    _log "verifying phpMyAdmin checksum"
    local expected_sum
    expected_sum="$(grep -v '^#' "${REPO_DIR}/install/phpmyadmin.sha256" | head -1)"
    local actual_sum
    actual_sum="$(sha256sum "$pma_archive" | awk '{print $1}')"
    if [[ "$expected_sum" != "$actual_sum" ]]; then
      rm -f "$pma_archive"
      _die "phpMyAdmin checksum mismatch: expected $expected_sum, got $actual_sum"
    fi
    _ok "checksum verified"

    # Extract
    _log "extracting phpMyAdmin to $pma_root"
    tar -C "$pma_root" -xzf "$pma_archive"
    rm -f "$pma_archive"

    # Create symlink for easy access
    rm -f "$pma_link"
    ln -s "$pma_extract" "$pma_link"
    _ok "phpMyAdmin extracted and symlinked"
  fi

  # Write config.inc.php (idempotent: overwrite on every run to stay in sync with code)
  _log "writing phpMyAdmin config.inc.php"
  cat > "${pma_link}/config.inc.php" <<'CONFIGEOF'
<?php
/**
 * phpMyAdmin configuration file (auto-generated by install.sh).
 *
 * This config uses phpMyAdmin's signon authentication mode, which expects
 * the frontend to populate the SignonSession with MySQL credentials and
 * redirect to index.php. The SSO handler (sso.php) does this.
 */

// Authentication method
$cfg['Servers'][1]['auth_type'] = 'signon';

// SSO handler endpoint (relative to phpMyAdmin root)
$cfg['Servers'][1]['SignonURL'] = '/phpmyadmin/sso.php';

// Session name used for signon credentials
$cfg['Servers'][1]['SignonSession'] = 'SignonSession';

// Disable the login form (we use SSO only)
$cfg['Servers'][1]['SignonLogoutURL'] = '/logout';

// MySQL connection details
// Note: sso.php will override these with the per-user values from the panel API.
// These defaults are NOT used for authentication; they're fallbacks.
// M25.1: default to Unix socket so that even without SSO (e.g. a direct
// /phpmyadmin/index.php visit) phpMyAdmin's connect path matches what
// skip-networking in my.cnf permits. Port kept for wire-level
// compatibility with older signon plugins; with connect_type=socket
// phpMyAdmin ignores it.
$cfg['Servers'][1]['host'] = 'localhost';
$cfg['Servers'][1]['port'] = 3306;
$cfg['Servers'][1]['connect_type'] = 'socket';
$cfg['Servers'][1]['socket'] = '/var/run/mysqld/mysqld.sock';
$cfg['Servers'][1]['compress'] = false;

// No control connection. phpMyAdmin uses the "controluser" for its
// optional pmadb features (bookmarks, history, designer, etc.) — we
// disable pmadb below, so no second connection is needed. Leaving
// controluser = 'root' here would make phpMyAdmin try to authenticate
// as root@localhost on every page load and surface "Access denied for
// user 'root'@'localhost'" + "Connection for controluser failed"
// banners, even on SSO sessions that work fine for the data
// connection. Omitting these keys entirely makes PMA skip it.

// Allow no password (some test/dev servers may have unprotected root)
$cfg['Servers'][1]['AllowNoPassword'] = false;

// Appearance
$cfg['PmaAbsoluteUri'] = 'https://' . $_SERVER['HTTP_HOST'] . '/phpmyadmin/';
$cfg['Servers'][1]['only_db'] = '';

// Session settings
$cfg['SessionSavePath'] = '/tmp';
$cfg['SendErrorReports'] = 'always';
$cfg['ErrorHandler'] = 'default';

// Allow extensions (for bookmarks, query history, etc.)
$cfg['Servers'][1]['pmadb'] = false;  // Disable to avoid per-user pma__* tables
$cfg['Servers'][1]['bookmarktable'] = false;
$cfg['Servers'][1]['relation'] = false;
$cfg['Servers'][1]['table_info'] = false;
$cfg['Servers'][1]['table_coords'] = false;
$cfg['Servers'][1]['pdf_pages'] = false;
$cfg['Servers'][1]['column_info'] = false;
$cfg['Servers'][1]['history'] = false;
$cfg['Servers'][1]['recent'] = false;
$cfg['Servers'][1]['favorite'] = false;
$cfg['Servers'][1]['users'] = false;
$cfg['Servers'][1]['usergroups'] = false;
$cfg['Servers'][1]['navigationhiding'] = false;
$cfg['Servers'][1]['savedsearches'] = false;
$cfg['Servers'][1]['central_columns'] = false;
$cfg['Servers'][1]['designer_settings'] = false;
$cfg['Servers'][1]['export_templates'] = false;

// Security: hide password in PhpMyAdmin interface
$cfg['Servers'][1]['hide_dbs'] = '';

// SSL settings for secure connections (if needed)
$cfg['Servers'][1]['ssl'] = false;
$cfg['Servers'][1]['ssl_key'] = '';
$cfg['Servers'][1]['ssl_cert'] = '';
$cfg['Servers'][1]['ssl_ca'] = '';
$cfg['Servers'][1]['ssl_capath'] = '';
$cfg['Servers'][1]['ssl_ciphers'] = '';

// Miscellaneous
$cfg['CookiePath'] = '/phpmyadmin/';
$cfg['CookieSameSite'] = 'Lax';
$cfg['CookieSecure'] = true;
$cfg['CookieHttpOnly'] = true;

?>
CONFIGEOF
  # `cat >` inherits the invoking shell's umask, which under systemd or
  # sudo is often 0077/0027, leaving the file as 0600 root:root. phpMyAdmin
  # (running in the jabali-pma pool as www-data) then greets the user with
  # "Existing configuration file ... is not readable." Force readable perms.
  chown root:www-data "${pma_link}/config.inc.php"
  chmod 0640 "${pma_link}/config.inc.php"
  _ok "config.inc.php written"

  # Deploy sso.php from the install directory
  _log "deploying SSO handler"
  if [[ ! -f "${REPO_DIR}/install/phpmyadmin/sso.php" ]]; then
    _die "sso.php not found at ${REPO_DIR}/install/phpmyadmin/sso.php"
  fi
  cp "${REPO_DIR}/install/phpmyadmin/sso.php" "${pma_link}/sso.php"
  chown root:www-data "${pma_link}/sso.php"
  chmod 0640 "${pma_link}/sso.php"
  _ok "sso.php deployed"

  # Ensure the nginx config directory exists
  local nginx_inc_dir="/etc/nginx/sites-available/includes"
  mkdir -p "$nginx_inc_dir"
  chmod 0755 "$nginx_inc_dir"

  # Write the http-scope map + log_format to /etc/nginx/conf.d/. Debian's
  # nginx.conf already includes conf.d/*.conf at http{} scope, so this is
  # the right place for directives that can't live inside server{}.
  _log "writing jabali-pma http-scope log format"
  mkdir -p /etc/nginx/conf.d
  cat > /etc/nginx/conf.d/jabali-pma-logformat.conf <<'LOGFMTEOF'
# jabali phpMyAdmin log format — redacts query strings so SSO tokens
# don't leak into access logs. Referenced by the /phpmyadmin/ location
# block at /etc/nginx/sites-available/includes/phpmyadmin.conf.
map $args $jabali_pma_logargs {
    ""      "-";
    default "[REDACTED]";
}
log_format jabali_pma '$remote_addr - $remote_user [$time_local] '
                      '"$request_method $uri $server_protocol" '
                      '$status $body_bytes_sent '
                      'args=$jabali_pma_logargs "$http_referer" '
                      '"$http_user_agent"';
LOGFMTEOF
  chmod 0644 /etc/nginx/conf.d/jabali-pma-logformat.conf
  _ok "jabali-pma log format written"

  # Write the phpMyAdmin nginx location block (reusable include for the panel vhost)
  _log "writing phpMyAdmin nginx location block"
  cat > "${nginx_inc_dir}/phpmyadmin.conf" <<'NGINXEOF'
# phpMyAdmin location block for nginx.
# Designed to be included inside a server{} block (port 80 default vhost
# or the panel vhost). The jabali_pma log_format used below is defined
# at http{} scope in /etc/nginx/conf.d/jabali-pma-logformat.conf.

# phpMyAdmin location (matches /phpmyadmin/* requests)
location ^~ /phpmyadmin/ {
    # Redirect to the location symlink
    alias /opt/phpmyadmin/current/;

    # Log with redacted query string (no tokens in access log)
    access_log /var/log/nginx/jabali-pma.access.log jabali_pma;
    error_log  /var/log/nginx/jabali-pma.error.log warn;

    # Deny access to sensitive files
    location ~ /\.ht {
        deny all;
    }
    location ~ /install {
        deny all;
    }

    # Pass PHP files to the appropriate FPM pool
    # This will be templated at vhost render time with the domain owner's pool socket
    location ~ \.php$ {
        # NOTE: nginx templater must replace {PHP_POOL_SOCKET} with the actual pool socket
        # Example: fastcgi_pass unix:/run/php/jabali-user123/fpm.sock;
        fastcgi_pass unix:{PHP_POOL_SOCKET};
        fastcgi_index index.php;
        include fastcgi_params;
        fastcgi_param SCRIPT_FILENAME $request_filename;
    }

    # Static files (CSS, JS, images) — cache them
    location ~ \.(js|css|png|jpg|jpeg|gif|ico|svg|woff|woff2|ttf|eot)$ {
        expires 30d;
        add_header Cache-Control "public, immutable";
    }
}
NGINXEOF

  # Substitute {PHP_POOL_SOCKET} placeholder with actual pma socket
  _log "substituting PHP_POOL_SOCKET in phpmyadmin.conf"
  sed -i "s|{PHP_POOL_SOCKET}|/run/php/jabali-pma/fpm.sock|g" "${nginx_inc_dir}/phpmyadmin.conf"
  _ok "phpMyAdmin nginx config ready"
  _ok "nginx location block written"

  # Create log directory for phpMyAdmin nginx logs
  mkdir -p /var/log/nginx
  touch /var/log/nginx/jabali-pma.access.log
  touch /var/log/nginx/jabali-pma.error.log
  chmod 0640 /var/log/nginx/jabali-pma.{access,error}.log
  chown www-data:www-data /var/log/nginx/jabali-pma.{access,error}.log

  _ok "phpMyAdmin installed and configured"
}

install_sftp_group() {
  _log "creating jabali-sftp system group"

  # Check if group exists using getent.
  if getent group jabali-sftp >/dev/null; then
    _ok "jabali-sftp group already exists"
  else
    # Create the group as a system group.
    groupadd --system jabali-sftp 2>/dev/null || true
    _ok "jabali-sftp system group created"
  fi
}

install_sftp_sshd_config() {
  _log "installing SFTP sshd drop-in configuration"

  # Install the sshd drop-in configuration file with correct permissions.
  # Path is resolved against $REPO_DIR (clone target) so this works under
  # `curl | bash` where CWD has no ./install/ tree.
  install -m 0644 -o root -g root "$REPO_DIR/install/ssh/jabali-sftp.conf" /etc/ssh/sshd_config.d/jabali-sftp.conf
  _ok "SFTP sshd drop-in installed"

  # Validate sshd configuration before reloading.
  _log "validating sshd configuration"
  if ! sshd -t; then
    _die "sshd configuration validation failed. Check /etc/ssh/sshd_config.d/jabali-sftp.conf for errors."
  fi
  _ok "sshd configuration is valid"

  # Reload sshd to apply the new configuration.
  _log "reloading sshd"
  systemctl reload sshd
  _ok "sshd reloaded"
}

install_sso_key() {
  local sso_key_path="/etc/jabali-panel/sso.key"

  # Always enforce ownership+mode, even when the file already exists —
  # earlier installer versions wrote it mode 0600 owned by root, which
  # the panel service user cannot read. Fix in place on every run.
  mkdir -p /etc/jabali-panel
  chmod 0755 /etc/jabali-panel

  if [[ -f "$sso_key_path" ]]; then
    chown "$SERVICE_USER:$SERVICE_USER" "$sso_key_path"
    chmod 0600 "$sso_key_path"
    _ok "SSO key already exists at $sso_key_path (ownership refreshed)"
    return
  fi

  _log "generating SSO envelope key (32 bytes AES-256-GCM)"

  # Generate 32 random bytes and write to file with restrictive permissions,
  # owned by the service user so the panel process can read it.
  dd if=/dev/urandom of="$sso_key_path" bs=1 count=32 2>/dev/null
  chown "$SERVICE_USER:$SERVICE_USER" "$sso_key_path"
  chmod 0600 "$sso_key_path"

  _ok "SSO key created at $sso_key_path"
}

install_sso_reaper_timer() {
  # M22 rework (ADR-0040): the self-deleting sso-file design uses a
  # systemd timer to sweep stranded jabali-sso-<nonce>.php files older
  # than 60s. Defence in depth — the PHP file unlinks itself after
  # successful login, so the reaper only catches files that didn't get
  # to that step (PHP fatal mid-execution, web server crash, etc.).
  _log "installing sso reaper systemd timer"
  # install.sh never cd's into $REPO_DIR — every other function anchors
  # source paths against ${REPO_DIR} explicitly (see install_jabali_slices,
  # install_php_pool_template, install_kratos). A relative path like
  # "install/systemd/..." resolves against $PWD, which is /root when the
  # script runs via `curl | bash`, and the file-exists check below fires
  # with _err → exit 1. Fix: match the pattern used everywhere else.
  local svc_src="${REPO_DIR}/install/systemd/jabali-sso-reaper.service"
  local timer_src="${REPO_DIR}/install/systemd/jabali-sso-reaper.timer"
  local svc_dst="/etc/systemd/system/jabali-sso-reaper.service"
  local timer_dst="/etc/systemd/system/jabali-sso-reaper.timer"

  if [[ ! -f "$svc_src" || ! -f "$timer_src" ]]; then
    _err "sso reaper systemd units missing at $svc_src / $timer_src"
    exit 1
  fi

  install -m 0644 -o root -g root "$svc_src" "$svc_dst"
  install -m 0644 -o root -g root "$timer_src" "$timer_dst"

  # daemon-reload + enable --now are the two places this function has
  # historically stalled. Log before each so a bash `set -e` exit pins
  # the culprit — previous regression showed "SSO key created" as the
  # last line because every step in this function was silent.
  _log "sso reaper: systemctl daemon-reload"
  systemctl daemon-reload

  _log "sso reaper: enable --now jabali-sso-reaper.timer"
  systemctl enable --now jabali-sso-reaper.timer

  _ok "sso reaper timer enabled (every 30s)"
}

# ---------- step 8a: M26 security foundation (CrowdSec + UFW) ---------------
#
# Two idempotent installs that land BEFORE Stalwart so:
#   - CrowdSec LAPI binds on /run/crowdsec/api.sock (NOT 127.0.0.1:8080,
#     which Stalwart owns per ADR-0050).
#   - UFW is `enable`d ONCE (idempotent guard) before Stalwart's first
#     bind so the iptables/nftables reload cannot race Stalwart startup.
#
# Both are wired into main() between install_pdns_recursor and
# install_stalwart. Apt packages (crowdsec, ufw, yq) are in the
# install_base_packages batch; the CrowdSec firewall bouncer is detected
# + installed at runtime here because the package name varies by Debian
# release (nftables on trixie, iptables on bookworm) and apt-cache
# fallback is the safer model.
#
# cleanup_modsecurity also runs in this block — removes the dead M26
# ModSecurity stack on hosts upgraded from older installs (ADR-0055
# SUPERSEDED 2026-04-26).

add_crowdsec_apt_source() {
  # CrowdSec upstream apt source. Debian-stock crowdsec on trixie is
  # 1.4.6 — too old to support `api.server.listen_socket` (added in
  # CrowdSec 1.5.x; ADR-0050 requires socket binding). Upstream repo
  # ships current 1.7.x with socket support PLUS both bouncer variants
  # (crowdsec-firewall-bouncer-{iptables,nftables}) which Debian's
  # repo does not provide. See ADR-0053 for rationale.
  local key_url="https://packagecloud.io/crowdsec/crowdsec/gpgkey"
  local keyring="/etc/apt/keyrings/crowdsec.gpg"
  local sources_file="/etc/apt/sources.list.d/crowdsec.list"
  local source_line='deb [signed-by=/etc/apt/keyrings/crowdsec.gpg] https://packagecloud.io/crowdsec/crowdsec/any/ any main'

  install -d -m 0755 /etc/apt/keyrings

  if [[ ! -s "$keyring" ]]; then
    _log "fetching CrowdSec upstream signing key → $keyring"
    local tmp_key
    tmp_key="$(mktemp --tmpdir jabali-cs-key.XXXXXX)"
    if ! curl -fsSL --connect-timeout 10 -o "$tmp_key" "$key_url"; then
      rm -f "$tmp_key"
      _die "failed to fetch CrowdSec signing key from $key_url"
    fi
    gpg --batch --yes --dearmor -o "$keyring" "$tmp_key"
    rm -f "$tmp_key"
    chmod 0644 "$keyring"
  fi

  if [[ ! -f "$sources_file" ]] || ! grep -qF "$source_line" "$sources_file"; then
    _log "writing $sources_file"
    printf '%s\n' "$source_line" > "$sources_file"
    apt-get update -qq
  fi
}

install_crowdsec() {
  _log "configuring CrowdSec (upstream apt source for socket support, ADR-0053)"

  add_crowdsec_apt_source

  # If Debian-stock 1.4.x crowdsec was installed by an older install.sh
  # run (the previous deps batch listed `crowdsec` directly), upgrade to
  # the upstream version. apt-get install with the upstream repo enabled
  # picks the candidate from packagecloud automatically.
  if ! dpkg -s crowdsec >/dev/null 2>&1; then
    _spin "apt install crowdsec (upstream)" \
      apt-get install -y -qq --no-install-recommends crowdsec
  else
    local installed
    installed="$(dpkg-query -W -f='${Version}\n' crowdsec 2>/dev/null)"
    if [[ "$installed" == 1.4.* ]] || [[ "$installed" == 1.3.* ]]; then
      _log "upgrading crowdsec from $installed (Debian-stock) → upstream"
      _spin "apt upgrade crowdsec" \
        apt-get install -y -qq --only-upgrade crowdsec
    fi
  fi

  if ! command -v cscli >/dev/null 2>&1; then
    _die "cscli missing after upstream crowdsec install"
  fi

  # If a previous install left the package in `unpacked` state (postinst
  # never finished because an earlier drop-in pinned a config field the
  # stale 1.4.x agent couldn't parse), reconfigure now. Otherwise the
  # next systemctl restart fails before our drop-in even loads.
  local pkg_status
  pkg_status="$(dpkg-query -W -f='${Status}' crowdsec 2>/dev/null || true)"
  if [[ "$pkg_status" != *"installed"* ]]; then
    _log "crowdsec package status is '$pkg_status' — running dpkg --configure to finish postinst"
    dpkg --configure crowdsec || _warn "dpkg --configure crowdsec returned non-zero; continuing"
  fi

  # The hub index (/var/lib/crowdsec/hub/.index.json) must exist before
  # the agent starts — without it crowdsec FATALs with "invalid hub
  # index: unable to read index file". The package postinst tries to
  # download it but only on a successful first start; since our drop-in
  # may cause a chicken-and-egg restart loop, fetch the index explicitly
  # here. `cscli hub update` is idempotent — re-runs are a no-op when
  # the index is fresh.
  if [[ ! -f /var/lib/crowdsec/hub/.index.json ]]; then
    _log "downloading CrowdSec hub index (first install)"
    cscli hub update --error 2>&1 | sed 's/^/    /' || _warn "cscli hub update non-zero — surface via 'cscli hub update' for details"
  fi

  # Pick the firewall bouncer matching the kernel backend. Trixie+
  # defaults to nftables; bookworm uses iptables. apt-cache check guards
  # against packaging drift (both variants exist in upstream repo).
  local debian_rel bouncer_pkg
  debian_rel="$(lsb_release -rs 2>/dev/null | cut -d. -f1)"
  if [[ "$debian_rel" -ge 13 ]] && apt-cache show crowdsec-firewall-bouncer-nftables >/dev/null 2>&1; then
    bouncer_pkg="crowdsec-firewall-bouncer-nftables"
  else
    bouncer_pkg="crowdsec-firewall-bouncer-iptables"
  fi
  if ! dpkg -s "$bouncer_pkg" >/dev/null 2>&1; then
    _spin "apt install $bouncer_pkg" \
      apt-get install -y -qq --no-install-recommends "$bouncer_pkg"
  else
    _log "$bouncer_pkg already installed"
  fi

  # Patch /etc/crowdsec/config.yaml so LAPI binds on a Unix socket
  # (ADR-0050) at /run/crowdsec/api.sock instead of 127.0.0.1:8080
  # (which conflicts with Stalwart admin-http per ADR-0050). yq is the
  # Python jq-wrapper flavor (kislyuk/yq) on Debian — `-y -i` for
  # in-place YAML output.
  local cs_cfg="/etc/crowdsec/config.yaml"
  if [[ ! -f "$cs_cfg" ]]; then
    _die "crowdsec base package did not write $cs_cfg — abort before patching"
  fi
  local socket_path="/run/crowdsec/api.sock"
  # M27 fix — LAPI must ALSO listen on TCP loopback. The AppSec engine
  # authenticates incoming bouncer keys by calling LAPI itself via the
  # client URL in local_api_credentials.yaml. CrowdSec's HTTP client
  # doesn't parse a raw socket path as a URL — it concatenates as
  # `<socket>v1/decisions/stream` and bombs out with "unsupported
  # protocol scheme \"\"". Result: every nginx-bouncer → AppSec call
  # 401's silently. Adding a TCP listener + pointing the client URL
  # there fixes auth without removing the socket (cscli still works
  # over TCP, panel-agent still uses cscli unchanged).
  local lapi_tcp="127.0.0.1:8081"
  local current_socket current_uri
  current_socket="$(yq -r '.api.server.listen_socket // ""' "$cs_cfg" 2>/dev/null || echo "")"
  current_uri="$(yq -r '.api.server.listen_uri // ""' "$cs_cfg" 2>/dev/null || echo "")"
  if [[ "$current_socket" != "$socket_path" || "$current_uri" != "$lapi_tcp" ]]; then
    _log "patching $cs_cfg: listen_socket=$socket_path + listen_uri=$lapi_tcp"
    yq -y -i ".api.server.listen_socket = \"$socket_path\" | .api.server.listen_uri = \"$lapi_tcp\"" "$cs_cfg"
  else
    _log "$cs_cfg already pinned to socket $socket_path + tcp $lapi_tcp"
  fi

  # cscli + the in-process watcher both read
  # /etc/crowdsec/local_api_credentials.yaml for the LAPI endpoint. The
  # base package writes `url: http://127.0.0.1:8080` (Stalwart's port —
  # would crash the agent). M27 fix: point at the TCP loopback above so
  # the AppSec engine can parse it as a real URL. cscli works fine over
  # TCP loopback (verified on VM 192.168.100.150).
  local creds="/etc/crowdsec/local_api_credentials.yaml"
  local lapi_url="http://${lapi_tcp}/"
  if [[ -f "$creds" ]]; then
    local current_url
    current_url="$(yq -r '.url // ""' "$creds" 2>/dev/null || echo "")"
    if [[ "$current_url" != "$lapi_url" ]]; then
      _log "patching $creds: url = $lapi_url"
      yq -y -i ".url = \"$lapi_url\"" "$creds"
    fi
  fi

  # systemd drop-in: RuntimeDirectory creates /run/crowdsec (cleared on
  # stop), Group=jabali so panel-api (group jabali) can talk to LAPI
  # via cscli. Mode 0750 on the runtime dir + service-managed socket
  # mode (CrowdSec sets 0660 on the socket itself).
  local dropin_dir="/etc/systemd/system/crowdsec.service.d"
  local dropin="$dropin_dir/10-jabali-socket.conf"
  local desired_dropin=$'# Managed by jabali install.sh — M26. Do NOT hand-edit.\n# Pins CrowdSec LAPI to /run/crowdsec/api.sock so panel-api (group\n# jabali) can reach it via cscli without TCP loopback (ADR-0050).\n# ExecStartPost pins the socket to 0660 jabali (CrowdSec creates it\n# at 0755 by default which leaks connect(2) reach to any local user).\n[Service]\nRuntimeDirectory=crowdsec\nRuntimeDirectoryMode=0750\nGroup=jabali\nExecStartPost=/bin/sh -c \'until [ -S /run/crowdsec/api.sock ]; do sleep 0.1; done\'\nExecStartPost=/bin/chmod 0660 /run/crowdsec/api.sock\nExecStartPost=/bin/chgrp jabali /run/crowdsec/api.sock\n'
  install -d -m 0755 "$dropin_dir"
  if [[ ! -f "$dropin" ]] || ! cmp -s <(printf '%s' "$desired_dropin") "$dropin"; then
    _log "writing $dropin"
    local tmp
    tmp="$(mktemp --tmpdir jabali-cs-dropin.XXXXXX)"
    printf '%s' "$desired_dropin" >"$tmp"
    install -m 0644 -o root -g root "$tmp" "$dropin"
    rm -f "$tmp"
    systemctl daemon-reload
    systemctl restart crowdsec
  elif ! systemctl is-active --quiet crowdsec; then
    systemctl start crowdsec
  else
    _log "crowdsec drop-in already current — no restart needed"
  fi

  # Wait for the LAPI socket to come up. crowdsec.service reports active
  # the moment the systemd cgroup spawns; the LAPI socket appears a beat
  # later as the agent goroutine binds.
  local i
  for i in 1 2 3 4 5 6 7 8 9 10; do
    if [[ -S "$socket_path" ]]; then break; fi
    sleep 1
  done
  if [[ ! -S "$socket_path" ]]; then
    _die "$socket_path did not appear after CrowdSec restart; check journalctl -u crowdsec"
  fi

  if cscli lapi status >/dev/null 2>&1; then
    _ok "CrowdSec LAPI live at $socket_path"
  else
    _warn "cscli lapi status non-zero — surface via 'cscli lapi status' for details"
  fi
}

install_crowdsec_appsec() {
  # Optional AppSec layer — lets the admin Security tab push a
  # server-wide country allow/deny list enforced at L7. See
  # https://doc.crowdsec.net/docs/next/appsec/rules_examples/#5-geoblocking.
  # Install side:
  #   1. geoip-enrich parser so the runtime has Country.IsoCode
  #   2. appsec-virtual-patching collection (vpatch-* CVE rules + base-config)
  #   3. /etc/crowdsec/appsec-configs/jabali-appsec.yaml — our own
  #      appsec-CONFIG that loads vpatch-* AND carries the geoblock
  #      pre_eval hook. M27 fix: pre_eval lives in appsec-CONFIG (not
  #      appsec-rules) per upstream docs:
  #      https://doc.crowdsec.net/docs/next/appsec/rules_examples/
  #      Earlier shipped a /etc/crowdsec/appsec-rules/jabali-geoblock.yaml
  #      with pre_eval — it loaded as a rule but the hook never fired
  #      because rules use a different schema.
  #   4. /etc/crowdsec/acquis.d/jabali-appsec.yaml — AppSec listener on
  #      127.0.0.1:7422 (TCP loopback, same posture as Stalwart admin-http
  #      127.0.0.1:8080). Unix socket would be stricter but the upstream
  #      crowdsec-nginx-bouncer's Lua HTTP client doesn't speak unix.
  # Nginx enforcement ships via install_crowdsec_nginx_bouncer below
  # (the upstream crowdsec-nginx-bouncer package). Every vhost gets
  # AppSec evaluation automatically — no per-vhost snippet required.
  _log "configuring CrowdSec AppSec (server-wide geoblock rule)"

  # 1. GeoIP enricher — prereq for GeoIPEnrich expr.
  if ! cscli parsers list 2>/dev/null | grep -q 'crowdsecurity/geoip-enrich'; then
    _spin "cscli parsers install geoip-enrich" \
      cscli parsers install crowdsecurity/geoip-enrich
  fi

  # 2. AppSec base collections — virtual-patching gives us vpatch-*
  #    CVE rules + base-config plumbing; appsec-generic-rules adds CRS-style
  #    SSTI / WordPress upload / no-user-agent detection (enabled by default
  #    2026-04-26 — see plans/m27-crowdsec-extensions.md). Both are free
  #    upstream collections.
  if ! cscli collections list 2>/dev/null | grep -q 'crowdsecurity/appsec-virtual-patching'; then
    _spin "cscli collections install appsec-virtual-patching" \
      cscli collections install crowdsecurity/appsec-virtual-patching
  fi
  if ! cscli collections list 2>/dev/null | grep -q 'crowdsecurity/appsec-generic-rules'; then
    _spin "cscli collections install appsec-generic-rules" \
      cscli collections install crowdsecurity/appsec-generic-rules --force
  fi

  # WordPress collection — wp-login brute force, xmlrpc abuse,
  # plugin/theme CVE exploits. Installed by default since jabali ships
  # WordPress as the primary 1-click app (M10) and ~80% of tenant sites
  # are WP-based. Operators can remove via the Recommended hub picker
  # if they don't run WP.
  if ! cscli collections list 2>/dev/null | grep -q 'crowdsecurity/wordpress'; then
    _spin "cscli collections install wordpress" \
      cscli collections install crowdsecurity/wordpress --force
  fi

  # 3. Jabali AppSec config — our own appsec-CONFIG file. Loads
  #    base-config + vpatch-* + generic-* plus carries the geoblock
  #    pre_eval hook. The agent rewrites this file on every admin Apply
  #    (see panel-agent's csAppSecGeoblockSetHandler). Shipped initial
  #    state has NO pre_eval block (mode=off — geoblock stays opt-in).
  local configs_dir="/etc/crowdsec/appsec-configs"
  install -d -m 0755 "$configs_dir"
  local config_file="$configs_dir/jabali-appsec.yaml"
  local desired_config=$'# Managed by jabali — M27 AppSec config.\n# DO NOT hand-edit. Set via the admin Security \xe2\x86\x92 CrowdSec tab OR\n# POST /api/v1/admin/security/crowdsec/appsec/geoblock.\n# jabali-mode: off\n# jabali-countries:\nname: crowdsecurity/jabali-appsec\ndefault_remediation: ban\ninband_rules:\n - crowdsecurity/base-config\n - crowdsecurity/vpatch-*\n - crowdsecurity/generic-*\n'
  if [[ ! -f "$config_file" ]]; then
    _log "seeding $config_file (mode=off)"
    local tmp
    tmp="$(mktemp --tmpdir jabali-appsec-config.XXXXXX)"
    printf '%s' "$desired_config" >"$tmp"
    install -m 0644 -o root -g root "$tmp" "$config_file"
    rm -f "$tmp"
  elif ! grep -q 'crowdsecurity/generic-\*' "$config_file" && ! grep -q '# jabali-mode:' "$config_file"; then
    : # operator-edited or already migrated; skip
  elif ! grep -q 'crowdsecurity/generic-\*' "$config_file"; then
    # Existing install: append generic-* to inband_rules without
    # disturbing operator-set jabali-mode/countries header. Insert
    # before the closing of inband_rules block — appended at end of
    # the rule list (yaml parses fine either way).
    if grep -qE '^[[:space:]]*-[[:space:]]+crowdsecurity/vpatch' "$config_file"; then
      _log "appending crowdsecurity/generic-* to $config_file inband_rules"
      sed -i '/^[[:space:]]*-[[:space:]]\+crowdsecurity\/vpatch-\*[[:space:]]*$/a\ - crowdsecurity/generic-*' "$config_file"
    fi
  fi

  # Cleanup: M26-era /etc/crowdsec/appsec-rules/jabali-geoblock.yaml is
  # superseded by the appsec-config above. Schema was wrong (pre_eval in
  # a rule file is silently ignored).
  if [[ -f /etc/crowdsec/appsec-rules/jabali-geoblock.yaml ]]; then
    _log "removing legacy /etc/crowdsec/appsec-rules/jabali-geoblock.yaml"
    rm -f /etc/crowdsec/appsec-rules/jabali-geoblock.yaml
  fi

  # 4. AppSec acquisition — listener on 127.0.0.1:7422 (the CrowdSec
  #    convention; bouncer talks to it over loopback). Points at our
  #    jabali-appsec config (NOT virtual-patching) so the agent can
  #    inject the geoblock pre_eval hook.
  local acquis_dir="/etc/crowdsec/acquis.d"
  install -d -m 0755 "$acquis_dir"
  local acquis_file="$acquis_dir/jabali-appsec.yaml"
  local desired_acquis=$'# Managed by jabali install.sh — M27 AppSec geoblock.\n# TCP loopback listener. crowdsec-nginx-bouncer dials this via\n# APPSEC_URL=http://127.0.0.1:7422. Not exposed outside the host.\nappsec_config: crowdsecurity/jabali-appsec\nlabels:\n  type: appsec\nlisten_addr: 127.0.0.1:7422\nsource: appsec\n'
  if [[ ! -f "$acquis_file" ]] || ! cmp -s <(printf '%s' "$desired_acquis") "$acquis_file"; then
    _log "writing $acquis_file"
    local tmp
    tmp="$(mktemp --tmpdir jabali-appsec-acquis.XXXXXX)"
    printf '%s' "$desired_acquis" >"$tmp"
    install -m 0644 -o root -g root "$tmp" "$acquis_file"
    rm -f "$tmp"
  fi

  # Remove legacy appsec.sock ExecStartPost lines if the previous
  # socket-based install left them — they'd block startup now that
  # AppSec binds TCP (the `until [ -S ... ]` loop would never fire).
  local dropin="/etc/systemd/system/crowdsec.service.d/10-jabali-socket.conf"
  if [[ -f "$dropin" ]] && grep -q 'appsec.sock' "$dropin"; then
    _log "purging legacy appsec.sock ExecStartPost from $dropin"
    sed -i '/appsec\.sock/d' "$dropin"
    systemctl daemon-reload
  fi

  # Reload or restart to pick up acquis + config changes.
  systemctl reload crowdsec 2>/dev/null || systemctl restart crowdsec

  # Wait for AppSec TCP listener to come up — `ss -lnt sport = :7422`
  # is the signal the goroutine bound. Cap at 10s.
  local i
  for i in 1 2 3 4 5 6 7 8 9 10; do
    if ss -lnt 'sport = :7422' 2>/dev/null | grep -q 127.0.0.1; then break; fi
    sleep 1
  done
  if ss -lnt 'sport = :7422' 2>/dev/null | grep -q 127.0.0.1; then
    _ok "CrowdSec AppSec live at 127.0.0.1:7422"
  else
    _warn "CrowdSec AppSec listener did not appear on :7422 — check journalctl -u crowdsec"
  fi
}

install_crowdsec_nginx_bouncer() {
  # Upstream crowdsec-nginx-bouncer (Lua-based access_by_lua_block)
  # wires every HTTP request into the AppSec engine automatically —
  # no per-vhost auth_request snippet needed. See ADR-0060.
  #
  # Configuration uses `API_URL=""` + `APPSEC_URL=http://127.0.0.1:7422`
  # so the bouncer is AppSec-only. LAPI-sourced L3/L4 decisions stay
  # the province of crowdsec-firewall-bouncer-nftables (installed in
  # install_crowdsec) — running nginx-bouncer alongside firewall-
  # bouncer for LAPI decisions would double-enforce with no added
  # benefit.
  _log "configuring crowdsec-nginx-bouncer (AppSec enforcement)"

  if ! dpkg -s crowdsec-nginx-bouncer >/dev/null 2>&1; then
    _spin "apt install crowdsec-nginx-bouncer" \
      apt-get install -y -qq --no-install-recommends crowdsec-nginx-bouncer
  else
    _log "crowdsec-nginx-bouncer already installed"
  fi

  local bouncer_conf="/etc/crowdsec/bouncers/crowdsec-nginx-bouncer.conf"
  if [[ ! -f "$bouncer_conf" ]]; then
    _warn "$bouncer_conf missing after package install — bouncer postinst may have failed"
    return
  fi

  # Mint an API key via cscli if one isn't already registered for us.
  # Bouncer name pinned (not SUFFIX-randomised) so repeated install
  # runs don't accumulate stale bouncers.
  local bouncer_name="jabali-nginx"
  local api_key
  if cscli bouncers list -o json 2>/dev/null | python3 -c "import json,sys; [sys.exit(0) for b in json.load(sys.stdin) if b.get('name')=='$bouncer_name'] or sys.exit(1)" 2>/dev/null; then
    # Bouncer exists — reuse the key already in the config file (the
    # package postinst or our previous run set it).
    api_key="$(awk -F= '/^API_KEY=/{print $2; exit}' "$bouncer_conf" | tr -d '[:space:]')"
    if [[ -z "$api_key" ]]; then
      _log "bouncer '$bouncer_name' exists but API_KEY missing in conf — rotating"
      cscli bouncers delete "$bouncer_name" >/dev/null 2>&1 || true
      api_key="$(cscli bouncers add "$bouncer_name" -o raw 2>/dev/null)"
    fi
  else
    _log "registering '$bouncer_name' bouncer with LAPI"
    api_key="$(cscli bouncers add "$bouncer_name" -o raw 2>/dev/null)"
  fi
  if [[ -z "$api_key" ]]; then
    _warn "cscli bouncers add failed — $bouncer_conf left unmanaged"
    return
  fi

  # Rewrite bouncer config. API_URL empty = skip LAPI polling;
  # APPSEC_URL + ALWAYS_SEND_TO_APPSEC=true = every request goes
  # through AppSec pre_eval. APPSEC_FAILURE_ACTION=passthrough means
  # an AppSec outage doesn't 403 every request.
  local desired_conf
  desired_conf=$(cat <<EOF
# Managed by jabali install.sh — M26 AppSec enforcement.
# DO NOT hand-edit. Re-run install.sh to rotate API_KEY.
ENABLED=true
API_URL=
API_KEY=$api_key
USE_TLS_AUTH=false
CACHE_EXPIRATION=1
BOUNCING_ON_TYPE=all
FALLBACK_REMEDIATION=ban
REQUEST_TIMEOUT=3000
UPDATE_FREQUENCY=10
ENABLE_INTERNAL=false
MODE=live
EXCLUDE_LOCATION=
BAN_TEMPLATE_PATH=/var/lib/crowdsec/lua/templates/ban.html
REDIRECT_LOCATION=
RET_CODE=
CAPTCHA_PROVIDER=
SECRET_KEY=
SITE_KEY=
CAPTCHA_TEMPLATE_PATH=/var/lib/crowdsec/lua/templates/captcha.html
CAPTCHA_EXPIRATION=3600
APPSEC_URL=http://127.0.0.1:7422
APPSEC_FAILURE_ACTION=passthrough
APPSEC_CONNECT_TIMEOUT=1000
APPSEC_SEND_TIMEOUT=1000
APPSEC_PROCESS_TIMEOUT=2000
ALWAYS_SEND_TO_APPSEC=true
SSL_VERIFY=false
EOF
  )
  local tmp
  tmp="$(mktemp --tmpdir jabali-nginx-bouncer.XXXXXX)"
  printf '%s\n' "$desired_conf" >"$tmp"
  if ! cmp -s "$tmp" "$bouncer_conf"; then
    _log "writing $bouncer_conf"
    install -m 0600 -o root -g root "$tmp" "$bouncer_conf"
  fi
  rm -f "$tmp"

  # Validate + reload nginx. The bouncer package drops
  # /etc/nginx/conf.d/crowdsec_nginx.conf; nginx-t catches any
  # postinst that didn't run (Debian quirk after kernel update).
  if nginx -t >/dev/null 2>&1; then
    systemctl reload nginx || _warn "nginx reload failed — check 'systemctl status nginx'"
    _ok "crowdsec-nginx-bouncer configured (AppSec enforcement on every vhost)"
  else
    _warn "nginx -t failed after bouncer install — surface via 'nginx -t'"
  fi

  # M27 — captcha template sanity check. crowdsec-nginx-bouncer ships
  # ban.html + captcha.html in /var/lib/crowdsec/lua/templates/.
  # If they're missing the Step 5 captcha toggle still writes the
  # bouncer conf but remediation would render blank; surface as warn
  # rather than fail (package regression is upstream's problem).
  local tmpl_dir="/var/lib/crowdsec/lua/templates"
  for tmpl in ban.html captcha.html; do
    if [[ ! -f "$tmpl_dir/$tmpl" ]]; then
      _warn "$tmpl_dir/$tmpl missing — captcha remediation will render empty until reinstalled"
    fi
  done
}

install_crowdsec_profiles() {
  # M27 — defensive stub. The crowdsec Debian package ships
  # /etc/crowdsec/profiles.yaml with five upstream default profiles
  # (default_ip_remediation, default_range_remediation, etc.). If
  # it's missing we seed a minimal ban-all profile so Step 6's
  # marker-bounded rewrite always has a base file to slot into.
  # Idempotent — no-op when file exists.
  local profiles=/etc/crowdsec/profiles.yaml
  if [[ -f "$profiles" ]]; then return 0; fi
  _warn "$profiles missing after crowdsec package install — seeding minimal fallback"
  local tmp
  tmp="$(mktemp --tmpdir jabali-profiles.XXXXXX)"
  cat >"$tmp" <<'EOF'
name: default_ip_remediation
filters:
 - Alert.Remediation == true && Alert.GetScope() == "Ip"
decisions:
 - type: ban
   duration: 4h
on_success: break
EOF
  install -m 0644 -o root -g root "$tmp" "$profiles"
  rm -f "$tmp"
  systemctl reload crowdsec 2>/dev/null || true
}

cleanup_modsecurity() {
  # ADR-0055 SUPERSEDED 2026-04-26 — CrowdSec AppSec covers the WAF role.
  # Active cleanup so existing hosts that installed M26 ModSecurity drop
  # the dead nginx module + CRS rules + main include on `jabali update`.
  # Idempotent: bails fast when packages already gone and no leftover files.
  local pkgs_present=0
  if dpkg -l 2>/dev/null | grep -qE '^ii\s+(libnginx-mod-http-modsecurity|modsecurity-crs)\s'; then
    pkgs_present=1
  fi
  if [[ "$pkgs_present" == "0" && ! -d /etc/nginx/modsec && ! -e /etc/nginx/modsecurity.conf ]]; then
    return 0
  fi

  _log "removing ModSecurity (ADR-0055 superseded by CrowdSec AppSec)"
  if [[ "$pkgs_present" == "1" ]]; then
    DEBIAN_FRONTEND=noninteractive apt-get -y \
      -o Dpkg::Lock::Timeout=300 \
      remove --purge libnginx-mod-http-modsecurity modsecurity-crs >/dev/null 2>&1 || true
  fi

  # Sweep leftover nginx config + module symlinks. The apt purge usually
  # handles modules-enabled/*.conf already, but operators sometimes
  # symlinked manually — wipe both.
  rm -f /etc/nginx/modules-enabled/*modsecurity* 2>/dev/null || true
  rm -f /etc/nginx/modsecurity.conf 2>/dev/null || true
  rm -rf /etc/nginx/modsec 2>/dev/null || true
  rm -rf /etc/modsecurity 2>/dev/null || true

  if nginx -t >/dev/null 2>&1; then
    if systemctl is-active --quiet nginx; then
      systemctl reload nginx
    fi
    _ok "ModSecurity removed; nginx config still valid"
  else
    _warn "nginx -t failed after ModSecurity cleanup — first relevant error:"
    nginx -t 2>&1 | head -10 >&2
  fi
}

install_ufw() {
  _log "configuring UFW (package installed in base batch)"

  if ! command -v ufw >/dev/null 2>&1; then
    _die "ufw missing after apt install — install_base_packages did not install it"
  fi

  # Default policies. `ufw default <verb>` is idempotent — re-running a
  # second time prints "Default incoming policy changed to 'deny'" only
  # on actual change.
  ufw default deny incoming >/dev/null
  ufw default allow outgoing >/dev/null

  # Allow-list: SSH, web (panel + nginx), mail (Stalwart), DNS (PowerDNS
  # authoritative, TCP for AXFR + large UDP responses, UDP for normal
  # queries). MUST be in place BEFORE `ufw enable` runs in the same
  # install — otherwise default-deny locks out SSH the moment the
  # firewall activates.
  local port
  for port in 22 80 443 8443 25 465 587 993 995 4190 53; do
    # `ufw allow N/tcp` is idempotent — second invocation prints
    # "Skipping adding existing rule" but exits 0.
    ufw allow "${port}/tcp" >/dev/null
  done
  # DNS authoritative also needs UDP/53. Recursor + systemd-resolved
  # bind loopback only, so the wildcard UFW rule won't expose them.
  ufw allow 53/udp >/dev/null

  # Idempotent enable: bare `ufw --force enable` reloads the firewall
  # mid-install which can race in-flight TCP (apt mirror reuse, the
  # Stalwart bind that happens later in this same script). Guard on
  # `ufw status` reporting the firewall as actually active — NOT on
  # `systemctl is-active ufw`, because the ufw.service unit can be
  # reported active by systemd while the firewall itself is Status:
  # inactive (the service only loads rules at boot; a fresh host where
  # ufw was never enabled has the unit "active" but no rules applied).
  # Observed on Debian 13 minimal where the rules-sync block above
  # appended to user.rules while iptables stayed empty.
  if ! ufw status 2>/dev/null | grep -q '^Status: active'; then
    _log "enabling UFW for the first time"
    ufw --force enable >/dev/null
  else
    _log "UFW already active — skipping enable (rules synced above)"
  fi

  # Verify the allow-list landed and SSH is still in it. If SSH dropped
  # off, the next reboot would lock the operator out — fail the install.
  # Grep is lenient across UFW versions: rule line may render as
  # "22/tcp ALLOW ...", "22 (v6) ALLOW ...", or "22 ALLOW ..." on Debian
  # 13's ufw 0.36.x. Anchor on "ALLOW" and require a 22 token on the
  # To-column rather than relying on a specific /tcp suffix.
  if ! ufw status verbose 2>/dev/null \
       | awk '/ALLOW/ && ($1 == "22" || $1 == "22/tcp" || $1 ~ /^22\/tcp/)' \
       | grep -q .; then
    ufw status verbose 2>&1 >&2 || true
    _die "UFW allow rule for port 22 missing after install — refusing to leave operator at risk of SSH lockout (status dumped above)"
  fi
  _ok "UFW active; default-deny incoming with allow-list (22, 80, 443, 8443, 25, 465, 587, 993, 995, 4190, 53/tcp+udp)"
}

# ---------- step 8a.1: auto-restart drop-ins for critical services ----------
#
# Third-party packages ship with inconsistent Restart= defaults — some have
# `Restart=on-failure`, some `on-abnormal` (mariadb: restarts only on crash
# signals, NOT on non-zero exit), some omit it entirely. A stock Debian 13
# install can leave nginx, pdns, pdns-recursor, redis-server, crowdsec, and
# the crowdsec-firewall-bouncer with NO auto-restart at all, so a transient
# crash (OOM, disk spike, config reload race) bricks the service until the
# operator notices.
#
# Write a uniform drop-in that:
#   - Restart=on-failure   → restart on non-zero exit, NOT on manual stop
#   - RestartSec=5s        → short backoff, same as our jabali-* units
#   - StartLimitBurst=10   → tolerate 10 failures in the burst window
#                             (default 5 gave up too fast during a flap)
#   - StartLimitIntervalSec=60s → reset counter after 60s of stability
#
# Drop-in only — does NOT overwrite the package unit, so apt upgrades keep
# working. Idempotent: only daemon-reloads if the file content changed.
#
# sshd intentionally excluded — a bad sshd config shouldn't auto-retry
# forever and trap the operator (who may have just pushed a broken
# sshd_config.d drop-in). Manual restart is the correct failure mode there.
install_restart_drop_ins() {
  _log "installing Restart=on-failure drop-ins for critical services"

  local units=(
    nginx.service
    mariadb.service
    pdns.service
    pdns-recursor.service
    redis-server.service
    crowdsec.service
    systemd-resolved.service
  )

  # crowdsec-firewall-bouncer is package-variant-named. Pick whichever
  # exists (iptables/nftables/pf variants ship as different unit names).
  local cs_bouncer
  for cs_bouncer in crowdsec-firewall-bouncer-iptables.service \
                    crowdsec-firewall-bouncer-nftables.service \
                    crowdsec-firewall-bouncer.service; do
    if systemctl cat "$cs_bouncer" >/dev/null 2>&1; then
      units+=("$cs_bouncer")
      break
    fi
  done

  local changed=0 unit dropin_dir dropin dropin_new
  for unit in "${units[@]}"; do
    # Skip units the host doesn't have (e.g. a fresh box where one of
    # these wasn't installed for some reason — don't fail the install
    # over an optional dependency).
    if ! systemctl cat "$unit" >/dev/null 2>&1; then
      _warn "unit $unit not present on host; skipping auto-restart drop-in"
      continue
    fi
    dropin_dir="/etc/systemd/system/${unit}.d"
    dropin="${dropin_dir}/10-jabali-restart.conf"
    dropin_new="${dropin}.new"
    install -d -m 0755 "$dropin_dir"
    cat > "$dropin_new" <<'RESTARTCONF'
# Managed by jabali-panel install.sh. Uniform auto-restart policy for
# critical third-party services so a transient crash self-heals instead
# of waiting for the operator to notice. See install_restart_drop_ins()
# in install.sh for rationale. Hand edits will be overwritten on the
# next install.sh / `jabali update` run.
[Unit]
StartLimitBurst=10
StartLimitIntervalSec=60s

[Service]
Restart=on-failure
RestartSec=5s
RESTARTCONF
    if [[ -f "$dropin" ]] && cmp -s "$dropin" "$dropin_new"; then
      rm -f "$dropin_new"
    else
      mv "$dropin_new" "$dropin"
      chmod 0644 "$dropin"
      changed=1
      _log "wrote ${dropin}"
    fi
  done

  if [[ "$changed" == "1" ]]; then
    systemctl daemon-reload
  fi
  _ok "auto-restart drop-ins installed for ${#units[@]} critical services"
}

# ---------- step 8b: Stalwart Mail Server + Bulwark webmail (M6) -------------
#
# Two functions, one tool each. Both are disabled-by-default — systemd units
# are installed but the services are enabled on first panel
# domain.email_enable call (from the agent). install.sh re-runs are
# idempotent: binaries are re-downloaded only on version bump, service-
# account users + data dirs + secrets are preserved across runs.
#
# Layout established here (plan §1 Step 1, ADR-0041):
#   /opt/stalwart/                      — extracted Stalwart binary
#   /usr/local/bin/stalwart             — symlink
#   /var/lib/stalwart/                  — RocksDB mail storage (jabali-mail:jabali-mail, 0750)
#   /etc/stalwart/config.toml           — rendered skeleton (Step 2 fills directory block)
#   /etc/jabali-panel/dkim/             — Ed25519 DKIM keys (jabali:jabali, 0750)
#   /etc/jabali-panel/stalwart-admin.token — JMAP admin bearer (jabali:jabali-mail, 0640)
#   /opt/jabali-webmail/                — Bulwark Next.js source + build output
#   /var/lib/jabali-webmail/settings/   — Bulwark settings-sync data dir
#   /etc/jabali-panel/bulwark.env       — Bulwark runtime env (jabali-webmail:jabali-webmail, 0640)
#   /etc/jabali-panel/bulwark-session.key — Bulwark SESSION_SECRET (jabali-webmail:jabali-webmail, 0640)

install_stalwart() {
  local stalwart_version="0.16.0"
  _log "installing Stalwart Mail Server (v${stalwart_version})"

  # Ensure service user + group exist. Supplementary group `jabali` lets
  # Stalwart read /etc/jabali-panel/stalwart-admin.token (0640 jabali:jabali-mail).
  if ! getent passwd jabali-mail >/dev/null 2>&1; then
    _log "creating jabali-mail service user"
    useradd --system --no-create-home --shell /usr/sbin/nologin \
      --user-group jabali-mail
    usermod -a -G "$SERVICE_USER" jabali-mail
  fi

  # Data + config dirs.
  install -d -m 0750 -o jabali-mail -g jabali-mail /var/lib/stalwart
  install -d -m 0750 -o jabali-mail -g jabali-mail /etc/stalwart
  install -d -m 0750 -o "$SERVICE_USER" -g "$SERVICE_USER" /etc/jabali-panel/dkim

  local stalwart_binary="/usr/local/bin/stalwart"
  local stalwart_install_dir="/opt/stalwart"

  # Idempotence: skip re-download if the installed binary reports the
  # target version. Stalwart's version command output format is stable
  # across 0.14.x-0.16.x ("Stalwart Mail Server v0.16.0").
  if [[ -x "$stalwart_binary" ]]; then
    local installed_version
    installed_version=$("$stalwart_binary" --version 2>&1 | grep -oP 'v\K[0-9]+\.[0-9]+\.[0-9]+' | head -n1 || echo "unknown")
    if [[ "$installed_version" == "$stalwart_version" ]]; then
      _ok "Stalwart $stalwart_version already installed"
    else
      _warn "upgrading Stalwart $installed_version -> $stalwart_version"
      _install_stalwart_binary "$stalwart_version"
    fi
  else
    _install_stalwart_binary "$stalwart_version"
  fi

  # stalwart-cli is the v0.16 management surface (ADR-0045). Install
  # alongside the server so apply-plan.json can be provisioned during
  # the bootstrap step (follow-up commit). Version-pin independently of
  # the server binary.
  _install_stalwart_cli

  # JMAP admin token — used later for panel <-> Stalwart management auth
  # (JMAP basic auth with stored credential). Generated once + preserved
  # across re-runs so a re-install doesn't break the panel-agent's auth.
  local admin_token_file="/etc/jabali-panel/stalwart-admin.token"
  if [[ ! -f "$admin_token_file" ]]; then
    _log "generating Stalwart admin token -> $admin_token_file"
    umask 077
    openssl rand -base64 32 >"$admin_token_file"
    chmod 0640 "$admin_token_file"
    chown "$SERVICE_USER":jabali-mail "$admin_token_file"
  else
    _ok "Stalwart admin token already present"
  fi

  # MariaDB read-only password for Stalwart's SQL directory lookups.
  # Generated here (needed for config.json template rendering below), but
  # the actual CREATE USER + GRANT happens in install_stalwart_apply after
  # start_and_verify — migration 000054 creates the mailboxes table and
  # GRANT SELECT on a non-existent table is a fatal error (ERROR 1146).
  local stalwart_db_pw_file="/etc/jabali-panel/stalwart-mariadb.password"
  if [[ ! -f "$stalwart_db_pw_file" ]]; then
    _log "generating Stalwart MariaDB password -> $stalwart_db_pw_file"
    umask 077
    openssl rand -hex 32 >"$stalwart_db_pw_file"
    chmod 0640 "$stalwart_db_pw_file"
    chown root:jabali-mail "$stalwart_db_pw_file"
  fi
  local stalwart_db_pass
  stalwart_db_pass="$(cat "$stalwart_db_pw_file")"

  # Render /etc/stalwart/config.json from template. v0.16's config.json
  # is just a single tagged-enum `DataStore` descriptor for the REGISTRY
  # store (ADR-0045); it holds settings, directories, listeners, DKIM
  # etc. All mail storage / SQL directory backends are JMAP objects
  # inside that registry, applied via `stalwart-cli apply`. Template
  # therefore has no mustaches — but install.sh still runs the mustache
  # sanity check to protect against future template drift.
  local stalwart_config="/etc/stalwart/config.json"
  if [[ ! -f "${REPO_DIR}/install/stalwart/config.json.tmpl" ]]; then
    _die "Stalwart config template not found at ${REPO_DIR}/install/stalwart/config.json.tmpl"
  fi
  install -m 0640 -o jabali-mail -g jabali-mail \
    "${REPO_DIR}/install/stalwart/config.json.tmpl" "$stalwart_config"

  if grep -q '{{\..*}}' "$stalwart_config"; then
    _die "unsubstituted mustaches in $stalwart_config — template drift?"
  fi
  _ok "Stalwart datastore config at $stalwart_config"

  # stalwart.env — systemd EnvironmentFile. Populated with
  # STALWART_RECOVERY_ADMIN=admin:<stalwart-admin.token> so Stalwart
  # accepts Basic-auth calls from the panel-agent (ADR-0045 §Bootstrap).
  # Written/rewritten on every install run so a rotated admin token
  # propagates into the unit after a `jabali update`.
  local stalwart_env="/etc/jabali-panel/stalwart.env"
  local stalwart_admin_token
  stalwart_admin_token="$(cat "$admin_token_file")"
  cat >"$stalwart_env" <<EOF
# Stalwart Mail Server — systemd EnvironmentFile.
# Managed by install.sh. Do NOT hand-edit.
# STALWART_RECOVERY_ADMIN seeds an admin principal Stalwart accepts for
# HTTP Basic auth against /jmap; paired with the token at
# ${admin_token_file} the panel-agent uses for every management call.
STALWART_RECOVERY_ADMIN=admin:${stalwart_admin_token}
EOF
  chmod 0640 "$stalwart_env"
  chown root:jabali-mail "$stalwart_env"
  _ok "Stalwart env written (admin seed) at $stalwart_env"

  # Render /etc/jabali-panel/stalwart-apply-plan.json from template.
  # This is the JMAP declarative plan (ADR-0045) that seeds the
  # SqlDirectory + listeners + Authentication pointer. Rendered every
  # run; stalwart-cli apply is idempotent against already-applied state.
  local stalwart_apply_plan="/etc/jabali-panel/stalwart-apply-plan.json"
  if [[ ! -f "${REPO_DIR}/install/stalwart/apply-plan.json.tmpl" ]]; then
    _die "Stalwart apply plan template not found at ${REPO_DIR}/install/stalwart/apply-plan.json.tmpl"
  fi
  sed -e "s|{{\.MariaDBPassword}}|${stalwart_db_pass}|g" \
    "${REPO_DIR}/install/stalwart/apply-plan.json.tmpl" >"$stalwart_apply_plan"
  chown root:jabali-mail "$stalwart_apply_plan"
  chmod 0640 "$stalwart_apply_plan"
  if grep -q '{{\..*}}' "$stalwart_apply_plan"; then
    _die "unsubstituted mustaches in $stalwart_apply_plan — template drift?"
  fi
  _ok "Stalwart apply plan at $stalwart_apply_plan"

  # Systemd unit — installed then started + applied. We start on install
  # (not lazy on first domain.email_enable) because applying the plan
  # requires a running /jmap endpoint; the bootstrap sequence is:
  #
  #   1. install/update the unit
  #   2. systemctl daemon-reload
  #   3. systemctl enable --now jabali-stalwart
  #   4. poll 127.0.0.1:8446/jmap/session until 2xx/4xx (ready)
  #   5. stalwart-cli apply --file <plan>
  #
  # Ports 25/465/587/993 will bind on step 3. On a host with no
  # email-enabled domains this is an idle listener — Stalwart 550s
  # any inbound recipient until a Domain object exists in the registry
  # (which domain.email_enable creates via JMAP on first enable).
  if [[ ! -f "${REPO_DIR}/install/systemd/jabali-stalwart.service" ]]; then
    _die "Stalwart systemd unit not found at ${REPO_DIR}/install/systemd/jabali-stalwart.service"
  fi
  install -m 0644 -o root -g root "${REPO_DIR}/install/systemd/jabali-stalwart.service" \
    /etc/systemd/system/jabali-stalwart.service
  systemctl daemon-reload
  _ok "jabali-stalwart.service installed (apply deferred to install_stalwart_apply)"
}

# install_stalwart_apply — second phase of Stalwart bootstrap. Runs AFTER
# start_and_verify so that jabali-panel.service has applied migration
# 000054 (which creates jabali_panel.mailboxes + jabali_panel.domains).
# This phase:
#   1. Creates the jabali-stalwart-ro MariaDB user + SELECT grants
#   2. Enables + starts jabali-stalwart.service
#   3. Polls /jmap until ready
#   4. Runs stalwart-cli apply against the rendered plan
#
# Split out from install_stalwart (ADR-0045 bootstrap flow) because step 1
# requires the mailboxes table to already exist — migrations run inside
# the panel service on first start, not up-front in install.sh.
install_stalwart_apply() {
  _log "provisioning Stalwart MariaDB user + applying JMAP plan"

  # M25 Step 7: Stalwart seeds factory NetworkListeners into RocksDB on
  # first start (http at [::]:8080, https at [::]:443). stalwart-cli
  # apply is create-only and cannot remove them. _install_stalwart_apply_plan
  # calls _delete_stalwart_factory_listeners to remove them via the API
  # before restarting. See ADR-0050 §"Factory listener problem".

  local stalwart_db_user="jabali-stalwart-ro"
  local stalwart_db_pw_file="/etc/jabali-panel/stalwart-mariadb.password"
  if [[ ! -f "$stalwart_db_pw_file" ]]; then
    _die "Stalwart MariaDB password file missing at $stalwart_db_pw_file (install_stalwart must run first)"
  fi
  local stalwart_db_pass
  stalwart_db_pass="$(cat "$stalwart_db_pw_file")"

  # SELECT-only grant. Stalwart never writes to the source-of-truth
  # directory; on-every-auth `synchronize_account` writes into its own
  # registry (ADR-0045 §"Cache/invalidation model").
  mariadb -e "
    CREATE USER IF NOT EXISTS '${stalwart_db_user}'@'localhost' IDENTIFIED BY '${stalwart_db_pass}';
    ALTER USER '${stalwart_db_user}'@'localhost' IDENTIFIED BY '${stalwart_db_pass}';
    GRANT SELECT ON jabali_panel.mailboxes  TO '${stalwart_db_user}'@'localhost';
    GRANT SELECT ON jabali_panel.domains    TO '${stalwart_db_user}'@'localhost';
    FLUSH PRIVILEGES;
  "
  _ok "Stalwart MariaDB user provisioned: ${stalwart_db_user} (SELECT on mailboxes, domains)"

  local admin_token_file="/etc/jabali-panel/stalwart-admin.token"
  if [[ ! -f "$admin_token_file" ]]; then
    _die "Stalwart admin token missing at $admin_token_file (install_stalwart must run first)"
  fi
  local stalwart_admin_token
  stalwart_admin_token="$(cat "$admin_token_file")"

  local stalwart_apply_plan="/etc/jabali-panel/stalwart-apply-plan.json"
  if [[ ! -f "$stalwart_apply_plan" ]]; then
    _die "Stalwart apply plan missing at $stalwart_apply_plan (install_stalwart must run first)"
  fi

  _install_stalwart_apply_plan "$stalwart_apply_plan" "$stalwart_admin_token"
  _ok "jabali-stalwart.service started + plan applied"

  # M25 Step 7 verification: post-apply, Stalwart's localhost-only
  # listeners (admin-http on 8080, JMAP on 8446, internal/training on
  # 18181) MUST NOT be bound to 0.0.0.0 or [::]. The public listeners
  # (smtp 25/465/587, imap 993) are intentionally wildcard and skipped.
  # Each verify_no_all_interface_binds returns 0 if no listener exists
  # OR all listeners are loopback — a freshly-restarted Stalwart that
  # hasn't bound 8080 yet still passes (because the helper is "no
  # wildcard binds", not "must be present").
  _log "verifying Stalwart bind state (M25 Step 7)"
  if ! verify_no_all_interface_binds 8080; then
    _die "Stalwart factory http listener still bound on :8080 — _delete_stalwart_factory_listeners may have failed; check 'journalctl -u jabali-stalwart'"
  fi
  if ! verify_no_all_interface_binds 8446; then
    _die "Stalwart JMAP on :8446 is bound 0.0.0.0/[::] — apply-plan listener corrupt"
  fi
  if ! verify_no_all_interface_binds 18181; then
    _die "Stalwart internal listener on :18181 is bound 0.0.0.0/[::] — apply-plan listener corrupt"
  fi
  # Belt-and-braces: if the legacy ephemeral :35181 is still up (an
  # operator who hasn't restarted Stalwart since install) flag it as
  # WARN, not DIE. The runbook tells them to restart jabali-stalwart.
  if ss -lntp 2>/dev/null | grep -qE '(\*|0\.0\.0\.0|\[::\]):35181'; then
    _warn "Stalwart's legacy ephemeral :35181 is still bound on a wildcard — restart jabali-stalwart to pick up the M25 Step 7 #internal-loopback listener"
  fi
}

# _delete_stalwart_factory_listeners removes Stalwart's built-in factory
# NetworkListeners that bind to [::] (all interfaces). Stalwart seeds
# these into RocksDB on first start; stalwart-cli apply is create-only
# and cannot delete or replace them. We explicitly delete them before
# restarting so Stalwart does not rebind to all-interface ports we don't
# want (e.g. [::]:8080 web UI, [::]:443 HTTPS web UI).
#
# Arguments: $1 = jmap_port (8080 or 8446), $2 = admin_token
#
# Factory listeners removed: http ([::]:8080), https ([::]:443)
_delete_stalwart_factory_listeners() {
  local jmap_port="$1"
  local admin_token="$2"
  local -a factory_names=("http" "https")
  for fname in "${factory_names[@]}"; do
    local query_out id=""
    query_out="$(STALWART_URL="http://127.0.0.1:${jmap_port}" \
      STALWART_USER="admin" \
      STALWART_PASSWORD="$admin_token" \
      /usr/local/bin/stalwart-cli query x:NetworkListener \
        --where "name=${fname}" --json 2>/dev/null || true)"
    id="$(printf '%s\n' "$query_out" \
      | python3 -c 'import sys,json; d=json.load(sys.stdin); print(d[0]["id"]) if isinstance(d,list) and d else None' \
      2>/dev/null || true)"
    if [[ -z "$id" ]] || [[ "$id" == "None" ]]; then
      _log "factory NetworkListener '${fname}' not found — already removed"
      continue
    fi
    _log "deleting factory NetworkListener '${fname}' (id=${id})"
    local del_rc=0
    STALWART_URL="http://127.0.0.1:${jmap_port}" \
      STALWART_USER="admin" \
      STALWART_PASSWORD="$admin_token" \
      /usr/local/bin/stalwart-cli delete x:NetworkListener --ids "$id" 2>/dev/null || del_rc=$?
    if (( del_rc == 0 )); then
      _ok "factory NetworkListener '${fname}' (id=${id}) deleted"
    else
      _warn "stalwart-cli delete x:NetworkListener --ids '${id}' failed (rc=${del_rc})"
    fi
  done
}

# _install_stalwart_apply_plan starts Stalwart (if not already running),
# waits for /jmap to be reachable, runs stalwart-cli apply against the
# rendered plan, then deletes factory listeners and restarts. Idempotent:
# on re-runs where Stalwart is already on :8446, apply is skipped but
# factory listener deletion and the restart still run to converge state.
_install_stalwart_apply_plan() {
  local plan_file="$1"
  local admin_token="$2"

  if ! systemctl is-enabled --quiet jabali-stalwart.service 2>/dev/null; then
    _log "enabling + starting jabali-stalwart.service"
    systemctl enable --now jabali-stalwart.service
  elif ! systemctl is-active --quiet jabali-stalwart.service; then
    _log "starting jabali-stalwart.service"
    systemctl start jabali-stalwart.service
  fi

  # Poll /jmap/session until Stalwart is serving HTTP. A 401 counts as
  # "ready" — it means the HTTP layer is up and rejecting our missing
  # Authorization header, which is exactly what we want before we try
  # to run an authenticated apply. Only 2xx/3xx/4xx are accepted; 5xx
  # means "server exists but is broken" and we keep polling. 000 means
  # curl couldn't connect (daemon not listening yet).
  #
  # Port probing: on first run the apply-plan has NOT created the
  # `jmap-loopback` NetworkListener yet, so Stalwart falls back to its
  # built-in default HTTP port 8080. On every subsequent run the
  # registry holds the plan's `127.0.0.1:8446` listener, so 8446 is the
  # management port. We probe both and apply against whichever answers.
  local jmap_port=""
  local jmap_status=""
  local waited=0
  local max_wait=30
  while (( waited < max_wait )); do
    for p in 8446 8080; do
      local status
      # `|| true` is load-bearing: curl exits 7 on "connection refused"
      # which is expected while Stalwart is still binding its listeners.
      # Under `set -euo pipefail`, the bare assignment would abort the
      # script on the first refused port before we ever get to try :8080.
      status="$(curl -sS -o /dev/null -w '%{http_code}' --connect-timeout 2 -m 3 \
        "http://127.0.0.1:${p}/jmap/session" 2>/dev/null || true)"
      status="${status:-000}"
      if [[ "$status" =~ ^[234][0-9][0-9]$ ]]; then
        jmap_port="$p"
        jmap_status="$status"
        break 2
      fi
    done
    sleep 1
    waited=$((waited + 1))
  done
  if [[ -z "$jmap_port" ]]; then
    _err "Stalwart /jmap did not come up on 8446 or 8080 within ${max_wait}s — check 'journalctl -u jabali-stalwart'"
    _die "Stalwart bootstrap timed out"
  fi
  _ok "Stalwart /jmap ready on :${jmap_port} (HTTP ${jmap_status}) after ${waited}s"

  # If Stalwart is serving on :8446 we know the plan is already applied
  # (that's the whole point of the 8080→restart→8446 dance below). Re-
  # running `stalwart-cli apply` against an already-applied plan fails
  # with primaryKeyViolation on the NetworkListener create steps because
  # the plan uses `@type: create`, not an upsert action — stalwart-cli
  # has no first-class "apply or update" verb. Skipping a no-op apply is
  # the right call here; reconciler-driven drift correction is out of
  # scope for install.sh.
  local skip_apply=0
  if [[ "$jmap_port" == "8446" ]]; then
    _ok "Stalwart plan already applied (serving on :8446) — skipping re-apply"
    skip_apply=1
  fi

  if (( skip_apply == 0 )); then
  # Idempotent apply: stalwart-cli apply uses @type: create for every
  # NetworkListener, which fails primaryKeyViolation on re-run because
  # there's no first-class upsert verb. Two re-run scenarios cause this:
  #
  #   (a) Full re-apply: every object in RocksDB from a prior successful
  #       apply. Every create step reports primaryKeyViolation. No harm
  #       done — nothing to converge.
  #
  #   (b) Partial re-apply: a subset of objects in RocksDB from a prior
  #       apply that completed some creates before being interrupted
  #       (operator Ctrl-C, VM reboot, crash in a later install step).
  #       Re-running MUST succeed for the missing objects OR we ship an
  #       incomplete config to the host (e.g. M25 Step 7 adding new
  #       listeners to a plan whose older siblings are already applied).
  #
  # Use --continue-on-error so apply reports every failure at the end
  # but keeps going through the rest. Then filter the failure lines: if
  # every failure is primaryKeyViolation (pre-existing object), treat
  # as idempotent success; anything else is a real error (schema drift,
  # auth failure, RocksDB corruption) and we _die.
  _log "applying plan via stalwart-cli against :${jmap_port} (--continue-on-error; primaryKeyViolation on pre-existing objects is idempotent)"
  local apply_out apply_rc=0
  apply_out="$(STALWART_URL="http://127.0.0.1:${jmap_port}" \
    STALWART_USER="admin" \
    STALWART_PASSWORD="$admin_token" \
    /usr/local/bin/stalwart-cli apply --continue-on-error --file "$plan_file" 2>&1)" || apply_rc=$?

  # Print the CLI's summary to the install log so operators can see what
  # was created vs already-existed in a single line. Trim to last 20
  # lines to keep the install transcript readable on large plans.
  printf '%s\n' "$apply_out" | tail -20

  if (( apply_rc != 0 )); then
    # Inspect every per-operation failure line (starts with ✗) and
    # categorize: primaryKeyViolation = idempotent (object already
    # exists from a prior apply), anything else = real error. Ignore
    # the trailing `error: apply completed with N failed operation(s)`
    # summary — it's just a restatement of rc!=0 and tells us nothing
    # about whether the underlying failures are idempotent.
    local non_idempotent_errs
    non_idempotent_errs="$(printf '%s\n' "$apply_out" \
      | grep '^✗' \
      | grep -v 'primaryKeyViolation' || true)"
    if [[ -n "$non_idempotent_errs" ]]; then
      _err "stalwart-cli apply reported non-idempotent failures:"
      printf '  %s\n' "$non_idempotent_errs" >&2
      _err "inspect the plan at $plan_file; re-verify against the upstream schema (ADR-0045 §Schema-pull)"
      _die "Stalwart apply failed"
    fi
    _ok "Stalwart apply: only primaryKeyViolation errors (pre-existing objects) — idempotent success"
  else
    _ok "Stalwart plan applied (SqlDirectory + listeners + Authentication)"
  fi
  fi # end: if (( skip_apply == 0 ))

  # Delete factory NetworkListeners ([::]:8080, [::]:443) before restart.
  # stalwart-cli apply is create-only; only an explicit API delete removes
  # factory-seeded objects from RocksDB. Must happen while Stalwart is
  # still up so the delete API call succeeds.
  _delete_stalwart_factory_listeners "$jmap_port" "$admin_token"

  # Restart so Stalwart rebinds to plan-defined listeners and drops any
  # factory [::] binds just removed. Required on both paths:
  #   8080 (fresh install) — activate newly-created plan listeners
  #   8446 (jabali-update) — drop stale factory binds
  _log "restarting jabali-stalwart to activate plan listeners and drop factory binds"
  systemctl restart jabali-stalwart.service
  waited=0
  while (( waited < 15 )); do
    local s
    # Same `|| true` rationale as the pre-apply probe loop above:
    # curl exits 7 on "connection refused" while Stalwart re-binds,
    # which would abort the script under `set -euo pipefail`.
    s="$(curl -sS -o /dev/null -w '%{http_code}' --connect-timeout 2 -m 3 \
      http://127.0.0.1:8446/jmap/session 2>/dev/null || true)"
    s="${s:-000}"
    if [[ "$s" =~ ^[234][0-9][0-9]$ ]]; then
      _ok "Stalwart now serving plan-defined listener on :8446 (HTTP $s)"
      return
    fi
    sleep 1
    waited=$((waited + 1))
  done
  _die "Stalwart did not come up on :8446 after restart — check 'journalctl -u jabali-stalwart'"
}

# _install_stalwart_binary is a private helper: download the release
# tarball, verify SHA-256 against install/stalwart.sha256, extract, symlink.
_install_stalwart_binary() {
  local version="$1"
  local arch="x86_64-unknown-linux-gnu"
  local tarball="stalwart-${arch}.tar.gz"
  local tarball_path="/tmp/${tarball}"
  local url="https://github.com/stalwartlabs/stalwart/releases/download/v${version}/${tarball}"
  local sha_file="${REPO_DIR}/install/stalwart.sha256"

  _log "downloading Stalwart $version from GitHub"
  if ! curl -fsSL "$url" -o "$tarball_path"; then
    _die "failed to download Stalwart from $url"
  fi

  if [[ ! -f "$sha_file" ]]; then
    _die "Stalwart SHA-256 checksum file not found at $sha_file"
  fi

  local expected_sha
  expected_sha="$(awk '/^[[:space:]]*#/ || NF==0 { next } { print $1; exit }' "$sha_file")"
  if [[ -z "$expected_sha" ]]; then
    _die "no checksum line found in $sha_file (comments only?)"
  fi
  if [[ "$expected_sha" == "PLACEHOLDER_CAPTURE_ON_FIRST_DEPLOY" ]]; then
    _die "Stalwart SHA-256 placeholder in $sha_file — capture the real checksum on first deploy and bump the file (see file header)"
  fi

  local actual_sha
  actual_sha="$(sha256sum "$tarball_path" | awk '{print $1}')"
  if [[ "$expected_sha" != "$actual_sha" ]]; then
    _die "Stalwart SHA-256 mismatch. Expected: $expected_sha, got: $actual_sha"
  fi

  # Atomic swap: extract to a sibling dir, rename, clean up.
  #
  # --no-same-owner: tar by default preserves uid/gid from the archive
  # (Stalwart's CI packages with uid 1001:1001), so without this flag
  # the binary lands owned by whoever happens to have uid 1001 on the
  # target host — typically the first hosting user. That uid then gets
  # the 256 MB binary charged against its POSIX disk quota, immediately
  # putting them over limit. Force root:root on extraction so the
  # binary always lives outside any hosting user's quota scope.
  local new_dir="/opt/stalwart.new"
  rm -rf "$new_dir"
  install -d -m 0755 -o root -g root "$new_dir"
  tar -xzf "$tarball_path" -C "$new_dir" --strip-components=0 --no-same-owner
  chown -R root:root "$new_dir"
  rm -f "$tarball_path"

  # Stalwart tarball layout: top-level `stalwart` binary. v0.16.0 ships
  # it mode 0644 (no exec bit) — the installer must chmod it +x before
  # use. Defensive find in case upstream nests the binary in a future
  # release.
  local bin_in_tar
  bin_in_tar="$(find "$new_dir" -maxdepth 2 -type f -name stalwart | head -n1)"
  if [[ -z "$bin_in_tar" ]]; then
    rm -rf "$new_dir"
    _die "Stalwart binary not found in tarball at $new_dir"
  fi
  chmod 0755 "$bin_in_tar"

  rm -rf /opt/stalwart.prev
  if [[ -d /opt/stalwart ]]; then
    mv /opt/stalwart /opt/stalwart.prev
  fi
  mv "$new_dir" /opt/stalwart
  # Recompute the path under its final location — $bin_in_tar still
  # points at the old /opt/stalwart.new tree.
  bin_in_tar="$(find /opt/stalwart -maxdepth 2 -type f -name stalwart | head -n1)"
  ln -sfn "$bin_in_tar" /usr/local/bin/stalwart
  rm -rf /opt/stalwart.prev
  _ok "Stalwart $version installed at /opt/stalwart (symlinked to /usr/local/bin/stalwart)"
}

# _install_stalwart_cli downloads + verifies the stalwart-cli release
# tarball (separate repo github.com/stalwartlabs/cli) and drops the binary
# at /usr/local/bin/stalwart-cli. ADR-0045 explains the role: the CLI
# speaks the v0.16 JMAP management API, used by install.sh bootstrap and
# the reconciler. Idempotent against version reported by --version.
_install_stalwart_cli() {
  local cli_version="1.0.0"
  local cli_binary="/usr/local/bin/stalwart-cli"
  local arch="x86_64-unknown-linux-gnu"
  local tarball="stalwart-cli-${arch}.tar.xz"
  local tarball_path="/tmp/${tarball}"
  local url="https://github.com/stalwartlabs/cli/releases/download/v${cli_version}/${tarball}"
  local sha_file="${REPO_DIR}/install/stalwart-cli.sha256"

  if [[ -x "$cli_binary" ]]; then
    local installed_version
    installed_version="$("$cli_binary" --version 2>&1 | grep -oP 'v?\K[0-9]+\.[0-9]+\.[0-9]+' | head -n1 || echo unknown)"
    if [[ "$installed_version" == "$cli_version" ]]; then
      _ok "stalwart-cli $cli_version already installed"
      return 0
    fi
    _warn "upgrading stalwart-cli $installed_version -> $cli_version"
  fi

  _log "downloading stalwart-cli $cli_version"
  if ! curl -fsSL "$url" -o "$tarball_path"; then
    _die "failed to download stalwart-cli from $url"
  fi

  if [[ ! -f "$sha_file" ]]; then
    _die "stalwart-cli SHA-256 checksum file not found at $sha_file"
  fi
  local expected_sha
  expected_sha="$(awk '/^[[:space:]]*#/ || NF==0 { next } { print $1; exit }' "$sha_file")"
  if [[ -z "$expected_sha" ]]; then
    _die "no checksum line found in $sha_file (comments only?)"
  fi
  if [[ "$expected_sha" == "PLACEHOLDER_CAPTURE_ON_FIRST_DEPLOY" ]]; then
    _die "stalwart-cli SHA-256 placeholder in $sha_file — capture the real checksum on first deploy and bump the file"
  fi
  local actual_sha
  actual_sha="$(sha256sum "$tarball_path" | awk '{print $1}')"
  if [[ "$expected_sha" != "$actual_sha" ]]; then
    _die "stalwart-cli SHA-256 mismatch. Expected: $expected_sha, got: $actual_sha"
  fi

  # .tar.xz — extract to a tmp dir, find the binary, atomic swap.
  local new_dir="/tmp/stalwart-cli.new"
  rm -rf "$new_dir"
  install -d -m 0755 -o root -g root "$new_dir"
  tar -xJf "$tarball_path" -C "$new_dir"
  rm -f "$tarball_path"

  local bin_in_tar
  bin_in_tar="$(find "$new_dir" -maxdepth 3 -type f -name stalwart-cli -perm -u+x | head -n1)"
  if [[ -z "$bin_in_tar" ]]; then
    rm -rf "$new_dir"
    _die "stalwart-cli binary not found in tarball"
  fi

  install -m 0755 -o root -g root "$bin_in_tar" "$cli_binary"
  rm -rf "$new_dir"
  _ok "stalwart-cli $cli_version installed at $cli_binary"
}

install_bulwark() {
  local bulwark_version="1.4.14"
  local arch="linux-amd64"
  local tarball="bulwark-standalone-${bulwark_version}-${arch}.tar.gz"
  local url="https://github.com/bulwarkmail/webmail/releases/download/${bulwark_version}/${tarball}"
  _log "installing Bulwark webmail (standalone tarball ${bulwark_version})"

  if ! getent passwd jabali-webmail >/dev/null 2>&1; then
    _log "creating jabali-webmail service user"
    useradd --system --no-create-home --shell /usr/sbin/nologin \
      --user-group jabali-webmail
    usermod -a -G "$SERVICE_USER" jabali-webmail
  fi

  install -d -m 0755 -o jabali-webmail -g jabali-webmail /opt/jabali-webmail
  install -d -m 0750 -o jabali-webmail -g jabali-webmail /var/lib/jabali-webmail
  install -d -m 0750 -o jabali-webmail -g jabali-webmail /var/lib/jabali-webmail/settings
  # jabali-webmail.service lists /opt/jabali-webmail/.next/cache in its
  # ReadWritePaths. systemd refuses to enter mount namespacing when a
  # ReadWritePaths entry doesn't exist yet, so Bulwark fails to start on
  # a fresh install until Next.js first writes to its own cache dir —
  # a chicken-and-egg. Pre-create the dir so systemd is happy. The
  # tarball ships .next/ without the cache subdir.
  install -d -m 0755 -o jabali-webmail -g jabali-webmail /opt/jabali-webmail/.next/cache

  # SESSION_SECRET — generate once, preserve across re-runs (rotating it
  # would invalidate every existing "remember me" cookie).
  local session_key_file="/etc/jabali-panel/bulwark-session.key"
  if [[ ! -f "$session_key_file" ]]; then
    _log "generating Bulwark SESSION_SECRET -> $session_key_file"
    umask 077
    openssl rand -base64 32 >"$session_key_file"
    chmod 0640 "$session_key_file"
    chown jabali-webmail:jabali-webmail "$session_key_file"
  else
    _ok "Bulwark SESSION_SECRET already present"
  fi

  # Idempotence: skip re-download if VERSION file already matches target.
  local version_file="/opt/jabali-webmail/VERSION"
  if [[ -f "$version_file" ]] && [[ "$(cat "$version_file")" == "$bulwark_version" ]]; then
    _ok "Bulwark $bulwark_version already installed"
    _install_bulwark_systemd
    return
  fi

  # Pinned SHA of the release tarball (not a git commit — v1.4.14 ships
  # a prebuilt standalone Next.js bundle).
  local sha_file="${REPO_DIR}/install/bulwark.sha256"
  if [[ ! -f "$sha_file" ]]; then
    _die "Bulwark SHA-256 checksum file not found at $sha_file"
  fi
  local expected_sha
  expected_sha="$(awk '/^[[:space:]]*#/ || NF==0 { next } { print $1; exit }' "$sha_file")"
  if [[ -z "$expected_sha" ]]; then
    _die "no checksum line found in $sha_file (comments only?)"
  fi
  if [[ "$expected_sha" == "PLACEHOLDER_CAPTURE_ON_FIRST_DEPLOY" ]]; then
    _die "Bulwark SHA-256 placeholder in $sha_file — capture with: curl -sSL $url | sha256sum, then bump the file"
  fi

  local tarball_path="/tmp/${tarball}"
  _log "downloading $tarball"
  if ! curl -fsSL "$url" -o "$tarball_path"; then
    _die "failed to download Bulwark from $url"
  fi

  local actual_sha
  actual_sha="$(sha256sum "$tarball_path" | awk '{print $1}')"
  if [[ "$expected_sha" != "$actual_sha" ]]; then
    rm -f "$tarball_path"
    _die "Bulwark SHA-256 mismatch. Expected: $expected_sha, got: $actual_sha"
  fi

  # Extract into a sibling directory, then atomic swap. The tarball's
  # top-level dir is `bulwark-standalone/`, so we extract into a staging
  # parent and then move the inner dir into place.
  local stage="/opt/jabali-webmail.stage"
  rm -rf "$stage"
  install -d -m 0755 -o jabali-webmail -g jabali-webmail "$stage"
  tar -xzf "$tarball_path" -C "$stage"
  rm -f "$tarball_path"

  local inner_dir="$stage/bulwark-standalone"
  if [[ ! -d "$inner_dir" ]]; then
    rm -rf "$stage"
    _die "Bulwark tarball did not contain bulwark-standalone/ directory"
  fi
  if [[ ! -f "$inner_dir/server.js" ]]; then
    rm -rf "$stage"
    _die "Bulwark tarball missing server.js entry — layout may have changed in a newer release"
  fi

  echo "$bulwark_version" >"$inner_dir/VERSION"
  chown -R jabali-webmail:jabali-webmail "$inner_dir"

  # Atomic swap.
  rm -rf /opt/jabali-webmail.prev
  if [[ -d /opt/jabali-webmail ]] && [[ "$(ls -A /opt/jabali-webmail 2>/dev/null)" ]]; then
    mv /opt/jabali-webmail /opt/jabali-webmail.prev
  else
    rmdir /opt/jabali-webmail 2>/dev/null || rm -rf /opt/jabali-webmail
  fi
  mv "$inner_dir" /opt/jabali-webmail
  rm -rf "$stage" /opt/jabali-webmail.prev

  _ok "Bulwark $bulwark_version installed at /opt/jabali-webmail"

  _install_bulwark_systemd
}

# _install_bulwark_systemd installs the unit file. Env file is rendered
# separately by _install_bulwark_env; the nginx per-domain vhost is
# written by the panel-agent's webmail.vhost_apply command, driven by
# the reconciler once a domain flips email_enabled=1.
_install_bulwark_systemd() {
  if [[ ! -f "${REPO_DIR}/install/systemd/jabali-webmail.service" ]]; then
    _die "Bulwark systemd unit not found at ${REPO_DIR}/install/systemd/jabali-webmail.service"
  fi
  install -m 0644 -o root -g root "${REPO_DIR}/install/systemd/jabali-webmail.service" \
    /etc/systemd/system/jabali-webmail.service

  # Re-create .next/cache after the atomic swap that ran in install_bulwark
  # (mv of inner_dir into /opt/jabali-webmail wipes the cache subdir we
  # created up front; the tarball doesn't ship one). Without this, the
  # unit crash-loops with status=226/NAMESPACE on first start because
  # systemd refuses to enter mount namespacing when a ReadWritePaths
  # entry doesn't exist on disk.
  install -d -m 0755 -o jabali-webmail -g jabali-webmail \
    /opt/jabali-webmail/.next/cache

  # M25 Step 5: deploy the unix-socket wrapper alongside Bulwark's stock
  # server.js. The systemd unit runs node /opt/jabali-webmail/server-unix.js
  # which loads Next.js's request handler and binds SOCKET_PATH instead of
  # TCP HOSTNAME:PORT. Re-deploy unconditionally so future bulwark-update
  # runs that re-extract a tarball over /opt/jabali-webmail (which would
  # remove our wrapper) restore it on the next install.sh.
  local wrapper_src="${REPO_DIR}/install/jabali-webmail/server-unix.js"
  if [[ ! -f "$wrapper_src" ]]; then
    _die "Bulwark unix wrapper not found at $wrapper_src"
  fi
  install -m 0644 -o jabali-webmail -g jabali-webmail "$wrapper_src" \
    /opt/jabali-webmail/server-unix.js

  # M25 Step 5: drop the http{}-level upstream declaration into
  # /etc/nginx/conf.d/. The per-domain mail vhosts reference it by name
  # via proxy_pass http://jabali_bulwark/;. Conf.d is loaded by Debian's
  # default nginx.conf at the http{} scope — which is where named
  # upstreams must live.
  local upstream_src="${REPO_DIR}/install/nginx/jabali-bulwark-upstream.conf"
  if [[ ! -f "$upstream_src" ]]; then
    _die "Bulwark upstream snippet not found at $upstream_src"
  fi
  install -m 0644 -o root -g root "$upstream_src" \
    /etc/nginx/conf.d/jabali-bulwark-upstream.conf
  if nginx -t >/dev/null 2>&1; then
    systemctl reload nginx 2>/dev/null || true
    _ok "Bulwark upstream wired into nginx (jabali_bulwark)"
  else
    _warn "nginx -t failed after dropping jabali-bulwark-upstream.conf — leaving in place but not reloading"
  fi

  systemctl daemon-reload
  _ok "jabali-webmail.service installed (disabled — starts on first domain.email_enable)"
  _install_bulwark_env
}

# _install_bulwark_env renders install/bulwark/bulwark.env.tmpl into
# /etc/jabali-panel/bulwark.env. Idempotent: writes only when the
# rendered content's SHA-256 differs from the on-disk file. Template
# variable is $JABALI_HOSTNAME (captured by the install.sh preamble).
# Invoked unconditionally from _install_bulwark_systemd so that even
# on a second run that skips the tarball re-download, the env file
# is kept in sync with the template (the one Bulwark actually reads
# at every service start).
_install_bulwark_env() {
  local src="${REPO_DIR}/install/bulwark/bulwark.env.tmpl"
  local dst="/etc/jabali-panel/bulwark.env"
  if [[ ! -f "$src" ]]; then
    _die "Bulwark env template not found at $src"
  fi

  # Resolve hostname: fresh install → JABALI_SRV_HOSTNAME; re-run →
  # parse config.toml; last resort → hostname -f. Mirrors the pattern
  # used by install_kratos so this works on jabali-update too.
  local _bwrk_host="${JABALI_SRV_HOSTNAME:-}"
  if [[ -z "$_bwrk_host" && -f /etc/jabali-panel/config.toml ]]; then
    _bwrk_host="$(awk -F'[= "]+' '/^[[:space:]]*hostname[[:space:]]*=/{print $2; exit}' \
      /etc/jabali-panel/config.toml)"
  fi
  if [[ -z "$_bwrk_host" ]]; then
    _bwrk_host="$(hostname -f 2>/dev/null || hostname 2>/dev/null || true)"
  fi
  if [[ -z "$_bwrk_host" ]]; then
    _die "cannot resolve panel hostname for Bulwark env — pass --hostname or ensure config.toml has 'hostname'"
  fi

  # Render into a tmpfile first so we can diff by hash before writing.
  # Using envsubst would pull in gettext as a dep; sed is enough for
  # the two variables this template uses.
  local tmp
  tmp=$(mktemp)
  # shellcheck disable=SC2016
  sed "s|\${JABALI_SERVER_HOSTNAME}|${_bwrk_host}|g" "$src" >"$tmp"

  local new_sha old_sha=""
  new_sha=$(sha256sum "$tmp" | awk '{print $1}')
  if [[ -f "$dst" ]]; then
    old_sha=$(sha256sum "$dst" | awk '{print $1}')
  fi
  if [[ "$new_sha" == "$old_sha" ]]; then
    rm -f "$tmp"
    _ok "Bulwark env ($dst) already up to date"
    return
  fi

  install -m 0640 -o jabali-webmail -g jabali-webmail "$tmp" "$dst"
  rm -f "$tmp"
  _ok "Bulwark env rendered -> $dst"

  # Soft reload: if the service is already running, restart so the new
  # env takes effect. If it's inactive, the next reconciler-triggered
  # start will pick up the file.
  if systemctl is-active jabali-webmail >/dev/null 2>&1; then
    systemctl restart jabali-webmail || _warn "failed to restart jabali-webmail after env update"
  fi
}

# ---------- step 8: Kratos identity provider (M20) ---------------------------

install_kratos() {
  # Kratos binary: vendored SHA-256 verification pattern matching wp-cli + phpmyadmin.
  local kratos_version="26.2.0"
  _log "installing Ory Kratos identity provider (v${kratos_version})"

  local kratos_binary="/usr/local/bin/kratos"
  local kratos_tar="/tmp/kratos_${kratos_version}-linux_64bit.tar.gz"
  local kratos_sha_file="${REPO_DIR}/install/kratos.sha256"
  local kratos_url="https://github.com/ory/kratos/releases/download/v${kratos_version}/kratos_${kratos_version}-linux_64bit.tar.gz"

  # Check if already installed at correct version.
  if [[ -f "$kratos_binary" ]]; then
    local installed_version
    installed_version=$("$kratos_binary" version 2>&1 | grep -oP 'Version:\s+\K[^[:space:]]+' || echo "unknown")
    if [[ "$installed_version" == "v${kratos_version}" ]]; then
      _ok "Kratos $kratos_version already installed"
      return
    fi
  fi

  # Download + verify SHA-256.
  _log "downloading Kratos $kratos_version from GitHub"
  if ! curl -fsSL "$kratos_url" -o "$kratos_tar"; then
    _die "failed to download Kratos from $kratos_url"
  fi

  if [[ ! -f "$kratos_sha_file" ]]; then
    _die "Kratos SHA-256 checksum file not found at $kratos_sha_file"
  fi

  # Skip comment + blank lines so the checksum file can carry provenance
  # metadata (`# Source: ...`, `# Verified: YYYY-MM-DD`) without tripping
  # the comparison — matches the sha256sum(1) convention.
  local expected_sha
  expected_sha="$(awk '/^[[:space:]]*#/ || NF==0 { next } { print $1; exit }' "$kratos_sha_file")"
  local actual_sha
  actual_sha="$(sha256sum "$kratos_tar" | awk '{print $1}')"
  if [[ -z "$expected_sha" ]]; then
    _die "no checksum line found in $kratos_sha_file (comments only?)"
  fi
  if [[ "$expected_sha" != "$actual_sha" ]]; then
    _die "Kratos SHA-256 mismatch. Expected: $expected_sha, got: $actual_sha"
  fi

  # Extract + install binary.
  tar -xzf "$kratos_tar" -C /tmp/
  install -m 0755 -o root -g root /tmp/kratos "$kratos_binary"
  rm -f "$kratos_tar" /tmp/kratos

  _ok "Kratos binary installed at $kratos_binary"

  # Provision MariaDB database + user for Kratos.
  local kratos_db_name="jabali_kratos"
  local kratos_db_user="jabali_kratos"
  local kratos_pw_file="/etc/jabali-panel/kratos-db-password"

  if [[ ! -f "$kratos_pw_file" ]]; then
    _log "generating Kratos DB password → $kratos_pw_file"
    umask 077
    openssl rand -hex 32 >"$kratos_pw_file"
    chmod 0600 "$kratos_pw_file"
    chown root:root "$kratos_pw_file"
  fi

  local kratos_db_pass
  kratos_db_pass="$(cat "$kratos_pw_file")"

  # Create database + user. Idempotent: CREATE IF NOT EXISTS.
  mariadb -e "
    CREATE DATABASE IF NOT EXISTS \`${kratos_db_name}\`
      CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci;
    CREATE USER IF NOT EXISTS '${kratos_db_user}'@'localhost' IDENTIFIED BY '${kratos_db_pass}';
    ALTER USER '${kratos_db_user}'@'localhost' IDENTIFIED BY '${kratos_db_pass}';
    GRANT SELECT, INSERT, UPDATE, DELETE, CREATE, DROP, INDEX, ALTER,
          REFERENCES, LOCK TABLES, CREATE TEMPORARY TABLES
      ON \`${kratos_db_name}\`.* TO '${kratos_db_user}'@'localhost';
    FLUSH PRIVILEGES;
  "

  _ok "Kratos database provisioned: DB=${kratos_db_name}, user=${kratos_db_user}"

  # Render kratos.yml config from template + write credentials.
  local kratos_config="/etc/jabali-panel/kratos.yml"
  local kratos_secrets_dir="/etc/jabali-panel/kratos-secrets"

  # Persisted secrets directory. Each file = one long-lived secret. We
  # generate once and reuse on re-runs — rotating these on every install
  # would invalidate existing sessions + encrypted cookies and surprise
  # operators. Rotation belongs in the runbook, not here.
  install -d -m 0700 -o root -g root "$kratos_secrets_dir"

  _kratos_ensure_secret() {
    local path="$1"
    if [[ ! -f "$path" ]]; then
      umask 077
      openssl rand -hex 32 > "$path"
      chmod 0600 "$path"
      chown root:root "$path"
    fi
  }
  _kratos_ensure_secret "$kratos_secrets_dir/default"
  _kratos_ensure_secret "$kratos_secrets_dir/cookie"

  local kratos_secret_default kratos_secret_cookie
  kratos_secret_default="$(cat "$kratos_secrets_dir/default")"
  kratos_secret_cookie="$(cat "$kratos_secrets_dir/cookie")"

  # Render kratos.yml from install/kratos.yml.tmpl. The template uses
  # Go-style {{.Var}} mustaches (matches docs/ADR-0034 style). We substitute
  # via sed rather than envsubst because (a) envsubst's ${VAR} syntax doesn't
  # match the template and (b) gettext-base isn't in Debian's minimal set,
  # so depending on it adds an apt dep without saving any code.
  if [[ ! -f "${REPO_DIR}/install/kratos.yml.tmpl" ]]; then
    _die "Kratos template not found at ${REPO_DIR}/install/kratos.yml.tmpl"
  fi

  # Resolve the panel hostname. Fresh install: prompt_server_settings set
  # $JABALI_SRV_HOSTNAME. Re-run after config.toml is seeded: pull from
  # the existing config. Last resort: hostname -f.
  local panel_hostname="${JABALI_SRV_HOSTNAME:-}"
  if [[ -z "$panel_hostname" && -f /etc/jabali-panel/config.toml ]]; then
    panel_hostname="$(awk -F'[= "]+' '/^[[:space:]]*hostname[[:space:]]*=/{print $2; exit}' /etc/jabali-panel/config.toml)"
  fi
  if [[ -z "$panel_hostname" ]]; then
    panel_hostname="$(hostname -f 2>/dev/null || hostname 2>/dev/null || echo 'localhost')"
  fi

  # None of these values contain `|`, so we use it as the sed delimiter
  # to avoid escaping `/` in URLs. All inputs are either generated by us
  # (hex passwords, fixed db-user/db-name) or validated DNS names — no
  # shell-metacharacter exposure.
  sed \
    -e "s|{{\.KratosDatabaseUser}}|${kratos_db_user}|g" \
    -e "s|{{\.KratosDatabasePassword}}|${kratos_db_pass}|g" \
    -e "s|{{\.KratosDatabaseName}}|${kratos_db_name}|g" \
    -e "s|{{\.PanelHostname}}|${panel_hostname}|g" \
    -e "s|{{\.KratosSecretDefault}}|${kratos_secret_default}|g" \
    -e "s|{{\.KratosCookieSecret}}|${kratos_secret_cookie}|g" \
    "${REPO_DIR}/install/kratos.yml.tmpl" > "$kratos_config"
  chmod 0640 "$kratos_config"
  chown root:"$SERVICE_USER" "$kratos_config"

  # Fail loud if any mustache slipped through (template drift — a new
  # placeholder was added without a matching sed line).
  if grep -q '{{\..*}}' "$kratos_config"; then
    _die "unsubstituted mustaches left in $kratos_config — template drift?"
  fi

  _ok "Kratos config written to $kratos_config"

  # Copy identity schema file.
  if [[ ! -f "${REPO_DIR}/install/kratos-identity-schema.json" ]]; then
    _die "Kratos identity schema not found at ${REPO_DIR}/install/kratos-identity-schema.json"
  fi
  install -m 0644 -o root -g root "${REPO_DIR}/install/kratos-identity-schema.json" \
    /etc/jabali-panel/kratos-identity-schema.json

  _ok "Kratos identity schema installed"

  # Run database migrations.
  _log "running Kratos database migrations"
  # Kratos emits ~2 JSON-log lines per migration (one per file, bidirectional).
  # On a fresh install that's hundreds of lines — silence the chatter and
  # surface the full log only on failure.
  local kratos_migrate_log="/tmp/jabali-kratos-migrate.$$.log"
  if ! "$kratos_binary" migrate sql -e -c "$kratos_config" --yes >"$kratos_migrate_log" 2>&1; then
    _err "Kratos database migrations failed — full output:"
    cat "$kratos_migrate_log" >&2
    rm -f "$kratos_migrate_log"
    _die "Kratos database migrations failed"
  fi
  local migrate_count
  migrate_count="$(grep -c 'applied successfully' "$kratos_migrate_log" 2>/dev/null || echo 0)"
  rm -f "$kratos_migrate_log"
  _ok "Kratos database migrations completed (${migrate_count} applied)"

  _ok "Kratos migrations completed"

  # Install systemd unit file. Each step is a _log line so when this
  # function silently exits (set -e), the operator can tell which one
  # was the last to fire — the alternative (no progress output) produced
  # the bug reported by the first-install operator: script dies with
  # zero diagnostic between "migrations completed" and the shell prompt.
  _log "installing jabali-kratos systemd unit"
  if [[ ! -f "${REPO_DIR}/install/systemd/jabali-kratos.service" ]]; then
    _die "Kratos systemd unit template not found at ${REPO_DIR}/install/systemd/jabali-kratos.service"
  fi
  install -m 0644 -o root -g root "${REPO_DIR}/install/systemd/jabali-kratos.service" \
    /etc/systemd/system/jabali-kratos.service

  _log "reloading systemd daemon"
  systemctl daemon-reload

  _log "enabling jabali-kratos.service"
  systemctl enable --quiet jabali-kratos

  _log "restarting jabali-kratos.service"
  # --quiet silences success output, but systemctl restart still returns
  # non-zero if the unit fails to start (e.g. Kratos crashes on config
  # parse). set -e would kill us silently. Capture + surface the failure
  # with the last 20 log lines so the operator gets context instead of
  # a bare shell prompt.
  if ! systemctl restart --quiet jabali-kratos; then
    _warn "jabali-kratos failed to start; dumping last 20 journal lines"
    journalctl -u jabali-kratos -n 20 --no-pager || true
    _die "jabali-kratos did not start — fix /etc/jabali-panel/kratos.yml and re-run install.sh"
  fi

  # Poll for readiness. M25 Step 3: Kratos's public endpoint is now a Unix
  # socket; curl --unix-socket talks to it via /admin/health/ready (any
  # path works; we want a 2xx). Same set-e-safe arithmetic as before.
  _log "waiting for Kratos to be ready (max 30s)"
  local waited=0
  while [[ $waited -lt 30 ]]; do
    if curl -sf --unix-socket /run/jabali-kratos/public.sock http://kratos/health/ready >/dev/null 2>&1; then
      _ok "Kratos is ready"
      break
    fi
    sleep 1
    waited=$((waited + 1))
  done

  if [[ $waited -eq 30 ]]; then
    _warn "Kratos did not become ready within 30s. Check: systemctl status jabali-kratos"
  fi

  # M25 Step 2+3 verification: both endpoints must be Unix sockets at
  # /run/jabali-kratos/{admin,public}.sock with mode 0660 jabali:jabali-sockets,
  # AND the legacy TCP listeners on 4433 + 4434 must be gone. The
  # verify_socket_perms + verify_no_all_interface_binds helpers were sourced
  # from install/scripts/socket-helpers.sh at the top of main(). If any
  # check fails the installer aborts loudly so the operator doesn't
  # discover a 502 from panel-api → Kratos in production.
  if ! verify_socket_perms /run/jabali-kratos/admin.sock jabali jabali-sockets 660; then
    _die "Kratos admin socket has wrong perms — see message above"
  fi
  if ! verify_socket_perms /run/jabali-kratos/public.sock jabali jabali-sockets 660; then
    _die "Kratos public socket has wrong perms — see message above"
  fi
  if ! verify_no_all_interface_binds 4434; then
    _die "Kratos admin still bound on TCP :4434 — kratos.yml didn't apply or rolled back to TCP"
  fi
  if ! verify_no_all_interface_binds 4433; then
    _die "Kratos public still bound on TCP :4433 — kratos.yml didn't apply or rolled back to TCP"
  fi

  # Migrate an existing /etc/jabali-panel/config.toml from the legacy TCP
  # URLs to the unix-socket forms. Idempotent: a config that already has
  # the unix URLs (or any other custom value) is left untouched. This
  # lives in install.sh — not in a separate cutover CLI — because
  # `jabali update` re-runs install.sh on the operator's host; a separate
  # script would never run on managed boxes (per "install.sh is truth"
  # memory).
  local panel_config="/etc/jabali-panel/config.toml"
  if [[ -f "$panel_config" ]] && grep -qE '^\s*admin_url\s*=\s*"http://127\.0\.0\.1:4434"' "$panel_config"; then
    _log "migrating config.toml admin_url from TCP to unix socket (M25 Step 2)"
    sed -i 's|^\(\s*admin_url\s*=\s*\)"http://127\.0\.0\.1:4434"|\1"unix:/run/jabali-kratos/admin.sock"|' "$panel_config"
    _ok "config.toml admin_url migrated"
  fi
  if [[ -f "$panel_config" ]] && grep -qE '^\s*public_url\s*=\s*"http://127\.0\.0\.1:4433"' "$panel_config"; then
    _log "migrating config.toml public_url from TCP to unix socket (M25 Step 3)"
    sed -i 's|^\(\s*public_url\s*=\s*\)"http://127\.0\.0\.1:4433"|\1"unix:/run/jabali-kratos/public.sock"|' "$panel_config"
    _ok "config.toml public_url migrated"
  fi

  _ok "Kratos identity provider installed and running"
}


# ---------- main ------------------------------------------------------------

main() {
  print_banner
  preflight
  prompt_server_settings
  install_base_packages
  # M25 step 1: kill the LLMNR :5355 listener once systemd-resolved is on
  # the host. Drop-in only — operator can override later.
  disable_llmnr
  # M18 — resource-limits prerequisites. cgroups v2 probe FIRST (fails
  # loud if misconfigured; every subsequent slice we ever emit depends
  # on unified hierarchy). Disk quota and /tmp tmpfs are both
  # idempotent and warn-and-skip on unsupported filesystems.
  # DNS is deliberately left alone at install time (see the block
  # following install_base_packages for rationale).
  configure_cgroups_v2
  configure_disk_quota
  configure_tmp_tmpfs
  install_nginx
  install_php
  install_disabled_page
  install_node
  install_go
  ensure_user_and_dirs
  # Order matters: write_env_file seeds PANEL_ADDR / PANEL_ENV / JWT_SECRET
  # hooks BEFORE provision_mariadb appends DATABASE_URL. Reversing the two
  # would leave a fresh install with only the DB URL and no server config.
  write_env_file
  provision_mariadb
  install_mariadb_skip_networking
  install_redis
  install_powerdns
  bootstrap_pdns_self_zone
  # M6.3: recursor owns loopback :53 and forwards panel-authoritative zones
  # into pdns-server at :5300. Must run AFTER bootstrap_pdns_self_zone (the
  # self-zone has to exist in pdns before the recursor's post-install probe
  # tries to resolve it) and BEFORE setup_certbot (certbot's HTTP-01 flow
  # needs the panel's own hostname to resolve locally).
  install_pdns_recursor
  setup_certbot
  # M26 Step 1 — security foundation. Wired here (after pdns/certbot,
  # before clone_or_update_repo and the long build_frontend / npm steps)
  # because:
  #   - All apt packages (crowdsec, ufw, yq) land in the install_base_packages
  #     batch above; the firewall bouncer is detected at runtime here.
  #   - CrowdSec LAPI binds on a Unix socket (ADR-0050) — must be
  #     configured BEFORE install_stalwart so it doesn't race Stalwart
  #     pinning 127.0.0.1:8080.
  #   - UFW activates with the SSH + panel + nginx + mail allow-list
  #     BEFORE Stalwart's first bind (avoids documented iptables-reload
  #     race) AND BEFORE build_frontend (so an interrupted build
  #     doesn't strand the host without a firewall).
  #   - cleanup_modsecurity removes the M26 ModSecurity stack on existing
  #     hosts that ran an earlier install (ADR-0055 superseded 2026-04-26).
  install_crowdsec
  install_crowdsec_appsec
  install_crowdsec_nginx_bouncer
  install_crowdsec_profiles
  cleanup_modsecurity
  install_ufw
  install_restart_drop_ins
  clone_or_update_repo
  # M25: source the socket-helper definitions now that the repo's install/
  # tree is on disk. Steps 2–5 will call verify_socket_perms /
  # verify_no_all_interface_binds after each service-bind change. Sourced
  # here (not earlier) because under `curl | bash` the install/scripts/
  # tree doesn't exist until clone_or_update_repo populates $REPO_DIR.
  # shellcheck source=install/scripts/socket-helpers.sh
  source "$REPO_DIR/install/scripts/socket-helpers.sh"
  # M25: bring the jabali-sockets group into existence. SERVICE_USER and
  # www-data already exist by now; jabali-webmail is created later by
  # install_bulwark — a second call after that picks it up. The function
  # is idempotent so repeating it is cheap.
  ensure_jabali_sockets_group
  install_jabali_slices
  install_kratos
  install_php_pool_template
  build_frontend
  build_backend
  write_config_file
  provision_tls_cert
  bootstrap_panel_acme_webroot
  install_jabali_panel_cert_hook
  seed_admin_env
  install_sso_key
  install_sso_reaper_timer
  # Order matters: install_phpmyadmin extracts the tarball to
  # /opt/phpmyadmin/current, which the pma pool config references as
  # chdir=. Starting the FPM service before the tarball is extracted
  # causes FPM to fail with "chdir path does not exist".
  install_phpmyadmin
  install_phpmyadmin_fpm_pool
  install_wp_cli
  install_sftp_group
  install_sftp_sshd_config
  install_nginx_default_vhost
  # M25 Step 4: install the nginx vhost on :8443 that terminates TLS and
  # proxies to the panel-api Unix socket. Runs AFTER install_nginx_default_vhost
  # so the http{} context (defined by Debian's stock nginx.conf) and the
  # default vhost are already in place; runs BEFORE write_systemd_unit so
  # nginx -t doesn't have to wait on panel-api startup.
  install_nginx_panel_vhost
  write_agent_systemd_unit
  write_systemd_unit
  start_and_verify_agent
  start_and_verify
  # First-phase Stalwart bootstrap (binary download, service user,
  # stalwart-cli, admin token, MariaDB password file, apply plan render,
  # unit file install). Safe to run after start_and_verify — doesn't
  # depend on the panel being up, just on the repo being cloned.
  install_stalwart
  # Second-phase Stalwart bootstrap: needs jabali_panel.{mailboxes,domains}
  # to exist, which the panel service creates via migration 000054 on its
  # first start (inside start_and_verify). Must run after, never before.
  install_stalwart_apply
  # M6.4 (ADR-0048): auto-register the panel hostname as an email-enabled
  # domain. Ordering: after start_and_verify (admin user exists via
  # BootstrapAdmin) AND after bootstrap_pdns_self_zone (pdns zone row
  # exists — FK-asserted inside install_panel_primary_domain) AND after
  # install_stalwart_apply (Stalwart ready to accept the domain-add
  # command the reconciler will fire).
  install_panel_primary_domain
  # Bulwark webmail. Depends on Stalwart being live (JMAP backend) so it
  # runs after install_stalwart_apply.
  install_bulwark
  # M25: jabali-webmail user now exists; second pass over the socket group
  # picks it up. Idempotent for SERVICE_USER + www-data which were added
  # earlier (post clone_or_update_repo).
  ensure_jabali_sockets_group
  seed_last_built_sha
  _ok "jabali-panel + jabali-agent installed. Status:"
  _ok "  systemctl status $AGENT_SERVICE_NAME"
  _ok "  systemctl status $SERVICE_NAME"

  # Display credentials if this was a fresh install.
  if [[ -n "${JABALI_SEED_EMAIL:-}" ]]; then
    local panel_host="${PANEL_ADDR%:*}"
    local panel_port="${PANEL_ADDR##*:}"
    [[ "$panel_host" == "0.0.0.0" || -z "$panel_host" ]] && panel_host="$(hostname -I | awk '{print $1}')"

    echo ""
    echo "╔══════════════════════════════════════════════════════════════╗"
    echo "║                     JABALI PANEL                           ║"
    echo "╠══════════════════════════════════════════════════════════════╣"
    echo "║                                                            ║"
    printf "║  URL:      https://%-39s ║\n" "${panel_host}:${panel_port}"
    printf "║  Email:    %-48s ║\n" "$JABALI_SEED_EMAIL"
    printf "║  Password: %-48s ║\n" "$JABALI_SEED_PASS"
    echo "║                                                            ║"
    echo "║  Change this password immediately after first login.       ║"
    echo "╚══════════════════════════════════════════════════════════════╝"
    echo ""
  fi
}

# ---------- uninstall flow --------------------------------------------------
# `install.sh --uninstall` tears down everything the installer creates, in
# roughly the reverse order of main(). Best-effort: every step uses `|| true`
# so a partial install (install failed mid-way) can still be cleaned up.
# Leaves apt-installed OS packages (nginx, mariadb, pdns, php, …) INSTALLED —
# they are shared with the rest of the OS and re-running install.sh will
# reconfigure them. Destructive prompts (drop databases, remove /home users)
# ask for explicit confirmation unless --yes is given.
uninstall() {
  [[ $EUID -eq 0 ]] || { printf 'install.sh --uninstall: must run as root\n' >&2; exit 1; }

  cat <<'EOF'

============================================================
  JABALI UNINSTALL
============================================================
This will remove:
  • jabali-* systemd units and their drop-ins
  • /usr/local/bin/{jabali,jabali-panel,jabali-agent,kratos,stalwart,stalwart-cli}
  • /etc/jabali-panel/, /etc/jabali/
  • /var/lib/jabali-*, /var/lib/stalwart, /run/jabali*
  • /opt/jabali-panel, /opt/jabali-webmail, /opt/stalwart, /opt/phpmyadmin
  • systemd-resolved + pdns-recursor jabali drop-ins
  • /etc/ssh/sshd_config.d/jabali-sftp.conf
  • jabali PHP-FPM pools
  • system accounts: jabali, jabali-mail, jabali-webmail, stalwart
  • system groups:  jabali, jabali-mail, jabali-webmail, jabali-sftp

Will ASK before:
  • dropping MariaDB databases (jabali_panel, jabali_pdns, jabali_kratos, stalwart_smtp)
  • removing user home directories under /home/

Will NOT remove apt packages (nginx, mariadb-server, pdns, php, node, …).

EOF

  if [[ "${_cli_yes:-}" != "1" ]]; then
    read -rp "Proceed with uninstall? [y/N]: " ans
    [[ "${ans:-}" =~ ^[yY] ]] || { _log "cancelled"; exit 0; }
  fi

  _log "stopping jabali services"
  local svc
  for svc in \
    jabali-panel.service \
    jabali-agent.service \
    jabali-kratos.service \
    jabali-stalwart.service \
    jabali-webmail.service \
    jabali-sso-reaper.timer \
    jabali-sso-reaper.service; do
    systemctl stop    "$svc" 2>/dev/null || true
    systemctl disable "$svc" 2>/dev/null || true
  done

  # All jabali-fpm@<user>.service instances (per-user slices).
  local unit
  while read -r unit; do
    [[ -n "$unit" ]] || continue
    systemctl stop    "$unit" 2>/dev/null || true
    systemctl disable "$unit" 2>/dev/null || true
  done < <(systemctl list-units --type=service --all --no-legend 'jabali-fpm@*.service' 2>/dev/null | awk '{print $1}')

  _log "removing jabali systemd unit files + drop-ins"
  rm -f  /etc/systemd/system/jabali-panel.service
  rm -f  /etc/systemd/system/jabali-agent.service
  rm -f  /etc/systemd/system/jabali-kratos.service
  rm -f  /etc/systemd/system/jabali-stalwart.service
  rm -f  /etc/systemd/system/jabali-webmail.service
  rm -f  /etc/systemd/system/jabali-sso-reaper.service
  rm -f  /etc/systemd/system/jabali-sso-reaper.timer
  rm -f  /etc/systemd/system/jabali-fpm@.service
  rm -f  /etc/systemd/system/jabali.slice
  rm -f  /etc/systemd/system/jabali-user.slice
  rm -rf /etc/systemd/system/jabali-fpm@*
  rm -rf /etc/systemd/system/jabali-panel.service.d
  rm -rf /etc/systemd/system/jabali-agent.service.d

  # Drop-ins on shared system services — remove only the files WE wrote.
  rm -f /etc/systemd/system/pdns-recursor.service.d/10-jabali-after.conf
  rm -f /etc/systemd/system/pdns-recursor.service.d/20-jabali-old-settings.conf
  rmdir --ignore-fail-on-non-empty /etc/systemd/system/pdns-recursor.service.d 2>/dev/null || true
  rm -f /etc/systemd/system/systemd-resolved.service.d/10-jabali-after.conf
  rmdir --ignore-fail-on-non-empty /etc/systemd/system/systemd-resolved.service.d 2>/dev/null || true
  rm -f /etc/systemd/resolved.conf.d/jabali.conf
  rm -f /etc/systemd/resolved.conf.d/zz-jabali-recursor.conf

  systemctl daemon-reload 2>/dev/null || true

  # Restart shared services so they re-read without jabali drop-ins.
  systemctl restart systemd-resolved 2>/dev/null || true
  systemctl restart pdns-recursor    2>/dev/null || true

  _log "removing binaries"
  rm -f /usr/local/bin/jabali \
        /usr/local/bin/jabali-panel \
        /usr/local/bin/jabali-agent \
        /usr/local/bin/kratos \
        /usr/local/bin/stalwart \
        /usr/local/bin/stalwart-cli

  _log "removing config files"
  rm -rf /etc/jabali-panel
  rm -rf /etc/jabali
  rm -f  /etc/nginx/conf.d/jabali-pma-logformat.conf
  rm -f  /etc/ssh/sshd_config.d/jabali-sftp.conf
  # Validate sshd now that our drop-in is gone — best-effort.
  sshd -t 2>/dev/null && systemctl reload ssh 2>/dev/null || true

  _log "removing PHP-FPM jabali pools"
  local pdir poolf
  for pdir in /etc/php/*/fpm/pool.d; do
    [[ -d "$pdir" ]] || continue
    for poolf in "$pdir"/jabali-*.conf "$pdir"/_jabali-*.conf; do
      [[ -f "$poolf" ]] && rm -f "$poolf"
    done
  done
  # Restart PHP-FPM so the per-version daemons drop the now-missing pool refs.
  local fpm
  for fpm in /etc/init.d/php*-fpm; do
    [[ -x "$fpm" ]] && systemctl restart "$(basename "$fpm")" 2>/dev/null || true
  done

  _log "removing state + install directories"
  rm -rf /var/lib/jabali        \
         /var/lib/jabali-panel  \
         /var/lib/jabali-webmail \
         /var/lib/stalwart       \
         /run/jabali             \
         /run/jabali-panel       \
         /opt/jabali-panel       \
         /opt/jabali-webmail     \
         /opt/jabali-webmail.stage \
         /opt/jabali-webmail.prev  \
         /opt/stalwart            \
         /opt/stalwart.new        \
         /opt/stalwart.prev       \
         /opt/phpmyadmin          \
         /var/www/jabali-disabled

  # MariaDB: drop jabali databases + users. Try socket-auth first; if that
  # fails (root password set), ask for a password once.
  local mysql_root_cmd=""
  if mariadb -u root -e 'SELECT 1' >/dev/null 2>&1; then
    mysql_root_cmd="mariadb -u root"
  elif mysql -u root -e 'SELECT 1' >/dev/null 2>&1; then
    mysql_root_cmd="mysql -u root"
  fi

  if [[ -z "$mysql_root_cmd" ]]; then
    _warn "MariaDB root login (socket auth) not available — skipping database drop"
    _warn "Drop manually: DROP DATABASE jabali_panel; DROP DATABASE jabali_pdns; DROP DATABASE jabali_kratos;"
  else
    local drop_db="n"
    if [[ "${_cli_yes:-}" == "1" ]]; then
      drop_db="y"
    else
      read -rp "Drop jabali MariaDB databases + users (jabali_panel, jabali_pdns, jabali_kratos, stalwart_smtp)? [y/N]: " drop_db
    fi
    if [[ "${drop_db:-}" =~ ^[yY] ]]; then
      _log "dropping jabali databases"
      $mysql_root_cmd <<'SQL' 2>/dev/null || _warn "some DROP statements failed (may already be gone)"
DROP DATABASE IF EXISTS jabali_panel;
DROP DATABASE IF EXISTS jabali_pdns;
DROP DATABASE IF EXISTS jabali_kratos;
DROP DATABASE IF EXISTS stalwart_smtp;
DROP USER IF EXISTS 'jabali_panel_app'@'localhost';
DROP USER IF EXISTS 'jabali_pdns'@'localhost';
DROP USER IF EXISTS 'jabali_kratos'@'localhost';
DROP USER IF EXISTS 'stalwart_smtp'@'localhost';
FLUSH PRIVILEGES;
SQL
    else
      _log "skipping database drop (user declined)"
    fi
  fi

  _log "removing jabali system accounts"
  local u
  for u in jabali-webmail jabali-mail stalwart jabali; do
    if id "$u" >/dev/null 2>&1; then
      # userdel -r would remove home; we pass --force for idempotence but NOT -r
      # because jabali's home is /opt/jabali-panel which we already rm -rf'd.
      userdel --force "$u" 2>/dev/null && _log "removed user $u" || _warn "could not remove user $u"
    fi
  done
  # Groups (may remain if --user-group flag wasn't used, or if the user was
  # removed but the group lingered).
  local g
  for g in jabali-sftp jabali-webmail jabali-mail jabali; do
    getent group "$g" >/dev/null 2>&1 && { groupdel "$g" 2>/dev/null && _log "removed group $g" || true; }
  done

  # Interactive: /home/ user cleanup. Jabali provisions end-user accounts
  # with home dirs under /home/<user>/. We can't distinguish jabali-created
  # accounts from pre-existing ones with certainty, so we list every
  # non-system /home entry and prompt per user.
  _log "enumerating /home/ users"
  local home_users=()
  while IFS=: read -r uname _ uid _ _ udir _; do
    [[ -d "$udir" ]] || continue
    [[ "$udir" == /home/* ]] || continue
    (( uid >= 1000 )) || continue
    home_users+=("$uname")
  done < /etc/passwd

  if [[ ${#home_users[@]} -eq 0 ]]; then
    _log "no /home/ users found"
  else
    printf '\nFound %d user(s) with home directories under /home/:\n' "${#home_users[@]}"
    printf '  %s\n' "${home_users[@]}"
    echo
    if [[ "${_cli_yes:-}" == "1" ]]; then
      _warn "--yes given: NOT removing /home users automatically (too destructive for auto-mode)."
      _warn "Remove manually if desired: userdel -r <user>"
    else
      local rm_all
      read -rp "Remove ALL listed users + their /home directories? [y/N/each]: " rm_all
      case "${rm_all:-}" in
        [yY]*)
          for u in "${home_users[@]}"; do
            userdel -r "$u" 2>/dev/null && _log "removed user $u + /home/$u" || _warn "userdel -r $u failed"
          done
          ;;
        each|EACH|e|E)
          for u in "${home_users[@]}"; do
            local ans2
            read -rp "  remove user '$u' (+ /home/$u)? [y/N]: " ans2
            if [[ "${ans2:-}" =~ ^[yY] ]]; then
              userdel -r "$u" 2>/dev/null && _log "removed $u" || _warn "userdel -r $u failed"
            fi
          done
          ;;
        *)
          _log "keeping all /home users"
          ;;
      esac
    fi
  fi

  _ok "jabali uninstall complete"
  _ok "OS packages (nginx, mariadb, pdns, php, node, …) left INSTALLED — remove with apt if desired"
}

if [[ -n "${_cli_uninstall:-}" ]]; then
  uninstall
else
  main "$@"
fi
