#!/usr/bin/env bash
# jabali-stalwart-push-cert.sh — push /etc/jabali/tls/panel-mail.{crt,key}
# into Stalwart as the default TLS certificate (Certificate object via
# the JMAP management API). Without this, Stalwart serves its rcgen
# self-signed cert (CN=rcgen self signed cert, SAN=localhost) on
# IMAPS/465/587 and every external mail client rejects the handshake.
#
# Idempotent: deletes any previously-pushed Certificate named
# "jabali-panel-mail" then re-creates it from the current on-disk PEM.
# Safer than update-or-create because Stalwart treats Certificate.name
# as immutable — a content drift between disk + Stalwart resolves to
# "create with new contents" rather than partial updates.
#
# Invoked by:
#   - install.sh after _install_stalwart_apply (first push)
#   - install/letsencrypt/jabali-panel-cert.sh on certbot renewal of the
#     mail.<panel-hostname> lineage (kind=mail)
#
# Reads creds from /etc/jabali-panel/stalwart.env (STALWART_RECOVERY_ADMIN
# = "admin:<password>"). Talks to the loopback JMAP listener on
# 127.0.0.1:8446. set -euo pipefail.

set -euo pipefail

CERT_PATH="/etc/jabali/tls/panel-mail.crt"
KEY_PATH="/etc/jabali/tls/panel-mail.key"
CERT_NAME="jabali-panel-mail"
STW_URL="${STALWART_URL:-http://127.0.0.1:8446}"
STW_ENV="/etc/jabali-panel/stalwart.env"
STW_CLI="/usr/local/bin/stalwart-cli"

log() { echo "jabali-stalwart-push-cert: $*" >&2; }

if [[ ! -x "$STW_CLI" ]]; then
  log "stalwart-cli not found at $STW_CLI — skipping"
  exit 0
fi
if [[ ! -f "$CERT_PATH" || ! -f "$KEY_PATH" ]]; then
  log "$CERT_PATH or $KEY_PATH missing — nothing to push"
  exit 0
fi
if [[ ! -f "$STW_ENV" ]]; then
  log "$STW_ENV missing — cannot resolve admin creds, skipping"
  exit 0
fi
if ! command -v jq >/dev/null; then
  log "jq missing — required for JSON-escape of PEM contents, skipping"
  exit 0
fi

# Parse STALWART_RECOVERY_ADMIN=admin:<password> from the env file.
admin_line="$(grep -E '^STALWART_RECOVERY_ADMIN=' "$STW_ENV" | head -1 | cut -d= -f2-)"
if [[ -z "$admin_line" ]]; then
  log "STALWART_RECOVERY_ADMIN not set in $STW_ENV — cannot push, skipping"
  exit 0
fi
admin_user="${admin_line%%:*}"
admin_pass="${admin_line#*:}"
if [[ -z "$admin_user" || -z "$admin_pass" ]]; then
  log "STALWART_RECOVERY_ADMIN malformed in $STW_ENV — cannot push, skipping"
  exit 0
fi

# Wait briefly for Stalwart to come up. The mgmt API is the same socket
# Stalwart starts listening on after RocksDB open + Bootstrap apply.
ready=0
for _ in 1 2 3 4 5 6 7 8 9 10; do
  if STALWART_URL="$STW_URL" STALWART_USER="$admin_user" STALWART_PASSWORD="$admin_pass" \
      "$STW_CLI" query x:Certificate --json >/dev/null 2>&1; then
    ready=1
    break
  fi
  sleep 2
done
if [[ "$ready" != "1" ]]; then
  log "stalwart-cli could not reach $STW_URL after 10 retries — leaving cert un-pushed"
  exit 0
fi

# Delete any prior Certificate with the same name so the next create
# carries the fresh PEM. stalwart-cli delete swallows "not found" — no
# need to pre-check existence.
prior_id="$(STALWART_URL="$STW_URL" STALWART_USER="$admin_user" STALWART_PASSWORD="$admin_pass" \
  "$STW_CLI" query x:Certificate --json 2>/dev/null \
  | jq -r ".[] | select(.id == \"$CERT_NAME\") | .id" \
  | head -1)"
if [[ -n "$prior_id" ]]; then
  STALWART_URL="$STW_URL" STALWART_USER="$admin_user" STALWART_PASSWORD="$admin_pass" \
    "$STW_CLI" delete x:Certificate --ids "$prior_id" >/dev/null 2>&1 || true
fi

# Build the create plan with JSON-escaped PEM. jq -Rs reads stdin raw +
# emits a single JSON-string literal — handles newlines, quotes, the
# whole bundle.
cert_pem_json="$(jq -Rs . <"$CERT_PATH")"
key_pem_json="$(jq -Rs . <"$KEY_PATH")"

plan_file="$(mktemp -t jabali-stalwart-cert-plan.XXXXXX.json)"
trap 'rm -f "$plan_file"' EXIT
cat >"$plan_file" <<EOF
[
  {
    "@type": "create",
    "object": "x:Certificate",
    "value": {
      "$CERT_NAME": {
        "certificate": $cert_pem_json,
        "privateKey": $key_pem_json
      }
    }
  }
]
EOF

STALWART_URL="$STW_URL" STALWART_USER="$admin_user" STALWART_PASSWORD="$admin_pass" \
  "$STW_CLI" apply --continue-on-error --file "$plan_file" >/dev/null 2>&1 || {
    log "stalwart-cli apply failed — Stalwart will keep serving the previous cert"
    exit 0
  }

log "pushed Stalwart Certificate '$CERT_NAME' from $CERT_PATH ($(stat -c %Y "$CERT_PATH" | xargs -I{} date -d @{} -Iseconds))"
