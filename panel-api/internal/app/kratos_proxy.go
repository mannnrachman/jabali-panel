package app

import (
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"

	"github.com/gin-gonic/gin"
)

// RegisterKratosProxy wires a same-origin reverse proxy at /.ory/* that
// forwards to the Kratos public HTTP API (cfg.Auth.Kratos.PublicURL).
//
// Why this exists: panel-api binds :8443 directly and serves the SPA from
// that port. The browser issues axios.get("/.ory/self-service/login/browser")
// as a relative URL, which resolves to https://<host>:8443/.ory/... On that
// port there is no nginx in front, so without this proxy the path falls
// through NoRoute and the SPA static handler returns index.html — axios then
// tries to JSON-parse the HTML and `flow.ui` ends up undefined, crashing
// the login page with "e.ui is undefined" before the user can even type.
//
// The proxy is same-origin on purpose: Kratos sets cookies (csrf_token,
// ory_kratos_session) with the host-only default — those cookies travel
// on any port of the panel hostname, so we don't need to stamp Domain.
// Path is preserved as-is (Kratos expects `/self-service/login/browser`,
// so we strip the `/.ory` prefix on the way through).
//
// upstream must be a complete URL (e.g. http://127.0.0.1:4433). Returns
// an error only if upstream is unparseable — panics should stay out of
// the web request path.
func RegisterKratosProxy(r *gin.Engine, upstream string) error {
	target, err := url.Parse(upstream)
	if err != nil {
		return err
	}

	// httputil.NewSingleHostReverseProxy handles scheme/host/port rewriting
	// and copies request/response headers. We layer a Director wrapper on
	// top so we can trim the `/.ory` prefix from the path — without this,
	// Kratos would see /.ory/self-service/login/browser and 404.
	proxy := httputil.NewSingleHostReverseProxy(target)
	origDirector := proxy.Director
	proxy.Director = func(req *http.Request) {
		origDirector(req)
		req.URL.Path = strings.TrimPrefix(req.URL.Path, "/.ory")
		if req.URL.Path == "" {
			req.URL.Path = "/"
		}
		// Force Host to the upstream so nginx-less setups don't confuse
		// Kratos about which vhost is talking to it. NewSingleHostReverseProxy
		// already sets req.URL.Host but not req.Host when the caller's Host
		// header differs from target.
		req.Host = target.Host
	}

	// Any method; Kratos endpoints include GET (flow init), POST (submit),
	// and DELETE (logout). Gin's Any() is the idiomatic wildcard.
	r.Any("/.ory/*proxyPath", func(c *gin.Context) {
		proxy.ServeHTTP(c.Writer, c.Request)
	})
	return nil
}
