#!/usr/bin/env bash
# jabali-panel-cert.sh — certbot deploy-hook for the panel hostname.
#
# install.sh drops this at /etc/letsencrypt/renewal-hooks/deploy/
# during install_jabali_panel_cert_hook (M32 Step 3). Certbot
# invokes it on every successful renewal AND the panel-agent
# invokes it directly after first-issue in ssl.panel.issue.
#
# Inputs:
#   $RENEWED_LINEAGE — set by certbot (or by ssl.panel.issue) to the
#                      path of the LE lineage directory, typically
#                      /etc/letsencrypt/live/<hostname>. Falls back
#                      to deriving from `hostname -f` so a hand
#                      invocation outside certbot still works.
#
# Side effects:
#   1. Copies fullchain.pem → /etc/jabali/tls/panel.crt (root:jabali 0640).
#   2. Copies privkey.pem   → /etc/jabali/tls/panel.key (root:jabali 0640).
#   3. Reloads nginx (graceful — keeps :443 user-domain TLS up).
#   4. Restarts jabali-panel.service (panel-api re-reads cert at startup).
#   5. Restarts jabali-bulwark.service (Bulwark Next custom server reads
#      from /etc/jabali/tls/ at boot for mail.<hostname> coverage).
#
# Idempotent: re-running with no actual cert change still rewrites
# the destination files (atomic via install -m), reloads nginx, and
# restarts the panel + Bulwark. Safe for certbot's daily timer.
set -euo pipefail

cn="$(hostname -f 2>/dev/null || hostname)"
src="${RENEWED_LINEAGE:-/etc/letsencrypt/live/${cn}}"

if [[ ! -d "$src" ]]; then
  echo "jabali-panel-cert.sh: no LE lineage at $src — nothing to deploy" >&2
  exit 0
fi
if [[ ! -f "$src/fullchain.pem" || ! -f "$src/privkey.pem" ]]; then
  echo "jabali-panel-cert.sh: lineage at $src missing fullchain.pem or privkey.pem" >&2
  exit 1
fi

dst_dir="/etc/jabali/tls"
install -d -m 0755 -o root -g root "$dst_dir"

# Use install(1) so the destination is replaced atomically and the
# perms/owner land in the same syscall — no half-readable cert
# during the swap.
install -m 0640 -o root -g jabali "$src/fullchain.pem" "$dst_dir/panel.crt"
install -m 0640 -o root -g jabali "$src/privkey.pem"   "$dst_dir/panel.key"

# Reload chain. Order matters:
#   - nginx first: a graceful reload picks up the new cert without
#     dropping any existing :443 user-domain TLS sessions.
#   - jabali-panel next: panel-api reads the cert at startup; restart
#     forces it to re-tls-handshake on the next request.
#   - jabali-bulwark last: Next custom server reads from
#     /etc/jabali/tls at boot, so the webmail :443 host swap picks
#     up the new cert. Brief (~3-5s) webmail blip is accepted.
systemctl reload nginx           || echo "jabali-panel-cert.sh: nginx reload failed (continuing)" >&2
systemctl restart jabali-panel   || echo "jabali-panel-cert.sh: jabali-panel restart failed (continuing)" >&2
systemctl restart jabali-bulwark || echo "jabali-panel-cert.sh: jabali-bulwark restart failed (continuing)" >&2

exit 0
