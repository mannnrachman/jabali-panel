package api

import (
	"context"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/agent"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/middleware"
)

// M40 (ADR-0086) AppArmor admin routes. Read-only status + per-profile
// mode flip (complain/enforce). Filesystem is the truth — DB doesn't
// mirror per-profile state.

const apparmorCallTimeout = 10 * time.Second

func RegisterSecurityAppArmorRoutes(rg *gin.RouterGroup, cli agent.AgentInterface) {
	g := rg.Group("/admin/security/apparmor", middleware.RequireAdmin())

	g.GET("/status", func(c *gin.Context) {
		ctx, cancel := context.WithTimeout(c.Request.Context(), apparmorCallTimeout)
		defer cancel()
		raw, err := cli.Call(ctx, "security.apparmor.status", map[string]any{})
		if err != nil {
			status, body := translateAgentError(err)
			c.JSON(status, body)
			return
		}
		c.Data(http.StatusOK, "application/json; charset=utf-8", raw)
	})

	g.POST("/profiles/:name/mode", func(c *gin.Context) {
		name := c.Param("name")
		var body struct {
			Mode string `json:"mode"`
		}
		if err := c.ShouldBindJSON(&body); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"status": "error", "error": "invalid_json"})
			return
		}
		if body.Mode != "complain" && body.Mode != "enforce" {
			c.JSON(http.StatusBadRequest, gin.H{"status": "error", "error": "mode must be complain|enforce"})
			return
		}
		ctx, cancel := context.WithTimeout(c.Request.Context(), apparmorCallTimeout)
		defer cancel()
		raw, err := cli.Call(ctx, "security.apparmor.set_mode", map[string]any{
			"profile": name,
			"mode":    body.Mode,
		})
		if err != nil {
			status, errBody := translateAgentError(err)
			c.JSON(status, errBody)
			return
		}
		c.Data(http.StatusOK, "application/json; charset=utf-8", raw)
	})
}
