// Package middleware holds the gin.HandlerFuncs the panel composes into
// its route chains: request IDs, CORS, JWT validation, role guards, and
// per-IP rate limiting.
package middleware

import (
	"regexp"

	"github.com/gin-gonic/gin"

	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/ginctx"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/ids"
)

// requestIDHeader is the conventional header name. Also exposed on responses
// so clients can correlate logs when reporting issues.
const requestIDHeader = "X-Request-ID"

// requestIDAllowed is deliberately restrictive: ASCII alphanumerics plus a
// handful of separators, bounded length. It's enough for callers that pass
// UUIDs, ULIDs, or their own tracer IDs; anything else is replaced so we
// never log or echo attacker-controlled bytes.
var requestIDAllowed = regexp.MustCompile(`^[A-Za-z0-9._\-]{1,128}$`)

// RequestID returns a middleware that ensures every request has a stable
// identifier on the context and the response. If the client sends a valid
// X-Request-ID header, it is used; otherwise a ULID is minted.
func RequestID() gin.HandlerFunc {
	return func(c *gin.Context) {
		id := c.GetHeader(requestIDHeader)
		if !requestIDAllowed.MatchString(id) {
			id = ids.NewULID()
		}
		ginctx.SetRequestID(c, id)
		c.Header(requestIDHeader, id)
		c.Next()
	}
}
