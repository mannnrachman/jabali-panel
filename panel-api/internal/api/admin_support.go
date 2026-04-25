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

// AdminSupportHandlerConfig holds dependencies for the support endpoints.
type AdminSupportHandlerConfig struct {
	Agent agent.AgentInterface
}

// RegisterAdminSupportRoutes mounts the diagnostic endpoints under
// /admin/support. Admin-only. ADR-0064.
//
// POST /diagnostic         — collect+redact+upload to enclosed; return URL+password
// POST /diagnostic/notify  — operator-confirmed: forward URL+password to team via ntfy
func RegisterAdminSupportRoutes(g *gin.RouterGroup, cfg AdminSupportHandlerConfig) {
	if cfg.Agent == nil {
		return
	}
	h := &adminSupportHandler{cfg: cfg}
	grp := g.Group("/admin/support")
	grp.Use(middleware.RequireAdmin())
	grp.POST("/diagnostic", h.diagnostic)
	grp.POST("/diagnostic/notify", h.diagnosticNotify)
}

type adminSupportHandler struct{ cfg AdminSupportHandlerConfig }

// diagnostic asks the agent to collect host state, redact, encrypt and
// upload to enclosed. Returns URL + password for the operator to copy
// — and to feed into /diagnostic/notify if they want the team paged.
// 120s timeout: journalctl for 10 services + a few-MB enclosed POST
// over a slow link can stretch.
func (h *adminSupportHandler) diagnostic(c *gin.Context) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), 120*time.Second)
	defer cancel()
	h.proxy(c, ctx, "system.diagnostic_report", nil)
}

// diagnosticNotify forwards a previously-minted URL+password pair to the
// team via ntfy. Two-step on purpose: the operator decides whether the
// case warrants a page, and the credentials never leave their browser
// without their explicit click.
func (h *adminSupportHandler) diagnosticNotify(c *gin.Context) {
	var body map[string]any
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_request", "details": err.Error()})
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 20*time.Second)
	defer cancel()
	h.proxy(c, ctx, "system.diagnostic_notify", body)
}

func (h *adminSupportHandler) proxy(c *gin.Context, ctx context.Context, cmd string, params any) {
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
