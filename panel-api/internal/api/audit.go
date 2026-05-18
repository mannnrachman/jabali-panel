package api

import (
	"log/slog"
	"net/http"

	"github.com/gin-gonic/gin"

	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/ginctx"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/middleware"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/repository"
)

// M49 unified audit log — read surface (ADR-0106). One store, two
// server-scoped views:
//
//   - GET /api/v1/admin/audit    (RequireAdmin)        — every row, raw
//   - GET /api/v1/me/activity    (RequireKratosSession) — caller's own
//     subject-scoped rows (their actions + actions taken ON their
//     account)
//
// The /me/activity scope is applied SERVER-SIDE via the repository's
// subject filter (never a client param) — the IDOR discipline that the
// live security testing validated on the domain/RequireOwner routes.
//
// Impersonation-visibility (ADR-0106 `audit_show_impersonation`,
// default-on) is moot in this codebase line: impersonation has no
// implementation (no emitter), so no `impersonation.*` rows exist to
// gate. Wire the server_settings toggle when/if impersonation lands.

const (
	defaultAuditPageSize = 50
	maxAuditPageSize     = 200
)

// AuditHandlerConfig — Repo is required (panic at boot if nil, per the
// route-family convention: programmer error, not a 500).
type AuditHandlerConfig struct {
	Repo repository.AuditEventRepository
	Log  *slog.Logger
}

type auditHandler struct{ cfg AuditHandlerConfig }

// RegisterAuditRoutes mounts the read surface. Call from app.go off the
// v1 group (which already carries RequireKratosSession); the admin
// view adds RequireAdmin on its own route.
func RegisterAuditRoutes(g *gin.RouterGroup, cfg AuditHandlerConfig) {
	if cfg.Repo == nil {
		panic("api.RegisterAuditRoutes: cfg.Repo is nil")
	}
	h := &auditHandler{cfg: cfg}
	g.GET("/admin/audit", middleware.RequireAdmin(), h.adminList)
	g.GET("/me/activity", h.meActivity)
}

// adminList — full forensics view (admin only).
func (h *auditHandler) adminList(c *gin.Context) {
	page, pageSize, opts := parseListOptions(c, defaultAuditPageSize, maxAuditPageSize)
	rows, total, err := h.cfg.Repo.ListAll(c.Request.Context(), opts)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal"})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"data":      rows,
		"total":     total,
		"page":      page,
		"page_size": pageSize,
	})
}

// meActivity — the caller's own activity feed. Subject scope is the
// session identity, enforced in the repo (ListBySubject); a blank
// subject would match nothing (safe-fail), never cross-tenant.
func (h *auditHandler) meActivity(c *gin.Context) {
	claims := ginctx.Claims(c)
	if claims == nil || claims.UserID == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}
	page, pageSize, opts := parseListOptions(c, defaultAuditPageSize, maxAuditPageSize)
	rows, total, err := h.cfg.Repo.ListBySubject(c.Request.Context(), claims.UserID, opts)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal"})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"data":      rows,
		"total":     total,
		"page":      page,
		"page_size": pageSize,
	})
}
