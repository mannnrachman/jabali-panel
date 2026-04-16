package api

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/ginctx"
)

// RegisterMeRoutes wires GET /api/v1/me, a thin handler that returns the
// current caller's identity from the JWT claims. Useful as an auth probe
// for the SPA ("am I still logged in?") and as an integration smoke test.
//
// The group passed here must already have RequireAuth applied.
func RegisterMeRoutes(g *gin.RouterGroup) {
	g.GET("/me", meHandler)
}

func meHandler(c *gin.Context) {
	claims := ginctx.Claims(c)
	if claims == nil {
		// Belt and braces — RequireAuth should have aborted already.
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthenticated"})
		return
	}
	resp := gin.H{
		"id":       claims.UserID,
		"email":    claims.Email,
		"is_admin": claims.IsAdmin,
	}
	if claims.ImpersonatedBy != "" {
		resp["impersonated_by"] = claims.ImpersonatedBy
	}
	c.JSON(http.StatusOK, resp)
}
