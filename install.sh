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
# If you fork the repo privately, pass a Gitea token to authenticate clone/pull:
#   curl -fsSL <...>/install.sh | bash -s -- <GITEA_TOKEN>
#   # or:
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
  _log "installing base packages (git, curl, ca-certificates, build-essential, mariadb)"
  export DEBIAN_FRONTEND=noninteractive
  apt-get update -qq
  apt-get install -y -qq --no-install-recommends \
    git curl ca-certificates build-essential tar openssl gnupg \
    mariadb-server mariadb-client
  _ok "base packages ready"
}

# ---------- step 1b: Node.js 22 LTS (for panel-ui) --------------------------

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
  _ok "installed $BIN_PATH (version=$version)"
  _ok "installed $AGENT_BIN_PATH (version=$version)"
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
    install -d -m 0750 -o root -g "$SERVICE_USER" "$cert_dir"

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

    chmod 0640 "$key_file" "$cert_file"
    chown root:"$SERVICE_USER" "$key_file" "$cert_file"
    _ok "self-signed TLS cert created ($cert_file)"
  fi

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

  local ok=0
  for i in 1 2 3 4 5 6 7 8 9 10; do
    if curl -fsSk -m 2 "${scheme}://${host}:${port}/health" >/tmp/jabali-health.json 2>/dev/null; then
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
  install_node
  install_go
  ensure_user_and_dirs
  # Order matters: write_env_file seeds PANEL_ADDR / PANEL_ENV / JWT_SECRET
  # hooks BEFORE provision_mariadb appends DATABASE_URL. Reversing the two
  # would leave a fresh install with only the DB URL and no server config.
  write_env_file
  provision_mariadb
  clone_or_update_repo
  build_frontend
  build_backend
  write_config_file
  provision_tls_cert
  seed_admin_env
  write_agent_systemd_unit
  write_systemd_unit
  start_and_verify_agent
  start_and_verify
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
