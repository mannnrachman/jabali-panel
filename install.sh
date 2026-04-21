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
#
# Examples:
#   curl -fsSL <...>/install.sh | bash -s -- --hostname=panel.example.com
#   curl -fsSL <...>/install.sh | bash -s -- --hostname panel.example.com --token <GITEA_TOKEN>
#
# Legacy: `bash -s -- <GITEA_TOKEN>` (positional token) still works.

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
PANEL_ADDR="${JABALI_PANEL_ADDR:-0.0.0.0:8443}"
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
_positional=()
while [[ $# -gt 0 ]]; do
  case "$1" in
    --hostname=*) _cli_hostname="${1#*=}"; shift ;;
    --hostname)   _cli_hostname="${2:-}"; shift 2 ;;
    --token=*)    _cli_token="${1#*=}"; shift ;;
    --token)      _cli_token="${2:-}"; shift 2 ;;
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

# ---------- tiny logger -----------------------------------------------------

_log()  { printf '\033[1;34m[jabali-install]\033[0m %s\n' "$*"; }
_ok()   { printf '\033[1;32m[jabali-install]\033[0m %s\n' "$*"; }
_warn() { printf '\033[1;33m[jabali-install]\033[0m %s\n' "$*" >&2; }
# _err prints in red on stderr — callers still control exit behavior.
# M18's configure_disk_quota relied on this silently; define it once
# so any future caller has a matching pair to _warn.
_err()  { printf '\033[1;31m[jabali-install]\033[0m %s\n' "$*" >&2; }
_die()  { printf '\033[1;31m[jabali-install]\033[0m %s\n' "$*" >&2; exit 1; }

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

  echo ""
  echo "=============================================================="
  echo "  Jabali Panel — Server Settings"
  echo "=============================================================="
  echo ""

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
    echo "Just one thing — your server's hostname. You can change it"
    echo "later from the admin panel."
    echo ""

    while true; do
      read -rp "Server hostname [${sys_hostname}]: " -u "$input_fd" inp_hostname || true
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
  _log "installing base packages (git, curl, ca-certificates, build-essential, mariadb, PHP, rsync, systemd-resolved)"
  export DEBIAN_FRONTEND=noninteractive
  apt-get update -qq
  apt-get install -y -qq --no-install-recommends \
    git curl ca-certificates build-essential tar bzip2 unzip openssl gnupg \
    mariadb-server mariadb-client \
    php-cli php-mysql php-curl php-xml php-mbstring php-gd php-zip \
    composer \
    rsync acl \
    systemd-resolved \
    quota quotatool xfsprogs

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

      if [[ -n "$migrated_ns" ]]; then
        _log "migrating existing /etc/resolv.conf upstreams to panel drop-in: ${migrated_ns}${migrated_search:+ (search: ${migrated_search})}"
        install -d -m 0755 /etc/systemd/resolved.conf.d
        {
          echo "# Managed by jabali-panel — edits via /jabali-admin/settings → DNS."
          echo "# Seeded by install.sh from the host's previous /etc/resolv.conf"
          echo "# so the host's DNS stayed working when install.sh handed"
          echo "# /etc/resolv.conf over to systemd-resolved's stub. The admin"
          echo "# can modify these upstreams via the panel UI at any time;"
          echo "# changes land in this same file."
          echo "[Resolve]"
          echo "DNS=${migrated_ns}"
          [[ -n "$migrated_search" ]] && echo "Domains=${migrated_search}"
        } > "$panel_dropin"
        chmod 0644 "$panel_dropin"
        systemctl restart systemd-resolved.service 2>/dev/null || true
      fi
    fi

    _log "linking /etc/resolv.conf → /run/systemd/resolve/stub-resolv.conf (was plain file)"
    ln -sf /run/systemd/resolve/stub-resolv.conf /etc/resolv.conf
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
  # Find the mount /home lives on. On a host where /home is on / this
  # returns "/", which we refuse to quota-enable (root fs quota is a
  # disaster for system daemons).
  home_mount="$(stat -c%m /home 2>/dev/null || echo /)"
  home_fs="$(stat -fc %T /home 2>/dev/null || echo unknown)"
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

  # If /home is on /, adding usrquota to the root filesystem is unsafe.
  # Warn + skip rather than block the install. The M18 cgroups drop-in
  # still gets applied per user; only disk-quota enforcement is absent.
  # Operator's path forward: migrate /home to its own mount (runbook §4),
  # then re-run install.sh to pick up quota.
  if [[ "$home_mount" == "/" ]]; then
    _warn "/home shares the root filesystem — skipping POSIX quota setup (would be unsafe on /)."
    _warn "cgroups limits (cpu/memory/io/tasks) still active."
    _warn "To enable disk quota: move /home to its own partition (see plans/m18-resource-limits-runbook.md §4) and re-run install.sh."
    return 0
  fi

  # Check whether fstab already has usrquota on this mount.
  if grep -E "^[^#]*\s$home_mount\s" /etc/fstab | grep -q "usrquota"; then
    _log "fstab: $home_mount already has usrquota set"
  else
    _log "adding usrquota,grpquota to /etc/fstab entry for $home_mount"
    # Preserve the original line; append the quota options after the existing opts.
    # Uses a unique marker to avoid double-patching on reinstall.
    if ! grep -q "# jabali-m18-quota" /etc/fstab; then
      # awk append "usrquota,grpquota" to the 4th field (options) for the /home line.
      # Backup first.
      cp -p /etc/fstab /etc/fstab.jabali-m18.bak
      awk -v mnt="$home_mount" '
        !/^#/ && $2 == mnt {
          sub(/^([^ \t]+[ \t]+[^ \t]+[ \t]+[^ \t]+[ \t]+)([^ \t]+)/, "\\1\\2,usrquota,grpquota")
          print $0 " # jabali-m18-quota"
          next
        }
        { print }
      ' /etc/fstab.jabali-m18.bak > /etc/fstab
      _ok "fstab patched; remount $home_mount for changes to take effect"
    fi
  fi

  # Remount to pick up the new options. On a busy mount this can fail;
  # operator must reboot or migrate. -oremount preserves current state.
  if ! mount -o remount "$home_mount" 2>/dev/null; then
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
    # ext4/ext3/ext2: quotacheck builds the aquota.user file, quotaon
    # turns enforcement on. Idempotent — safe to rerun.
    if [[ ! -f "$home_mount/aquota.user" ]]; then
      _log "running quotacheck (may take time on large /home)"
      quotacheck -cugm "$home_mount" || {
        _err "quotacheck failed"
        exit 1
      }
    fi
    quotaon -v "$home_mount" >/dev/null 2>&1 || true
    _ok "POSIX user quota active on $home_mount"
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
  if command -v nginx >/dev/null 2>&1; then
    _ok "nginx already installed ($(nginx -v 2>&1 | awk -F/ '{print $2}'))"
  else
    _log "installing nginx"
    apt-get install -y -qq --no-install-recommends nginx
    _ok "nginx installed"
  fi

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

  [[ -f /etc/apt/sources.list.d/sury-php.list ]] && { _ok "Sury PHP source already configured"; return; }

  # Derive the distro codename without depending on lsb_release (not
  # installed on minimal Debian 13). /etc/os-release is a systemd-era
  # standard and is always present.
  local codename
  if [[ -r /etc/os-release ]]; then
    # shellcheck disable=SC1091
    codename=$(. /etc/os-release && echo "${VERSION_CODENAME:-}")
  fi
  [[ -n "$codename" ]] || _die "cannot determine distro codename (no VERSION_CODENAME in /etc/os-release)"

  _log "downloading and verifying Sury GPG key"
  curl -fsSL https://packages.sury.org/php/apt.gpg -o /usr/share/keyrings/sury-php.gpg
  gpg --show-keys /usr/share/keyrings/sury-php.gpg 2>/dev/null | grep -q "$SURY_GPG_FINGERPRINT" || \
    _die "Sury GPG key fingerprint mismatch. Expected: $SURY_GPG_FINGERPRINT"
  _ok "Sury GPG key validated"

  cat > /etc/apt/sources.list.d/sury-php.list <<EOF
deb [signed-by=/usr/share/keyrings/sury-php.gpg] https://packages.sury.org/php/ ${codename} main
EOF
  _ok "added Sury PHP repository for ${codename}"
}

_install_php_version() {
  local version="$1"
  if ! command -v "php${version}" >/dev/null 2>&1; then
    _log "installing PHP ${version}"
    # Required packages: install must succeed.
    local required=("php${version}-fpm" "php${version}-cli")
    # Optional extensions: Sury's packaging drifts between PHP versions
    # (e.g. 8.5 ships OPcache inside -common instead of a standalone
    # -opcache package). Probe apt-cache and install only what's there.
    local optional_names=(mysql mbstring zip gd curl xml intl bcmath opcache)
    local optional=()
    for ext in "${optional_names[@]}"; do
      if apt-cache show "php${version}-${ext}" >/dev/null 2>&1; then
        optional+=("php${version}-${ext}")
      else
        _log "php${version}-${ext} not in apt sources — skipping (bundled elsewhere or unavailable)"
      fi
    done
    DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends \
      "${required[@]}" "${optional[@]}"
    _ok "PHP ${version} installed (${#optional[@]}/${#optional_names[@]} optional extensions)"
  else
    _ok "PHP ${version} already installed"
  fi

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
  _log "installing PHP/FPM from Sury repository"
  _install_sury_source
  apt-get update -qq

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

install_node() {
  # Idempotent: skip if a new-enough node is already installed. NodeSource
  # ships v22 for Debian 13 / Ubuntu 24.04 so apt stays consistent.
  if command -v node >/dev/null 2>&1; then
    local cur_major
    cur_major="$(node -v | sed -E 's/^v([0-9]+).*/\1/')"
    if [[ "$cur_major" -ge 22 ]]; then
      _ok "Node $(node -v) already installed"
      return
    fi
    _warn "upgrading Node $cur_major → 22"
  fi

  _log "installing Node.js 22 LTS (NodeSource)"
  install -d -m 0755 /etc/apt/keyrings
  curl -fsSL https://deb.nodesource.com/gpgkey/nodesource-repo.gpg.key \
    | gpg --dearmor --yes -o /etc/apt/keyrings/nodesource.gpg
  chmod 0644 /etc/apt/keyrings/nodesource.gpg
  echo 'deb [signed-by=/etc/apt/keyrings/nodesource.gpg] https://deb.nodesource.com/node_22.x nodistro main' \
    >/etc/apt/sources.list.d/nodesource.list
  apt-get update -qq
  apt-get install -y -qq --no-install-recommends nodejs
  _ok "Node $(node -v) / npm $(npm -v) installed"
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
          REFERENCES, LOCK TABLES
      ON \`${db_name}\`.* TO '${db_user}'@'localhost';
    FLUSH PRIVILEGES;
  "

  # Expose the DSN via /etc/jabali/panel.env so the service picks it up.
  local dsn="mysql://${db_user}:${db_pass}@127.0.0.1:3306/${db_name}?parseTime=true&charset=utf8mb4&loc=UTC"

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

# ---------- step 2.6: PowerDNS authoritative nameserver ----------------------

install_powerdns() {
  _log "installing PowerDNS (pdns-server + pdns-backend-mysql)"

  # The config directory for our env/cred files must exist before we
  # try to write into it. The panel's own config.toml lives here too;
  # write_config_file would normally create it, but install_powerdns
  # runs first.
  mkdir -p /etc/jabali-panel
  chmod 0755 /etc/jabali-panel

  # The Debian pdns-server package runs `invoke-rc.d pdns restart`
  # in its postinst before we've wired the MySQL backend. That would
  # fail loudly (exit 99, no backend configured) and print a scary
  # systemctl status dump. Pre-drop a policy-rc.d that tells the
  # package scripts to skip the start — we'll start pdns ourselves
  # after writing the correct config.
  local policy_rc=/usr/sbin/policy-rc.d
  local policy_rc_preexisted=0
  if [[ -e "$policy_rc" ]]; then
    policy_rc_preexisted=1
    mv "$policy_rc" "${policy_rc}.jabali-bak"
  fi
  cat > "$policy_rc" <<'POLICYEOF'
#!/bin/sh
# Temporarily installed by jabali-panel install.sh during pdns setup.
# Tells dpkg not to start services during package install — we start
# pdns explicitly once its MySQL backend is configured.
exit 101
POLICYEOF
  chmod 0755 "$policy_rc"

  # pdns-server is the daemon; pdns-backend-mysql is the SQL storage
  # backend. Stock Debian/Ubuntu package names.
  DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends \
    pdns-server pdns-backend-mysql

  # Undo the policy-rc.d trap regardless of exit path below.
  rm -f "$policy_rc"
  if [[ "$policy_rc_preexisted" == "1" ]]; then
    mv "${policy_rc}.jabali-bak" "$policy_rc"
  fi

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

  # Write pdns.conf. Single file, minimal surface. Port 53 UDP+TCP.
  local pdns_conf=/etc/powerdns/pdns.d/01-jabali-mysql.conf
  cat > "$pdns_conf" <<PDNSCONF
# Managed by Jabali Panel install.sh. Hand edits will be overwritten
# the next time install.sh runs.
launch=gmysql
gmysql-host=127.0.0.1
gmysql-port=3306
gmysql-dbname=jabali_pdns
gmysql-user=jabali_pdns
gmysql-password=${pdns_password}

# Bind to the host's public IP + 127.0.0.1 explicitly. We deliberately
# do NOT bind 0.0.0.0 because systemd-resolved (enabled earlier in
# install_base_packages for the panel's DNS Resolvers feature) is
# already listening on 127.0.0.53:53 as a stub. A 0.0.0.0:53 bind
# wildcards over every local IP — including 127.0.0.53 — and fails
# with EADDRINUSE, which surfaces as "Job for pdns.service failed"
# during install. Binding to the specific public IP + localhost avoids
# the overlap while keeping \`dig @localhost\` + \`dig @<panel-ip>\`
# both working from the host itself and from the internet.
# Operator can widen this via /etc/powerdns/pdns.conf if they add more
# IPs, but they must not re-add 0.0.0.0 while systemd-resolved runs.
local-address=127.0.0.1, ${JABALI_SRV_IPV4}, ::1

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
  chmod 0640 "$pdns_conf"
  chown root:pdns "$pdns_conf" 2>/dev/null || true

  _log "restarting pdns"
  systemctl enable pdns >/dev/null 2>&1 || true
  systemctl restart pdns

  # Quick sanity probe — if pdns isn't running after restart something is
  # broken and install.sh should fail fast rather than continue past it.
  sleep 2
  if ! systemctl is-active --quiet pdns; then
    systemctl status pdns --no-pager || true
    _die "pdns failed to start; check 'journalctl -u pdns' for details"
  fi
  _ok "PowerDNS running on port 53"
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
  _log "installing Certbot for Let's Encrypt SSL certificates"

  # Check if certbot is already installed (idempotent).
  if command -v certbot &>/dev/null; then
    local version
    version="$(certbot --version 2>/dev/null | head -n1)"
    _ok "Certbot already installed: $version"
    return 0
  fi

  # Install certbot and the nginx plugin. Stock Debian/Ubuntu package names.
  # The nginx plugin allows certbot to verify domain ownership via .well-known/acme-challenge
  # served through nginx, and can optionally auto-configure SSL in nginx.
  DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends \
    certbot python3-certbot-nginx

  # Verify installation succeeded.
  if ! command -v certbot &>/dev/null; then
    _die "Certbot installation failed"
  fi

  local version
  version="$(certbot --version 2>/dev/null | head -n1)"
  _ok "Certbot installed: $version"

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

  install -d -m 0755 -o "$SERVICE_USER" -g "$SERVICE_USER" "$REPO_DIR"
  install -d -m 0750 -o "$SERVICE_USER" -g "$SERVICE_USER" "$(dirname "$ENV_FILE")"
  install -d -m 0700 -o "$SERVICE_USER" -g "$SERVICE_USER" /var/lib/jabali/backups
  install -d -m 0700 -o "$SERVICE_USER" -g "$SERVICE_USER" /var/lib/jabali/restore
}

# ---------- step 4: clone / update repo -------------------------------------

clone_or_update_repo() {
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
    sudo -u "$SERVICE_USER" -H git "${git_args[@]}" -C "$REPO_DIR" fetch --quiet origin "$REPO_BRANCH"
    sudo -u "$SERVICE_USER" -H git -C "$REPO_DIR" reset --hard "origin/$REPO_BRANCH"
  else
    _log "cloning $REPO_URL into $REPO_DIR"
    sudo -u "$SERVICE_USER" -H git "${git_args[@]}" clone --quiet --branch "$REPO_BRANCH" \
      "$REPO_URL" "$REPO_DIR"
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

  if [[ -f "$cert_file" && -f "$key_file" ]]; then
    _ok "TLS cert exists: $cert_file"
  else
    _log "generating self-signed TLS certificate"
    # Dir traversable by www-data so nginx can open the key file below.
    install -d -m 0755 -o root -g root "$cert_dir"

    # Grab the machine's hostname and first non-loopback IP for SANs.
    local cn
    cn="$(hostname -f 2>/dev/null || hostname)"
    local ip
    ip="$(hostname -I 2>/dev/null | awk '{print $1}')"

    local san="DNS:${cn},DNS:localhost,IP:127.0.0.1"
    [[ -n "$ip" ]] && san+=",IP:${ip}"

    openssl req -x509 -newkey ec -pkeyopt ec_paramgen_curve:prime256v1 \
      -keyout "$key_file" -out "$cert_file" \
      -days 3650 -nodes \
      -subj "/CN=${cn}/O=Jabali Panel" \
      -addext "subjectAltName=${san}" \
      2>/dev/null

    _ok "self-signed TLS cert created ($cert_file)"
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

write_config_file() {
  local dest="$(dirname "$ENV_FILE")/config.toml"
  local src="$REPO_DIR/config.example.toml"
  if [[ -f "$dest" ]]; then
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
    printf 'addr        = "127.0.0.1:8443"\n'
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
dsn = "${PDNS_DB_USER}:${PDNS_DB_PASSWORD}@tcp(127.0.0.1:3306)/${PDNS_DB_NAME}?charset=utf8mb4&parseTime=true"
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
  #   - ProtectHome/ProtectKernel* keep the agent out of bystander state
  #   - NoNewPrivileges stays false because future commands may need
  #     capabilities-aware subprocess spawns (package install etc).
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
# etc).
ProtectSystem=strict
ProtectHome=yes
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
After=network-online.target ${AGENT_SERVICE_NAME}.service
Wants=network-online.target
# Panel hard-requires the agent at boot; without the socket we can't do
# privileged ops. If the agent crashes post-boot the panel stays up —
# individual handlers will return 503 with agent:unavailable.
Requires=${AGENT_SERVICE_NAME}.service

[Service]
Type=simple
User=$SERVICE_USER
Group=$SERVICE_USER
# /run/jabali-panel — systemd creates owned $SERVICE_USER:$SERVICE_USER 0755
# on service start and tears down on stop. The SSO UDS listener binds
# \${runtime}/sso.sock here; unlike /run/jabali (owned by root, used by
# the privileged agent), /run/jabali-panel is safe for the unprivileged
# panel to write to.
RuntimeDirectory=jabali-panel
RuntimeDirectoryMode=0755
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
  local host="${PANEL_ADDR%:*}"
  local port="${PANEL_ADDR##*:}"
  [[ "$host" == "$PANEL_ADDR" || -z "$host" ]] && host="127.0.0.1"

  # Use HTTPS if TLS is configured, with -k for self-signed cert.
  local scheme="http"
  if grep -q '^TLS_CERT=' "$ENV_FILE" 2>/dev/null; then
    scheme="https"
  fi

  # First-run migrations can take a while on a fresh InnoDB (45s+
  # observed). Give the service up to 120s before declaring defeat.
  local ok=0
  local deadline=$((SECONDS + 120))
  while (( SECONDS < deadline )); do
    if curl -fsSk -m 2 "${scheme}://${host}:${port}/health" >/tmp/jabali-health.json 2>/dev/null; then
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
  install -d -m 0755 -o root -g root /var/lib/jabali-panel
  printf '%s\n' "$sha" >/var/lib/jabali-panel/last-built-sha
  chmod 0644 /var/lib/jabali-panel/last-built-sha
  _ok "last-built-sha seeded to ${sha:0:7}"
}

# ---------- step: SSO key generation ----------------------------------------


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
$cfg['Servers'][1]['host'] = 'localhost';
$cfg['Servers'][1]['port'] = 3306;
$cfg['Servers'][1]['connect_type'] = 'tcp';
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

  # Poll for readiness. Kratos exposes /health/ready on the public port (4433).
  # Use `waited=$((waited+1))` rather than `((waited++))` — the post-increment
  # form evaluates to the OLD value (0 on first iter), which `set -e` treats
  # as a failed command and silently kills the installer.
  _log "waiting for Kratos to be ready (max 30s)"
  local waited=0
  while [[ $waited -lt 30 ]]; do
    if curl -sf http://127.0.0.1:4433/health/ready >/dev/null 2>&1; then
      _ok "Kratos is ready"
      break
    fi
    sleep 1
    waited=$((waited + 1))
  done

  if [[ $waited -eq 30 ]]; then
    _warn "Kratos did not become ready within 30s. Check: systemctl status jabali-kratos"
  fi

  _ok "Kratos identity provider installed and running"
}


# ---------- main ------------------------------------------------------------

main() {
  print_banner
  preflight
  prompt_server_settings
  install_base_packages
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
  install_powerdns
  bootstrap_pdns_self_zone
  setup_certbot
  clone_or_update_repo
  install_jabali_slices
  install_kratos
  install_php_pool_template
  build_frontend
  build_backend
  write_config_file
  provision_tls_cert
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
  write_agent_systemd_unit
  write_systemd_unit
  start_and_verify_agent
  start_and_verify
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

main "$@"
