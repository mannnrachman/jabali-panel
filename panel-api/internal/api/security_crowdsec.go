package api

import (
	"context"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"

	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/agent"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/middleware"
)

// M26 Step 4 (ADR-0053). CrowdSec admin endpoints.

const csCallTimeout = 10 * time.Second

// validCrowdSecScopes mirrors the agent-side allow-list. Reject unknown
// scope values at the API edge so the operator gets a clean 400 instead
// of a generic agent error. See feedback_verify_wire_contract — keep
// the wire-contract values explicit.
var validCrowdSecScopes = map[string]bool{
	"ip": true, "range": true, "country": true, "as": true,
}

// RegisterSecurityCrowdSecRoutes mounts admin-only CrowdSec endpoints.
func RegisterSecurityCrowdSecRoutes(rg *gin.RouterGroup, cli agent.AgentInterface) {
	g := rg.Group("/admin/security/crowdsec", middleware.RequireAdmin())

	g.GET("/status", agentPassthrough(cli, "security.crowdsec.status", nil, csCallTimeout))

	g.GET("/decisions", func(c *gin.Context) {
		params := map[string]any{}
		if scope := c.Query("scope"); scope != "" {
			if !validCrowdSecScopes[scope] {
				c.JSON(http.StatusBadRequest, gin.H{
					"status": "error",
					"error":  "invalid_scope",
					"detail": "scope must be one of ip|range|country|as",
				})
				return
			}
			params["scope"] = scope
		}
		if limit := c.Query("limit"); limit != "" {
			n, err := strconv.Atoi(limit)
			if err != nil || n < 1 || n > 1000 {
				c.JSON(http.StatusBadRequest, gin.H{
					"status": "error",
					"error":  "invalid_limit",
				})
				return
			}
			params["limit"] = n
		}
		ctx, cancel := context.WithTimeout(c.Request.Context(), csCallTimeout)
		defer cancel()
		raw, err := cli.Call(ctx, "security.crowdsec.decisions.list", params)
		if err != nil {
			status, body := translateAgentError(err)
			c.JSON(status, body)
			return
		}
		c.Data(http.StatusOK, "application/json; charset=utf-8", raw)
	})

	g.POST("/decisions", func(c *gin.Context) {
		var body struct {
			IP       string `json:"ip"`
			Duration string `json:"duration"`
			Reason   string `json:"reason"`
		}
		if err := c.ShouldBindJSON(&body); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"status": "error", "error": "invalid_json"})
			return
		}
		ctx, cancel := context.WithTimeout(c.Request.Context(), csCallTimeout)
		defer cancel()
		raw, err := cli.Call(ctx, "security.crowdsec.decisions.add", map[string]any{
			"ip": body.IP, "duration": body.Duration, "reason": body.Reason,
		})
		if err != nil {
			status, body := translateAgentError(err)
			c.JSON(status, body)
			return
		}
		c.Data(http.StatusCreated, "application/json; charset=utf-8", raw)
	})

	g.DELETE("/decisions/:id", func(c *gin.Context) {
		id, err := strconv.Atoi(c.Param("id"))
		if err != nil || id < 1 {
			c.JSON(http.StatusBadRequest, gin.H{"status": "error", "error": "invalid_id"})
			return
		}
		ctx, cancel := context.WithTimeout(c.Request.Context(), csCallTimeout)
		defer cancel()
		raw, err := cli.Call(ctx, "security.crowdsec.decisions.delete", map[string]any{"id": id})
		if err != nil {
			status, body := translateAgentError(err)
			c.JSON(status, body)
			return
		}
		c.Data(http.StatusOK, "application/json; charset=utf-8", raw)
	})

	g.GET("/bouncers", agentPassthrough(cli, "security.crowdsec.bouncers.list", nil, csCallTimeout))
	g.GET("/metrics", agentPassthrough(cli, "security.crowdsec.metrics", nil, csCallTimeout))

	g.GET("/hub", func(c *gin.Context) {
		params := map[string]any{}
		if t := c.Query("type"); t != "" {
			params["type"] = t
		}
		ctx, cancel := context.WithTimeout(c.Request.Context(), csCallTimeout)
		defer cancel()
		raw, err := cli.Call(ctx, "security.crowdsec.hub.list", params)
		if err != nil {
			status, body := translateAgentError(err)
			c.JSON(status, body)
			return
		}
		c.Data(http.StatusOK, "application/json; charset=utf-8", raw)
	})
}

// agentPassthrough is a shared helper for "GET that just forwards to a
// no-arg agent command and returns the raw JSON response."
func agentPassthrough(cli agent.AgentInterface, command string, params any, timeout time.Duration) gin.HandlerFunc {
	return func(c *gin.Context) {
		ctx, cancel := context.WithTimeout(c.Request.Context(), timeout)
		defer cancel()
		raw, err := cli.Call(ctx, command, params)
		if err != nil {
			status, body := translateAgentError(err)
			c.JSON(status, body)
			return
		}
		c.Data(http.StatusOK, "application/json; charset=utf-8", raw)
	}
}
