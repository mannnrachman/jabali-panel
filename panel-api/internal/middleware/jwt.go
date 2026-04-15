package middleware

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/auth"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/ginctx"
)

// RequireAuth returns a middleware that parses the Bearer token in the
// Authorization header, verifies it against the issuer, and attaches the
// resulting AccessClaims to the Gin context. Any failure responds with a
// generic 401 JSON so callers cannot probe internal error types.
func RequireAuth(iss *auth.JWTIssuer) gin.HandlerFunc {
	return func(c *gin.Context) {
		raw := c.GetHeader("Authorization")
		token, ok := extractBearer(raw)
		if !ok {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
				"error": "missing_authorization",
			})
			return
		}
		claims, err := iss.Verify(token)
		if err != nil {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
				"error": "invalid_token",
			})
			return
		}
		ginctx.SetClaims(c, claims)
		c.Next()
	}
}

// extractBearer returns the opaque token and true on a well-formed
// "Authorization: Bearer <token>" header, or false otherwise.
func extractBearer(raw string) (string, bool) {
	const prefix = "Bearer "
	if len(raw) <= len(prefix) {
		return "", false
	}
	if !strings.EqualFold(raw[:len(prefix)], prefix) {
		return "", false
	}
	tok := strings.TrimSpace(raw[len(prefix):])
	if tok == "" {
		return "", false
	}
	return tok, true
}
