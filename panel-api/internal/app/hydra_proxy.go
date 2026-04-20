package app

import (
	"net/http"
	"net/http/httputil"
	"net/url"

	"github.com/gin-gonic/gin"
)

// RegisterHydraProxy wires same-origin reverse proxies for every
// public Hydra endpoint OIDC clients and browsers need to reach. All
// routes forward to cfg.Auth.Hydra.PublicURL (typically
// http://127.0.0.1:4444, loopback-only by design).
//
// Why this exists: Hydra's OIDC discovery doc (`/.well-known/openid-
// configuration`) advertises fully-qualified URLs for token and
// userinfo. Those URLs must live on the same origin as the panel, or
// (a) browsers refuse to forward cookies on the cross-origin token
// fetch, and (b) CORS surface blows up for every installed OIDC
// client. Proxying in-process keeps the origin consistent with the
// panel domain and means each OIDC plugin (WP OpenID Connect Generic,
// etc.) needs exactly one URL — the panel's.
//
// Routes mounted (all forwarded path-unchanged to upstream):
//
//   - /oauth2/auth                       — authorization endpoint
//   - /oauth2/token                      — token endpoint
//   - /oauth2/revoke                     — token revocation (RFC 7009)
//   - /oauth2/sessions/logout            — RP-initiated logout (OIDC)
//   - /oauth2/fallbacks/consent          — Hydra's built-in consent page
//   - /oauth2/fallbacks/error            — Hydra's built-in error page
//   - /.well-known/openid-configuration  — OIDC discovery
//   - /.well-known/jwks.json             — JWKS for ID-token verification
//   - /userinfo                          — OIDC userinfo endpoint
//
// Hydra's admin endpoints (/admin/*, /health/*, /oauth2/introspect)
// are NEVER proxied — they stay loopback-only and are reached directly
// by hydraclient. Exposing the admin API would let any caller mint
// clients, accept consent, or revoke tokens.
//
// Unlike RegisterKratosProxy (which strips a /.ory prefix), each route
// below forwards path-unchanged — Hydra expects /oauth2/auth, not
// /hydra/oauth2/auth.
//
// Returns an error only if upstream is unparseable; any such failure
// happens at boot, not in a web request.
func RegisterHydraProxy(r *gin.Engine, upstream string) error {
	target, err := url.Parse(upstream)
	if err != nil {
		return err
	}

	// One shared httputil.ReverseProxy handles all routes. Director
	// rewrites scheme/host only — path stays verbatim because Hydra's
	// routes are ALREADY root-relative (/oauth2/..., /.well-known/..,
	// /userinfo). No prefix to strip.
	proxy := httputil.NewSingleHostReverseProxy(target)
	origDirector := proxy.Director
	proxy.Director = func(req *http.Request) {
		origDirector(req)
		req.Host = target.Host
	}

	handler := func(c *gin.Context) {
		proxy.ServeHTTP(c.Writer, c.Request)
	}

	// /oauth2/* covers auth, token, revoke, sessions/logout, fallbacks,
	// and any future sub-routes Hydra adds. Gin's wildcard catches them
	// all in one registration so a Hydra upgrade that adds, say,
	// /oauth2/device/auth (device code grant) doesn't need a panel-side
	// route addition.
	r.Any("/oauth2/*proxyPath", handler)

	// /.well-known/* covers openid-configuration AND jwks.json. Same
	// wildcard logic as above — one registration forwards every
	// discovery doc Hydra exposes.
	r.Any("/.well-known/*proxyPath", handler)

	// /userinfo is a single endpoint, not a tree, so register it as
	// exact. GETs carry the bearer token; POSTs are also allowed per
	// OIDC spec (refresh-token flow compatibility).
	r.Any("/userinfo", handler)

	return nil
}
