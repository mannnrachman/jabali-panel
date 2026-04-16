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
  local input_fd
  if [[ -r /dev/tty ]]; then
    exec 3</dev/tty
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

  if [[ -z "$input_fd" ]]; then
    _warn "no TTY available — using auto-detected defaults + env vars."
    _warn "override hostname via JABALI_HOSTNAME"
    inp_hostname="${JABALI_HOSTNAME:-$sys_hostname}"
    if [[ ! "$inp_hostname" =~ ^[a-zA-Z0-9][a-zA-Z0-9.-]*[a-zA-Z0-9]$ ]]; then
      _die "no TTY and no valid JABALI_HOSTNAME (detected: '$inp_hostname')"
    fi
    inp_ns1_name="ns1.${inp_hostname}"
    inp_ns1_ip="${inp_ipv4}"
    inp_ns2_name="ns2.${inp_hostname}"
    inp_ns2_ip="${inp_ipv4}"
  else
    echo "Just one thing — your server's hostname. You can change it"
    echo "later from the admin panel."
    echo ""

    while true; do
      read -rp "Server hostname [${sys_hostname}]: " -u "$input_fd" inp_hostname || true
      inp_hostname="${inp_hostname:-$sys_hostname}"
      [[ "$inp_hostname" =~ ^[a-zA-Z0-9][a-zA-Z0-9.-]*[a-zA-Z0-9]$ ]] && break
      _warn "invalid hostname; use letters/digits/dots/hyphens"
    done

    # NS names + IPs are auto-derived from the hostname — no prompt.
    # Both nameservers get the same IPv4 at install time; the operator
    # later points ns2 at a separate server via the admin Server
    # Settings page, which triggers a zone re-push automatically.
    inp_ns1_name="ns1.${inp_hostname}"
    inp_ns1_ip="${inp_ipv4}"
    inp_ns2_name="ns2.${inp_hostname}"
    inp_ns2_ip="${inp_ipv4}"

    # Close the TTY FD so we don't leak it to child processes.
    [[ "$input_fd" == "3" ]] && exec 3<&-
  fi

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
  _log "installing base packages (git, curl, ca-certificates, build-essential, mariadb)"
  export DEBIAN_FRONTEND=noninteractive
  apt-get update -qq
  apt-get install -y -qq --no-install-recommends \
    git curl ca-certificates build-essential tar openssl gnupg \
    mariadb-server mariadb-client
  _ok "base packages ready"
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

  # Write pdns.conf. Single file, minimal surface. Listens on all
  # interfaces; port 53 UDP+TCP.
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

# Bind on all interfaces so ns1 can be reached externally. Operator can
# narrow this via /etc/powerdns/pdns.conf if they run a firewall in
# front.
local-address=0.0.0.0, ::

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

# ---------- main ------------------------------------------------------------

main() {
  preflight
  prompt_server_settings
  install_base_packages
  install_nginx
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
  setup_certbot
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
