package api

import (
	"context"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/agent"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/middleware"
)

// M42 (ADR-0087) AIDE FIM admin routes. Read-only status + manual
// check trigger. Filesystem (/var/lib/aide/aide.db) is the truth;
// DB doesn't mirror state.

const aideCallTimeout = 10 * time.Second
const aideCheckTimeout = 11 * time.Minute

func RegisterSecurityAideRoutes(rg *gin.RouterGroup, cli agent.AgentInterface) {
	g := rg.Group("/admin/security/aide", middleware.RequireAdmin())

	g.GET("/status", func(c *gin.Context) {
		ctx, cancel := context.WithTimeout(c.Request.Context(), aideCallTimeout)
		defer cancel()
		raw, err := cli.Call(ctx, "security.aide.status", map[string]any{})
		if err != nil {
			status, body := translateAgentError(err)
			c.JSON(status, body)
			return
		}
		c.Data(http.StatusOK, "application/json; charset=utf-8", raw)
	})

	g.POST("/check", func(c *gin.Context) {
		ctx, cancel := context.WithTimeout(c.Request.Context(), aideCheckTimeout)
		defer cancel()
		raw, err := cli.Call(ctx, "security.aide.check", map[string]any{})
		if err != nil {
			status, body := translateAgentError(err)
			c.JSON(status, body)
			return
		}
		c.Data(http.StatusOK, "application/json; charset=utf-8", raw)
	})
}
