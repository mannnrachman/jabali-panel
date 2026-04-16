# 0014 — PANEL_PORT 8443, user sites on 443

## Status
Accepted — 2026-04-16

## Context
A panel server must listen on HTTPS. User domains also need HTTPS. Both cannot listen on 443 simultaneously. Nginx can reverse-proxy the panel on 8443, freeing 443 for user vhosts.

## Decision
Nginx listens on 443 and serves user vhosts. Panel-api binds directly to 8443 with TLS (terminating). Nginx on 8443 reverse-proxies to panel-api and also handles the WSS tunnel (for Bulwark webmail, future). Users access the panel at `https://panel.<node>:8443/`.

## Consequences

### Positive
- User domains get 443 for SSL handshake (standard)
- Let's Encrypt doesn't interfere with panel cert
- Panel can have a self-signed cert (internal)
- Nginx can handle multiple upstreams (panel-api, tunnel) on one port

### Negative
- Admin must remember `:8443` suffix (not on the default HTTPS port)
- Firewall rules must allow 8443 ingress
- TLS termination on two layers (nginx + panel-api) wastes CPU

### Neutral
- Nginx and panel-api certs can be managed independently

## Alternatives considered

- **Panel on 443 `/admin` subpath**: Rejected — breaks user domain SSL handshake (SNI mismatch)
- **Panel on separate IP**: Rejected — requires extra IP allocation and complexity
- **Panel on non-standard port like 8080 (HTTP only)**: Rejected — insecure for admin panel

## References
- `panel-api/cmd/panel-api/main.go` — TLS listener on 8443
- `nginx.conf` — reverse-proxy config for 8443
- `docs/install.md` — port assignment documentation
