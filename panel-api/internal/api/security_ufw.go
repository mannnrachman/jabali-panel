package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"

	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/agent"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/middleware"
)

// M26 Step 4 (ADR-0054). UFW admin endpoints. Each handler shells the
// agent's security.ufw.* commands; agent does the authoritative
// validation. This layer adds RequireAdmin + thin shape checks.

const ufwCallTimeout = 10 * time.Second

// RegisterSecurityUFWRoutes mounts admin-only UFW endpoints under the
// given router group. The group is expected to already carry
// RequireAuth middleware; we add RequireAdmin ourselves.
func RegisterSecurityUFWRoutes(rg *gin.RouterGroup, cli agent.AgentInterface) {
	g := rg.Group("/admin/security/ufw", middleware.RequireAdmin())

	g.GET("/status", func(c *gin.Context) {
		ctx, cancel := context.WithTimeout(c.Request.Context(), ufwCallTimeout)
		defer cancel()
		raw, err := cli.Call(ctx, "security.ufw.status", nil)
		if err != nil {
			status, body := translateAgentError(err)
			c.JSON(status, body)
			return
		}
		c.Data(http.StatusOK, "application/json; charset=utf-8", raw)
	})

	g.POST("/rules", func(c *gin.Context) {
		var body struct {
			Action string `json:"action"`
			Port   string `json:"port"`
			Proto  string `json:"proto,omitempty"`
			From   string `json:"from,omitempty"`
		}
		if err := c.ShouldBindJSON(&body); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"status": "error", "error": "invalid_json", "detail": err.Error()})
			return
		}
		ctx, cancel := context.WithTimeout(c.Request.Context(), ufwCallTimeout)
		defer cancel()
		params := map[string]any{"action": body.Action, "port": body.Port}
		if body.Proto != "" {
			params["proto"] = body.Proto
		}
		if body.From != "" {
			params["from"] = body.From
		}
		raw, err := cli.Call(ctx, "security.ufw.rule.add", params)
		if err != nil {
			status, body := translateAgentError(err)
			c.JSON(status, body)
			return
		}
		c.Data(http.StatusCreated, "application/json; charset=utf-8", raw)
	})

	g.DELETE("/rules/:num", func(c *gin.Context) {
		num, err := strconv.Atoi(c.Param("num"))
		if err != nil || num < 1 {
			c.JSON(http.StatusBadRequest, gin.H{"status": "error", "error": "invalid_num"})
			return
		}
		ctx, cancel := context.WithTimeout(c.Request.Context(), ufwCallTimeout)
		defer cancel()
		raw, err := cli.Call(ctx, "security.ufw.rule.delete", map[string]any{"num": num})
		if err != nil {
			status, body := translateAgentError(err)
			c.JSON(status, body)
			return
		}
		c.Data(http.StatusOK, "application/json; charset=utf-8", raw)
	})

	g.PUT("/default", func(c *gin.Context) {
		var body struct {
			Chain  string `json:"chain"`
			Policy string `json:"policy"`
		}
		if err := c.ShouldBindJSON(&body); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"status": "error", "error": "invalid_json"})
			return
		}
		ctx, cancel := context.WithTimeout(c.Request.Context(), ufwCallTimeout)
		defer cancel()
		raw, err := cli.Call(ctx, "security.ufw.default.set", map[string]any{"chain": body.Chain, "policy": body.Policy})
		if err != nil {
			status, body := translateAgentError(err)
			c.JSON(status, body)
			return
		}
		c.Data(http.StatusOK, "application/json; charset=utf-8", raw)
	})

	// enable / disable: destructive — require {"confirm":"YES"}.
	g.POST("/enable", ufwToggleHandler(cli, "security.ufw.enable"))
	g.POST("/disable", ufwToggleHandler(cli, "security.ufw.disable"))
}

func ufwToggleHandler(cli agent.AgentInterface, command string) gin.HandlerFunc {
	return func(c *gin.Context) {
		var body struct {
			Confirm string `json:"confirm"`
		}
		// Body is optional — empty body means no confirm, which we reject.
		_ = json.NewDecoder(c.Request.Body).Decode(&body)
		if body.Confirm != "YES" {
			c.JSON(http.StatusBadRequest, gin.H{
				"status": "error",
				"error":  "confirmation_required",
				"detail": `body must be {"confirm":"YES"} to apply this destructive action`,
			})
			return
		}
		ctx, cancel := context.WithTimeout(c.Request.Context(), ufwCallTimeout)
		defer cancel()
		raw, err := cli.Call(ctx, command, map[string]any{"confirm": body.Confirm})
		if err != nil {
			status, body := translateAgentError(err)
			c.JSON(status, body)
			return
		}
		c.Data(http.StatusOK, "application/json; charset=utf-8", raw)
	}
}
