// Package ginctx centralises the keys and typed accessors we use to pass
// request-scoped values through a Gin context.
//
// Keeping keys here (rather than scattered string literals across handlers)
// prevents typo bugs and gives us one place to audit what ends up on the
// context.
package ginctx

import (
	"github.com/gin-gonic/gin"

	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/auth"
)

// Keys. Exported so middleware + handlers can share them.
const (
	KeyRequestID = "request_id"
	KeyClaims    = "access_claims"
)

// SetRequestID stores id on the context.
func SetRequestID(c *gin.Context, id string) { c.Set(KeyRequestID, id) }

// RequestID returns the request ID set by the RequestID middleware, or ""
// if the middleware didn't run (e.g. early-stage handlers in tests).
func RequestID(c *gin.Context) string {
	v, _ := c.Get(KeyRequestID)
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}

// SetClaims stores the verified JWT claims on the context.
func SetClaims(c *gin.Context, claims *auth.AccessClaims) { c.Set(KeyClaims, claims) }

// Claims returns the verified JWT claims attached by RequireAuth, or nil
// when the middleware did not run or the token was invalid.
func Claims(c *gin.Context) *auth.AccessClaims {
	v, _ := c.Get(KeyClaims)
	if cl, ok := v.(*auth.AccessClaims); ok {
		return cl
	}
	return nil
}
