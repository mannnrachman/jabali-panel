package middleware

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"

	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/auth"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/ginctx"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/kratosclient"
)

// RequireKratosSession is Gin middleware that validates a Kratos session cookie
// and attaches the authenticated AccessClaims to the request context.
// The middleware extracts the ory_kratos_session cookie, calls Kratos /sessions/whoami,
// and on success, builds AccessClaims from the identity traits.
//
// On auth failure, it returns 401 with error reason (e.g., "missing_session").
// The Authorization header is ignored in Kratos mode (feature flag isolates one auth source).
func RequireKratosSession(kratosClient *kratosclient.Client) gin.HandlerFunc {
	return func(c *gin.Context) {
		// Extract the session cookie.
		cookie, err := c.Cookie("ory_kratos_session")
		if err != nil {
			c.JSON(http.StatusUnauthorized, gin.H{
				"error": "missing_session",
				"message": "Kratos session cookie not found",
			})
			c.Abort()
			return
		}

		// Validate the session via Kratos whoami.
		identity, err := kratosClient.Whoami(c.Request.Context(), cookie)
		if err != nil {
			if errors.Is(err, kratosclient.ErrUnauthenticated) {
				c.JSON(http.StatusUnauthorized, gin.H{
					"error":   "invalid_session",
					"message": "Kratos session validation failed",
				})
			} else {
				c.JSON(http.StatusUnauthorized, gin.H{
					"error":   "session_error",
					"message": err.Error(),
				})
			}
			c.Abort()
			return
		}

		// Extract claims from the identity traits.
		claims := &auth.AccessClaims{
			UserID:   identity.ID,
			Email:    identity.GetTraitEmail(),
			IsAdmin:  identity.GetTraitIsAdmin(),
		}

		// Attach to context for handler access.
		ginctx.SetClaims(c, claims)

		c.Next()
	}
}
