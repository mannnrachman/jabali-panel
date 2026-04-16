package api

import (
	"context"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/agent"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/middleware"
)

// systemCallTimeout bounds agent calls for system endpoints. Generous
// because service.list probes multiple units sequentially.
const systemCallTimeout = 10 * time.Second

// RegisterSystemRoutes mounts admin-only system endpoints under the
// given router group. The group is expected to already carry RequireAuth
// middleware; we add RequireAdmin ourselves.
func RegisterSystemRoutes(rg *gin.RouterGroup, cli agent.AgentInterface) {
	sys := rg.Group("/system", middleware.RequireAdmin())

	sys.GET("/info", func(c *gin.Context) {
		ctx, cancel := context.WithTimeout(c.Request.Context(), systemCallTimeout)
		defer cancel()

		raw, err := cli.Call(ctx, "system.info", nil)
		if err != nil {
			status, body := translateAgentError(err)
			c.JSON(status, body)
			return
		}
		c.Data(http.StatusOK, "application/json; charset=utf-8", raw)
	})

	sys.GET("/services", func(c *gin.Context) {
		ctx, cancel := context.WithTimeout(c.Request.Context(), systemCallTimeout)
		defer cancel()

		raw, err := cli.Call(ctx, "service.list", nil)
		if err != nil {
			status, body := translateAgentError(err)
			c.JSON(status, body)
			return
		}
		c.Data(http.StatusOK, "application/json; charset=utf-8", raw)
	})
}
