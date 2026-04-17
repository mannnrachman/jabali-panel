package api

import (
	"context"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/agent"
)

// systemCallTimeout bounds agent calls for system endpoints. Generous
// because service.list probes multiple units sequentially.
const phpVersionCallTimeout = 10 * time.Second

// RegisterPHPVersionRoutes mounts GET /php/versions under the given router group.
// The group is expected to already carry RequireAuth middleware. This endpoint
// is available to all authenticated users (admin and non-admin) since they both
// need to select PHP versions for their domains.
func RegisterPHPVersionRoutes(rg *gin.RouterGroup, cli agent.AgentInterface) {
	php := rg.Group("/php")

	php.GET("/versions", func(c *gin.Context) {
		ctx, cancel := context.WithTimeout(c.Request.Context(), phpVersionCallTimeout)
		defer cancel()

		raw, err := cli.Call(ctx, "php.version.list", nil)
		if err != nil {
			status, body := translateAgentError(err)
			c.JSON(status, body)
			return
		}
		c.Data(http.StatusOK, "application/json; charset=utf-8", raw)
	})
}
