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
# Usage:
#   curl -fsSL https://git.linux-hosting.co.il/shukivaknin/jabali2/raw/branch/main/install.sh | bash
#   # or with a token for a private repo:
#   curl -fsSL <...>/install.sh | bash -s -- <GITEA_TOKEN>
#   # or local checkout:
#   JABALI_GITEA_TOKEN=xxx bash install.sh

set -euo pipefail

# ---------- config (override via env) ---------------------------------------

REPO_URL="${JABALI_REPO_URL:-https://git.linux-hosting.co.il/shukivaknin/jabali2.git}"
REPO_BRANCH="${JABALI_REPO_BRANCH:-main}"
REPO_DIR="${JABALI_REPO_DIR:-/opt/jabali-panel}"
GO_VERSION="${JABALI_GO_VERSION:-1.25.1}"
GO_ROOT="${JABALI_GO_ROOT:-/usr/local/go}"
SERVICE_USER="${JABALI_SERVICE_USER:-jabali}"
SERVICE_NAME="${JABALI_SERVICE_NAME:-jabali-panel}"
PANEL_ADDR="${JABALI_PANEL_ADDR:-127.0.0.1:8443}"
BIN_PATH="/usr/local/bin/jabali-panel"
ENV_FILE="/etc/jabali/panel.env"
GITEA_TOKEN="${JABALI_GITEA_TOKEN:-${1:-}}"

# ---------- tiny logger -----------------------------------------------------

_log()  { printf '\033[1;34m[jabali-install]\033[0m %s\n' "$*"; }
_ok()   { printf '\033[1;32m[jabali-install]\033[0m %s\n' "$*"; }
_warn() { printf '\033[1;33m[jabali-install]\033[0m %s\n' "$*" >&2; }
_die()  { printf '\033[1;31m[jabali-install]\033[0m %s\n' "$*" >&2; exit 1; }

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

# ---------- step 1: base packages -------------------------------------------

install_base_packages() {
  _log "installing base packages (git, curl, ca-certificates, build-essential)"
  export DEBIAN_FRONTEND=noninteractive
  apt-get update -qq
  apt-get install -y -qq --no-install-recommends \
    git curl ca-certificates build-essential tar
  _ok "base packages ready"
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
    useradd --system --home-dir "$REPO_DIR" --shell /usr/sbin/nologin \
      --comment "Jabali Panel service user" "$SERVICE_USER"
  else
    _ok "user '$SERVICE_USER' exists"
  fi

  install -d -m 0755 -o "$SERVICE_USER" -g "$SERVICE_USER" "$REPO_DIR"
  install -d -m 0750 -o "$SERVICE_USER" -g "$SERVICE_USER" "$(dirname "$ENV_FILE")"
}

# ---------- step 4: clone / update repo -------------------------------------

clone_or_update_repo() {
  local clone_url="$REPO_URL"
  if [[ -n "$GITEA_TOKEN" ]]; then
    # Inject token only for this operation, then strip it from the persisted
    # remote URL so `git remote -v` doesn't leak it.
    clone_url="$(echo "$REPO_URL" | sed "s|https://|https://oauth2:${GITEA_TOKEN}@|")"
  fi

  if [[ -d "$REPO_DIR/.git" ]]; then
    _log "pulling latest $REPO_BRANCH into $REPO_DIR"
    sudo -u "$SERVICE_USER" -H git -C "$REPO_DIR" fetch --quiet origin "$REPO_BRANCH"
    sudo -u "$SERVICE_USER" -H git -C "$REPO_DIR" reset --hard "origin/$REPO_BRANCH"
  else
    _log "cloning $REPO_URL into $REPO_DIR"
    sudo -u "$SERVICE_USER" -H git clone --quiet --branch "$REPO_BRANCH" \
      "$clone_url" "$REPO_DIR"
    # Strip token from saved remote
    sudo -u "$SERVICE_USER" -H git -C "$REPO_DIR" remote set-url origin "$REPO_URL"
  fi
  _ok "repo at $(sudo -u "$SERVICE_USER" -H git -C "$REPO_DIR" rev-parse --short HEAD)"
}

# ---------- step 5: build backend -------------------------------------------

build_backend() {
  _log "building panel-api"
  local version
  version="$(sudo -u "$SERVICE_USER" -H git -C "$REPO_DIR" rev-parse --short HEAD)"

  # Build into a temp path as the service user (no write to /usr/local needed
  # during compile), then atomically move into place as root.
  local tmpbin="$REPO_DIR/bin/jabali-panel.new"
  install -d -m 0755 -o "$SERVICE_USER" -g "$SERVICE_USER" "$REPO_DIR/bin"

  sudo -u "$SERVICE_USER" -H env \
    PATH="$GO_ROOT/bin:/usr/bin:/bin" \
    HOME="$REPO_DIR" \
    GOCACHE="$REPO_DIR/.cache/go-build" \
    GOMODCACHE="$REPO_DIR/.cache/go-mod" \
    bash -c "cd '$REPO_DIR' && go build -trimpath -ldflags '-s -w -X git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/api.Version=$version' -o '$tmpbin' ./panel-api/cmd/server"

  install -m 0755 "$tmpbin" "$BIN_PATH"
  rm -f "$tmpbin"
  _ok "installed $BIN_PATH (version=$version)"
}

# ---------- step 6: env file + systemd unit ---------------------------------

write_env_file() {
  if [[ -f "$ENV_FILE" ]]; then
    _ok "env file exists: $ENV_FILE (not overwriting)"
    return
  fi
  _log "writing env file: $ENV_FILE"
  cat >"$ENV_FILE" <<EOF
# Jabali Panel — environment for jabali-panel.service
# Generated $(date -Iseconds). Edit as needed, then: systemctl restart $SERVICE_NAME
# Secrets belong here (DATABASE_URL, JWT_SECRET). Non-secret config goes in
# $(dirname "$ENV_FILE")/config.toml.

PANEL_ADDR=$PANEL_ADDR
PANEL_ENV=production
EOF
  chmod 0640 "$ENV_FILE"
  chown root:"$SERVICE_USER" "$ENV_FILE"
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
}

write_systemd_unit() {
  _log "writing systemd unit: /etc/systemd/system/${SERVICE_NAME}.service"
  cat >"/etc/systemd/system/${SERVICE_NAME}.service" <<EOF
[Unit]
Description=Jabali Panel API
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=$SERVICE_USER
Group=$SERVICE_USER
EnvironmentFile=$ENV_FILE
ExecStart=$BIN_PATH
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
  systemctl enable --quiet "$SERVICE_NAME.service"
}

# ---------- step 7: start + smoke test --------------------------------------

start_and_verify() {
  _log "starting $SERVICE_NAME"
  systemctl restart "$SERVICE_NAME"

  _log "waiting for /health"
  local host="${PANEL_ADDR%:*}"
  local port="${PANEL_ADDR##*:}"
  [[ "$host" == "$PANEL_ADDR" || -z "$host" ]] && host="127.0.0.1"

  local ok=0
  for i in 1 2 3 4 5 6 7 8 9 10; do
    if curl -fsS -m 2 "http://${host}:${port}/health" >/tmp/jabali-health.json 2>/dev/null; then
      ok=1; break
    fi
    sleep 0.5
  done

  if (( ok == 0 )); then
    _warn "health probe failed; dumping last 20 log lines"
    journalctl -u "$SERVICE_NAME" -n 20 --no-pager || true
    _die "$SERVICE_NAME did not come up"
  fi

  _ok "health OK: $(cat /tmp/jabali-health.json)"
  rm -f /tmp/jabali-health.json
}

# ---------- main ------------------------------------------------------------

main() {
  preflight
  install_base_packages
  install_go
  ensure_user_and_dirs
  clone_or_update_repo
  build_backend
  write_env_file
  write_config_file
  write_systemd_unit
  start_and_verify
  _ok "jabali-panel installed. Status: systemctl status $SERVICE_NAME"
}

main "$@"
