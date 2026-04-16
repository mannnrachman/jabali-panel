package api

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/agent"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/ginctx"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/ids"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/middleware"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/models"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/reconciler"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/repository"
)

type DomainHandlerConfig struct {
	Domains    repository.DomainRepository
	Users      repository.UserRepository
	Packages   repository.PackageRepository
	Agent      agent.AgentInterface
	Reconciler *reconciler.Reconciler
}

const (
	defaultDomainsPageSize = 20
	maxDomainsPageSize     = 200
)

func RegisterDomainRoutes(g *gin.RouterGroup, cfg DomainHandlerConfig) {
	h := &domainHandler{cfg: cfg}
	domains := g.Group("/domains")
	domains.GET("", h.list)
	domains.POST("", h.create)
	domains.GET("/:id", h.get)
	domains.PATCH("/:id", h.update)
	domains.DELETE("/:id", middleware.RequireAdmin(), h.delete)
}

type domainHandler struct{ cfg DomainHandlerConfig }

type createDomainRequest struct {
	Name    string `json:"name" binding:"required"`
	UserID  string `json:"user_id"`
	DocRoot string `json:"doc_root"`
}

type updateDomainRequest struct {
	IsEnabled             *bool   `json:"is_enabled,omitempty"`
	NginxCustomDirectives *string `json:"nginx_custom_directives,omitempty"`
}

func (h *domainHandler) list(c *gin.Context) {
	claims := ginctx.Claims(c)
	if claims == nil {
		c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "unauthenticated"})
		return
	}

	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", strconv.Itoa(defaultDomainsPageSize)))
	if page < 1 {
		page = 1
	}
	if pageSize < 1 || pageSize > maxDomainsPageSize {
		pageSize = defaultDomainsPageSize
	}
	offset := (page - 1) * pageSize

	var domains []models.Domain
	var total int64
	var err error

	if claims.IsAdmin {
		domains, total, err = h.cfg.Domains.List(c.Request.Context(), offset, pageSize)
	} else {
		domains, total, err = h.cfg.Domains.ListByUserID(c.Request.Context(), claims.UserID, offset, pageSize)
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal"})
		return
	}
	if domains == nil {
		domains = []models.Domain{}
	}
	c.JSON(http.StatusOK, gin.H{
		"data":      domains,
		"total":     total,
		"page":      page,
		"page_size": pageSize,
	})
}

func (h *domainHandler) get(c *gin.Context) {
	claims := ginctx.Claims(c)
	if claims == nil {
		c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "unauthenticated"})
		return
	}
	domain, err := h.cfg.Domains.FindByID(c.Request.Context(), c.Param("id"))
	if err != nil {
		if isNotFound(err) {
			c.JSON(http.StatusNotFound, gin.H{"error": "not_found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal"})
		return
	}
	if !claims.IsAdmin && domain.UserID != claims.UserID {
		c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "forbidden"})
		return
	}
	c.JSON(http.StatusOK, domain)
}

func (h *domainHandler) create(c *gin.Context) {
	claims := ginctx.Claims(c)
	if claims == nil {
		c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "unauthenticated"})
		return
	}

	var req createDomainRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusUnprocessableEntity, gin.H{"error": "validation_failed", "detail": err.Error()})
		return
	}

	targetUserID := req.UserID
	if !claims.IsAdmin {
		targetUserID = claims.UserID
	}
	if targetUserID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "user_id is required"})
		return
	}

	ctx := c.Request.Context()

	user, err := h.cfg.Users.FindByID(ctx, targetUserID)
	if err != nil {
		if isNotFound(err) {
			c.JSON(http.StatusBadRequest, gin.H{"error": "user not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal"})
		return
	}

	// Admins are panel-only — they have no /home/<name>, so domains
	// can't be hosted under them. Bad request, not authz failure.
	if user.IsAdmin {
		c.JSON(http.StatusBadRequest, gin.H{
			"error":  "admin_cannot_host",
			"detail": "admin users are panel-only — create a regular user to host domains",
		})
		return
	}

	// Quota check.
	if user.PackageID != nil && *user.PackageID != "" {
		count, err := h.cfg.Domains.CountByUserID(ctx, targetUserID)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal"})
			return
		}
		pkg, err := h.cfg.Packages.FindByID(ctx, *user.PackageID)
		if err == nil && pkg.MaxDomains > 0 && count >= int64(pkg.MaxDomains) {
			c.JSON(http.StatusConflict, gin.H{"error": "domain_quota_exceeded"})
			return
		}
	}

	docRoot := req.DocRoot
	if docRoot == "" {
		docRoot = "/home/" + domainLinuxUser(user.Email) + "/public_html/" + req.Name
	}

	now := time.Now().UTC()
	domain := &models.Domain{
		ID:        ids.NewULID(),
		UserID:    targetUserID,
		Name:      req.Name,
		DocRoot:   docRoot,
		IsEnabled: true,
		CreatedAt: now,
		UpdatedAt: now,
	}

	if err := h.cfg.Domains.Create(ctx, domain); err != nil {
		if isConflict(err) {
			c.JSON(http.StatusConflict, gin.H{"error": "domain_already_exists"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal"})
		return
	}

	// Schedule reconciliation. The reconciler will converge the domain's
	// OS-level state (nginx vhost, PHP pool, etc.) with the DB state.
	// This is non-blocking and out-of-band.
	if h.cfg.Reconciler != nil {
		h.cfg.Reconciler.Schedule(domain.ID)
	}

	c.JSON(http.StatusCreated, domain)
}

func (h *domainHandler) update(c *gin.Context) {
	claims := ginctx.Claims(c)
	if claims == nil {
		c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "unauthenticated"})
		return
	}

	domain, err := h.cfg.Domains.FindByID(c.Request.Context(), c.Param("id"))
	if err != nil {
		if isNotFound(err) {
			c.JSON(http.StatusNotFound, gin.H{"error": "not_found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal"})
		return
	}
	if !claims.IsAdmin && domain.UserID != claims.UserID {
		c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "forbidden"})
		return
	}

	var req updateDomainRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusUnprocessableEntity, gin.H{"error": "validation_failed", "detail": err.Error()})
		return
	}

	ctx := c.Request.Context()

	if req.IsEnabled != nil && *req.IsEnabled != domain.IsEnabled {
		domain.IsEnabled = *req.IsEnabled
	}

	if req.NginxCustomDirectives != nil {
		if msg := validateNginxDirectives(*req.NginxCustomDirectives); msg != "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": msg})
			return
		}
		domain.NginxCustomDirectives = req.NginxCustomDirectives
	}

	domain.UpdatedAt = time.Now().UTC()
	if err := h.cfg.Domains.Update(ctx, domain); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal"})
		return
	}

	// Schedule reconciliation to sync the domain state with the agent.
	if h.cfg.Reconciler != nil {
		h.cfg.Reconciler.Schedule(domain.ID)
	}

	c.JSON(http.StatusOK, domain)
}

func (h *domainHandler) delete(c *gin.Context) {
	ctx := c.Request.Context()
	domain, err := h.cfg.Domains.FindByID(ctx, c.Param("id"))
	if err != nil {
		if isNotFound(err) {
			c.JSON(http.StatusNotFound, gin.H{"error": "not_found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal"})
		return
	}

	// Capture name BEFORE deleting — once the DB row is gone, the
	// reconciler can't look it up by ID. We pass the name to
	// ReconcileDeleted which targets the agent-side teardown directly.
	name := domain.Name
	if err := h.cfg.Domains.Delete(ctx, domain.ID); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal"})
		return
	}

	// Tear down OS-level resources out-of-band. Best-effort: the user
	// sees the row gone immediately; if agent teardown fails, the next
	// ReconcileAll tick logs the orphan for ops to investigate.
	if h.cfg.Reconciler != nil {
		go h.cfg.Reconciler.ReconcileDeleted(context.Background(), name)
	}

	c.Status(http.StatusNoContent)
}

func validateNginxDirectives(directives string) string {
	forbidden := []string{"proxy_pass", "lua_", "load_module", "ssl_certificate"}
	lower := strings.ToLower(directives)
	for _, word := range forbidden {
		if strings.Contains(lower, word) {
			return "forbidden directive: " + word
		}
	}
	return ""
}

func domainLinuxUser(email string) string {
	if i := strings.IndexByte(email, '@'); i > 0 {
		return email[:i]
	}
	return "user"
}
