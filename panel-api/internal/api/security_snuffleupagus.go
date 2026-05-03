// Package api — security_snuffleupagus.go (M41, ADR-0088)
//
// REST surface for the operator's Security → Snuffleupagus card.
// Wave A registers the routes returning stub data. Waves B-D fill in
// the real implementations.
package api

import (
	"context"
	"net/http"
	"time"

	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/agent"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/middleware"
	"github.com/gin-gonic/gin"
)

const snuffleupagusCallTimeout = 10 * time.Second

// RegisterSecuritySnuffleupagusRoutes wires the M41 admin endpoints.
// Mounted under the same /admin/security/* group as the other Security
// cards (CrowdSec, Malware, AppArmor [parked], AIDE).
func RegisterSecuritySnuffleupagusRoutes(rg *gin.RouterGroup, cli agent.AgentInterface) {
	g := rg.Group("/admin/security/snuffleupagus", middleware.RequireAdmin())

	g.GET("/status", func(c *gin.Context) {
		ctx, cancel := context.WithTimeout(c.Request.Context(), snuffleupagusCallTimeout)
		defer cancel()
		raw, err := cli.Call(ctx, "snuffleupagus.status", map[string]any{})
		if err != nil {
			status, body := translateAgentError(err)
			c.JSON(status, body)
			return
		}
		c.Data(http.StatusOK, "application/json; charset=utf-8", raw)
	})

	g.POST("/mode", func(c *gin.Context) {
		// Wave B fills this in.
		c.JSON(http.StatusNotImplemented, gin.H{"error": "not_implemented", "wave": "B"})
	})

	g.GET("/rules", func(c *gin.Context) {
		// Wave B fills this in.
		c.JSON(http.StatusNotImplemented, gin.H{"error": "not_implemented", "wave": "B"})
	})

	g.POST("/rules/:name/toggle", func(c *gin.Context) {
		// Wave B fills this in.
		c.JSON(http.StatusNotImplemented, gin.H{"error": "not_implemented", "wave": "B"})
	})

	g.GET("/incidents", func(c *gin.Context) {
		// Wave D fills this in.
		c.JSON(http.StatusNotImplemented, gin.H{"error": "not_implemented", "wave": "D"})
	})
}
