package api

import (
	"context"
	"encoding/json"
	"net/http"
	"regexp"
	"time"

	"github.com/gin-gonic/gin"

	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/agent"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/middleware"
)

// AdminServicesHandlerConfig holds dependencies for the service-control
// endpoints used by the M31 server-status page.
type AdminServicesHandlerConfig struct {
	Agent agent.AgentInterface
}

// RegisterAdminServicesRoutes mounts POST /admin/services/:name/{action}.
// RequireAdmin gates every route. Allowed actions are constrained to
// {restart, start, stop} — enable/disable are deferred to a follow-up
// because they survive reboots and need a heavier audit trail.
func RegisterAdminServicesRoutes(g *gin.RouterGroup, cfg AdminServicesHandlerConfig) {
	if cfg.Agent == nil {
		return
	}
	h := &adminServicesHandler{cfg: cfg}
	grp := g.Group("/admin/services")
	grp.Use(middleware.RequireAdmin())
	grp.POST("/:name/:action", h.action)
}

type adminServicesHandler struct{ cfg AdminServicesHandlerConfig }

var (
	servicesActionAllowlist = map[string]string{
		"restart": "service.restart",
		"start":   "service.start",
		"stop":    "service.stop",
	}
	// serviceNameRe is intentionally narrow — we re-validate panel-side
	// even though the agent does the same check. Only allowlisted units
	// (per ServiceListResponse) are valid; this regex eliminates
	// shell-injection-shaped strings before the request even hits the
	// agent.
	serviceNameRe = regexp.MustCompile(`^[a-zA-Z0-9._@-]+$`)
)

func (h *adminServicesHandler) action(c *gin.Context) {
	name := c.Param("name")
	action := c.Param("action")
	if !serviceNameRe.MatchString(name) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_service_name"})
		return
	}
	cmd, ok := servicesActionAllowlist[action]
	if !ok {
		c.JSON(http.StatusBadRequest, gin.H{"error": "unsupported_action"})
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 30*time.Second)
	defer cancel()
	raw, err := h.cfg.Agent.Call(ctx, cmd, map[string]any{"name": name})
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
