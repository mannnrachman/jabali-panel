package middleware

import (
	"net/http"
	"slices"
	"strings"

	"github.com/gin-gonic/gin"
)

// corsAllowedHeaders is the static list sent back in preflight responses.
// Keeping it explicit (rather than echoing Access-Control-Request-Headers)
// means we document what the SPA is actually allowed to send.
var corsAllowedHeaders = []string{
	"Authorization",
	"Content-Type",
	"X-Device-Id",
	"X-Request-ID",
}

var corsAllowedMethods = []string{
	http.MethodGet,
	http.MethodPost,
	http.MethodPut,
	http.MethodPatch,
	http.MethodDelete,
	http.MethodOptions,
}

// CORS returns a middleware that reflects a whitelisted Origin back to the
// browser with Allow-Credentials=true so the SPA can send the refresh cookie.
//
// Design choices:
//   - We never emit "Access-Control-Allow-Origin: *" because it's incompatible
//     with Allow-Credentials=true. If the operator misconfigures allowed
//     origins to just "*", we treat it as "no allowed origins".
//   - Same-origin requests are auto-allowed: when the Origin's host:port
//     matches the request's Host header, we reflect the Origin back without
//     requiring the operator to add it to the whitelist. Firefox sends an
//     Origin header even on same-origin fetch/XHR, and without the reflected
//     headers the browser blocks the response (OpaqueResponseBlocking),
//     which manifested as a blank /login page after token expiry.
//   - Cross-origin requests still require an explicit whitelist entry.
//   - Vary: Origin is always appended on requests that carry Origin, so
//     intermediate caches don't poison responses across origins.
func CORS(allowedOrigins []string) gin.HandlerFunc {
	// Pre-sanitise the whitelist: drop "*" and any empty entries so the
	// hot path is a plain slice lookup.
	normalised := make([]string, 0, len(allowedOrigins))
	for _, o := range allowedOrigins {
		o = strings.TrimSpace(o)
		if o == "" || o == "*" {
			continue
		}
		normalised = append(normalised, o)
	}
	allowedMethodsHeader := strings.Join(corsAllowedMethods, ", ")
	allowedHeadersHeader := strings.Join(corsAllowedHeaders, ", ")

	return func(c *gin.Context) {
		origin := c.GetHeader("Origin")
		if origin == "" {
			c.Next()
			return
		}
		c.Writer.Header().Add("Vary", "Origin")

		if !slices.Contains(normalised, origin) && !isSameOrigin(origin, c.Request.Host) {
			// Not whitelisted and not same-origin — serve the request
			// (CORS is a browser check) but emit no Allow-* headers.
			// The browser will refuse to surface the response to the page.
			c.Next()
			return
		}

		c.Header("Access-Control-Allow-Origin", origin)
		c.Header("Access-Control-Allow-Credentials", "true")

		if c.Request.Method == http.MethodOptions {
			c.Header("Access-Control-Allow-Methods", allowedMethodsHeader)
			c.Header("Access-Control-Allow-Headers", allowedHeadersHeader)
			c.Header("Access-Control-Max-Age", "600")
			c.AbortWithStatus(http.StatusNoContent)
			return
		}

		c.Header("Access-Control-Expose-Headers", "X-Request-ID")
		c.Next()
	}
}

// isSameOrigin returns true when the Origin header's host:port authority
// matches the request's Host header — i.e. the browser is doing a same-origin
// fetch and just happens to include Origin. In that case the request has the
// same trust boundary as the serving page, so CORS is not protecting anything
// real; we reflect the Origin back so the browser surfaces the response.
//
// Only the authority (host:port) is compared. Scheme differences between
// page and API would be a mixed-content issue already blocked by the browser
// before this code sees the request.
func isSameOrigin(origin, host string) bool {
	if origin == "" || host == "" {
		return false
	}
	// Origin is "<scheme>://<host>[:<port>]"; strip the scheme.
	i := strings.Index(origin, "://")
	if i < 0 {
		return false
	}
	return origin[i+3:] == host
}
