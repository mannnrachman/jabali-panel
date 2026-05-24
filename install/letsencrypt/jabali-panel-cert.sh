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
    # Push the renewed PEM into Stalwart's Certificate object so
    # IMAPS / 465 / 587 serve the LE cert instead of Stalwart's rcgen
    # self-signed fallback. Idempotent; safe to call multiple times.
    # The script waits for Stalwart to come up after the restart above.
    if [[ -x /usr/local/bin/jabali-stalwart-push-cert ]]; then
      /usr/local/bin/jabali-stalwart-push-cert || \
        echo "jabali-panel-cert.sh: jabali-stalwart-push-cert non-zero (continuing)" >&2
    fi
    ;;
  *)
    install -m 0640 -o root -g jabali "$src/fullchain.pem" "$dst_dir/panel.crt"
    install -m 0640 -o root -g jabali "$src/privkey.pem"   "$dst_dir/panel.key"
    systemctl reload nginx           || echo "jabali-panel-cert.sh: nginx reload failed (continuing)" >&2
    # --no-block on jabali-panel: this hook is invoked synchronously by
    # the panel-agent ssl.panel.issue command, whose CALLER is
    # jabali-panel's own panel-cert reconciler. A blocking
    # `systemctl restart jabali-panel` SIGTERMs the panel mid-RPC, so
    # the reconciler never reaches MarkIssued and the row stays
    # pending_acme forever — every tick re-dispatches, re-runs this
    # hook, re-kills the panel (observed on mx: clean SIGTERM every
    # ~10 min, NRestarts=0, row never leaving pending_acme). --no-block
    # queues the restart so the agent reply propagates and the
    # reconciler commits status=issued BEFORE systemd fires it; the
    # panel then restarts into an already-issued row and does not
    # re-dispatch. jabali-bulwark is not the caller — synchronous.
    systemctl restart --no-block jabali-panel || echo "jabali-panel-cert.sh: jabali-panel restart failed (continuing)" >&2
    systemctl restart jabali-bulwark || echo "jabali-panel-cert.sh: jabali-bulwark restart failed (continuing)" >&2
    ;;
esac

exit 0
