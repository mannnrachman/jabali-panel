package api

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/agent"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/middleware"
)

// AdminUpdatesHandlerConfig holds dependencies for the system-updates endpoints.
type AdminUpdatesHandlerConfig struct {
	Agent agent.AgentInterface
}

// RegisterAdminUpdatesRoutes mounts the eight system-update endpoints. All
// state lives in systemd transient units on the host, so this layer is a
// thin proxy: agent commands do the work, panel-api just enforces auth +
// shape. M29.
func RegisterAdminUpdatesRoutes(g *gin.RouterGroup, cfg AdminUpdatesHandlerConfig) {
	if cfg.Agent == nil {
		return
	}
	h := &adminUpdatesHandler{cfg: cfg}
	grp := g.Group("/admin/updates")
	grp.Use(middleware.RequireAdmin())
	grp.GET("/jabali/check", h.jabaliCheck)
	grp.POST("/jabali/run", h.jabaliRun)
	grp.GET("/jabali/status", h.jabaliStatus)
	grp.DELETE("/jabali", h.jabaliStop)
	grp.GET("/apt/check", h.aptCheck)
	grp.POST("/apt/run", h.aptRun)
	grp.GET("/apt/status", h.aptStatus)
	grp.DELETE("/apt", h.aptStop)
}

type adminUpdatesHandler struct{ cfg AdminUpdatesHandlerConfig }

func (h *adminUpdatesHandler) callAgent(c *gin.Context, cmd string, params any, timeout time.Duration) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), timeout)
	defer cancel()
	raw, err := h.cfg.Agent.Call(ctx, cmd, params)
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": "agent_error", "details": err.Error()})
		return
	}
	var data any
	if err := json.Unmarshal(raw, &data); err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": "agent_parse"})
		return
	}
	c.JSON(http.StatusOK, data)
}

// `jabali update -f` itself is async (transient unit). The check call is
// synchronous (git fetch) — generous timeout because the upstream may be
// slow. 60 s covers any reasonable network blip.
func (h *adminUpdatesHandler) jabaliCheck(c *gin.Context) {
	h.callAgent(c, "system.update_check", nil, 60*time.Second)
}

func (h *adminUpdatesHandler) jabaliRun(c *gin.Context) {
	// systemd-run --no-block returns immediately; 10 s timeout is plenty.
	h.callAgent(c, "system.update_run", map[string]any{}, 10*time.Second)
}

func (h *adminUpdatesHandler) jabaliStatus(c *gin.Context) {
	h.callAgent(c, "system.update_status", map[string]any{"since": c.Query("since")}, 15*time.Second)
}

func (h *adminUpdatesHandler) jabaliStop(c *gin.Context) {
	h.callAgent(c, "system.unit_stop", map[string]any{"unit": "jabali-update-oneshot.service"}, 10*time.Second)
}

func (h *adminUpdatesHandler) aptCheck(c *gin.Context) {
	// `apt-get update` over slow mirrors can take 60+ seconds.
	h.callAgent(c, "system.apt_check", nil, 120*time.Second)
}

func (h *adminUpdatesHandler) aptRun(c *gin.Context) {
	h.callAgent(c, "system.apt_run", map[string]any{}, 10*time.Second)
}

func (h *adminUpdatesHandler) aptStatus(c *gin.Context) {
	h.callAgent(c, "system.apt_status", map[string]any{"since": c.Query("since")}, 15*time.Second)
}

func (h *adminUpdatesHandler) aptStop(c *gin.Context) {
	h.callAgent(c, "system.unit_stop", map[string]any{"unit": "jabali-apt-oneshot.service"}, 10*time.Second)
}
