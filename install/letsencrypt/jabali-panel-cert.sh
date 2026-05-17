#!/usr/bin/env bash
# jabali-panel-cert.sh — certbot deploy-hook for the panel certs.
#
# install.sh drops this at /etc/letsencrypt/renewal-hooks/deploy/
# during install_jabali_panel_cert_hook. Certbot invokes it on every
# successful renewal AND the panel-agent invokes it directly after
# first-issue in ssl.panel.issue.
#
# Post ADR-0105 the panel has TWO independent certs:
#   kind=hostname  → /etc/jabali/tls/panel.{crt,key};
#                    reload nginx, restart jabali-panel + jabali-bulwark.
#   kind=mail      → /etc/jabali/tls/panel-mail.{crt,key};
#                    reload nginx, restart jabali-stalwart.
#
# Inputs:
#   $RENEWED_LINEAGE        — LE lineage dir (certbot or ssl.panel.issue).
#   $JABALI_PANEL_CERT_KIND — "hostname" (default) or "mail". On a real
#                             certbot renewal the var is absent, so the
#                             kind is derived from the lineage basename
#                             (mail.* → mail) to keep unattended
#                             renewals correct.
#
# Idempotent + best-effort reloads. set -euo pipefail.
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

# Resolve kind: explicit env wins; otherwise infer from the lineage
# name so certbot's unattended timer routes mail.* correctly.
kind="${JABALI_PANEL_CERT_KIND:-}"
if [[ -z "$kind" ]]; then
  case "$(basename "$src")" in
    mail.*) kind="mail" ;;
    *)      kind="hostname" ;;
  esac
fi

dst_dir="/etc/jabali/tls"
install -d -m 0755 -o root -g root "$dst_dir"

case "$kind" in
  mail)
    install -m 0640 -o root -g jabali "$src/fullchain.pem" "$dst_dir/panel-mail.crt"
    install -m 0640 -o root -g jabali "$src/privkey.pem"   "$dst_dir/panel-mail.key"
    # nginx serves mail.<hostname> :443; Stalwart reads the mail cert
    # for SMTP/IMAPS. jabali-panel / jabali-bulwark use the hostname
    # cert and are deliberately NOT bounced here.
    systemctl reload nginx            || echo "jabali-panel-cert.sh: nginx reload failed (continuing)" >&2
    systemctl restart jabali-stalwart || echo "jabali-panel-cert.sh: jabali-stalwart restart failed (continuing)" >&2
    ;;
  *)
    install -m 0640 -o root -g jabali "$src/fullchain.pem" "$dst_dir/panel.crt"
    install -m 0640 -o root -g jabali "$src/privkey.pem"   "$dst_dir/panel.key"
    systemctl reload nginx           || echo "jabali-panel-cert.sh: nginx reload failed (continuing)" >&2
    systemctl restart jabali-panel   || echo "jabali-panel-cert.sh: jabali-panel restart failed (continuing)" >&2
    systemctl restart jabali-bulwark || echo "jabali-panel-cert.sh: jabali-bulwark restart failed (continuing)" >&2
    ;;
esac

exit 0
