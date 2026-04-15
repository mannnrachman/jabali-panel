package middleware

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/ginctx"
)

// RequireAdmin returns a middleware that responds 403 unless the context
// carries verified admin claims. If no claims are present (i.e. RequireAuth
// did not run first), it returns 401 — surfacing the misconfiguration
// rather than accidentally allowing the request.
func RequireAdmin() gin.HandlerFunc {
	return func(c *gin.Context) {
		claims := ginctx.Claims(c)
		if claims == nil {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "unauthenticated"})
			return
		}
		if !claims.IsAdmin {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "forbidden"})
			return
		}
		c.Next()
	}
}

// RequireOwner returns a middleware that responds 403 unless the caller
// is either an admin or the owner of the resource identified by the named
// path parameter. Example:
//
//	router.GET("/users/:id", RequireAuth(iss), RequireOwner("id"), handler)
//
// Compares the parameter value against claims.UserID (i.e. the JWT "sub"
// claim). Admins always pass so operators can reach into any user's data.
func RequireOwner(paramName string) gin.HandlerFunc {
	return func(c *gin.Context) {
		claims := ginctx.Claims(c)
		if claims == nil {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "unauthenticated"})
			return
		}
		if claims.IsAdmin {
			c.Next()
			return
		}
		if c.Param(paramName) != claims.UserID {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "forbidden"})
			return
		}
		c.Next()
	}
}
