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
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/reconciler"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/repository"
)

// M26 Step 4 (ADR-0055). ModSecurity admin endpoints. Mix of agent
// passthroughs (engine config + audit log) and DB-backed routes
// (per-domain toggle list + PATCH).

const modsecCallTimeout = 10 * time.Second

// SecurityModsecHandlerConfig wires repos + agent + reconciler. The
// reconciler handle is required so a PATCH on /admin/security/modsec/
// domains/:id triggers a single-domain reconcile that re-renders the
// vhost with/without the modsecurity directives (Step 5).
type SecurityModsecHandlerConfig struct {
	Agent      agent.AgentInterface
	Domains    repository.DomainRepository
	Reconciler *reconciler.Reconciler
}

// RegisterSecurityModsecRoutes mounts admin-only ModSec endpoints.
func RegisterSecurityModsecRoutes(rg *gin.RouterGroup, cfg SecurityModsecHandlerConfig) {
	g := rg.Group("/admin/security/modsec", middleware.RequireAdmin())

	// GET /admin/security/modsec/status — passthrough to agent
	// security.modsec.global.get; the UI also renders the audit-tail
	// summary alongside, so the response shape stays {engine_mode,paranoia}.
	g.GET("/status", agentPassthrough(cfg.Agent, "security.modsec.global.get", nil, modsecCallTimeout))

	// PUT /admin/security/modsec/global {engine_mode, paranoia}
	g.PUT("/global", func(c *gin.Context) {
		var body struct {
			EngineMode string `json:"engine_mode"`
			Paranoia   int    `json:"paranoia"`
		}
		if err := c.ShouldBindJSON(&body); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"status": "error", "error": "invalid_json"})
			return
		}
		ctx, cancel := context.WithTimeout(c.Request.Context(), modsecCallTimeout)
		defer cancel()
		raw, err := cfg.Agent.Call(ctx, "security.modsec.global.set", map[string]any{
			"engine_mode": body.EngineMode, "paranoia": body.Paranoia,
		})
		if err != nil {
			status, body := translateAgentError(err)
			c.JSON(status, body)
			return
		}
		c.Data(http.StatusOK, "application/json; charset=utf-8", raw)
	})

	// GET /admin/security/modsec/domains — paginated list of domains
	// surfacing only id, name, modsec_enabled. Envelope matches
	// /admin/ips per feedback_verify_wire_contract:
	// {data:[...], total, page, page_size}.
	g.GET("/domains", func(c *gin.Context) {
		page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
		if page < 1 {
			page = 1
		}
		pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "50"))
		if pageSize < 1 || pageSize > 200 {
			pageSize = 50
		}
		opts := repository.ListOptions{
			Limit:  pageSize,
			Offset: (page - 1) * pageSize,
		}
		ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
		defer cancel()
		domains, total, err := cfg.Domains.List(ctx, opts)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"status": "error", "error": "list_failed", "detail": err.Error()})
			return
		}
		type modsecDomainRow struct {
			ID            string `json:"id"`
			Name          string `json:"name"`
			ModSecEnabled bool   `json:"modsec_enabled"`
		}
		out := make([]modsecDomainRow, 0, len(domains))
		for _, d := range domains {
			out = append(out, modsecDomainRow{ID: d.ID, Name: d.Name, ModSecEnabled: d.ModSecEnabled})
		}
		c.JSON(http.StatusOK, gin.H{
			"data":      out,
			"total":     total,
			"page":      page,
			"page_size": pageSize,
		})
	})

	// PATCH /admin/security/modsec/domains/:id {modsec_enabled}
	g.PATCH("/domains/:id", func(c *gin.Context) {
		id := c.Param("id")
		if id == "" {
			c.JSON(http.StatusBadRequest, gin.H{"status": "error", "error": "missing_id"})
			return
		}
		var body struct {
			ModSecEnabled *bool `json:"modsec_enabled"`
		}
		if err := json.NewDecoder(c.Request.Body).Decode(&body); err != nil || body.ModSecEnabled == nil {
			c.JSON(http.StatusBadRequest, gin.H{"status": "error", "error": "missing_modsec_enabled"})
			return
		}
		ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
		defer cancel()
		if err := cfg.Domains.SetModSecEnabled(ctx, id, *body.ModSecEnabled); err != nil {
			if err == repository.ErrNotFound {
				c.JSON(http.StatusNotFound, gin.H{"status": "error", "error": "not_found"})
				return
			}
			c.JSON(http.StatusInternalServerError, gin.H{"status": "error", "error": "update_failed", "detail": err.Error()})
			return
		}
		// Trigger a reconcile so the vhost is regenerated with/without
		// the modsecurity directives (Step 5 wiring). Non-blocking.
		if cfg.Reconciler != nil {
			cfg.Reconciler.Schedule(id)
		}
		c.JSON(http.StatusOK, gin.H{"id": id, "modsec_enabled": *body.ModSecEnabled})
	})

	// GET /admin/security/modsec/audit?lines=
	g.GET("/audit", func(c *gin.Context) {
		params := map[string]any{}
		if l := c.Query("lines"); l != "" {
			n, err := strconv.Atoi(l)
			if err != nil || n < 1 || n > 1000 {
				c.JSON(http.StatusBadRequest, gin.H{"status": "error", "error": "invalid_lines"})
				return
			}
			params["lines"] = n
		}
		ctx, cancel := context.WithTimeout(c.Request.Context(), modsecCallTimeout)
		defer cancel()
		raw, err := cfg.Agent.Call(ctx, "security.modsec.audit.tail", params)
		if err != nil {
			status, body := translateAgentError(err)
			c.JSON(status, body)
			return
		}
		c.Data(http.StatusOK, "application/json; charset=utf-8", raw)
	})
}
