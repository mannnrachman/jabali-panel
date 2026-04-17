package api

import (
	"errors"
	"log/slog"
	"net/http"

	"github.com/gin-gonic/gin"

	ginctx "git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/ginctx"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/repository"
)

// DomainPHPPoolHandlerConfig wires the domain↔pool binding routes.
type DomainPHPPoolHandlerConfig struct {
	Domains  repository.DomainRepository
	PHPPools repository.PHPPoolRepository
}

// RegisterDomainPHPPoolRoutes adds two routes under the existing /domains
// group that bind or unbind a domain to a PHP-FPM pool. Lives in its own
// file so domains.go stays under the 800-line invariant.
//
// Routes:
//   - POST   /domains/:id/php-pool  { "pool_id": "<ulid>" }
//   - DELETE /domains/:id/php-pool
//
// Both paths require the caller to own the domain (or be admin). For
// POST, the pool must also belong to the same user that owns the domain —
// this prevents a user from pointing their domain at another user's pool
// and thereby reading another user's docroot via PHP.
func RegisterDomainPHPPoolRoutes(g *gin.RouterGroup, cfg DomainPHPPoolHandlerConfig) {
	h := &domainPHPPoolHandler{cfg: cfg}
	g.POST("/domains/:id/php-pool", h.bind)
	g.DELETE("/domains/:id/php-pool", h.unbind)
}

type domainPHPPoolHandler struct{ cfg DomainPHPPoolHandlerConfig }

type bindDomainPHPPoolRequest struct {
	PoolID string `json:"pool_id" binding:"required"`
}

func (h *domainPHPPoolHandler) bind(c *gin.Context) {
	claims := ginctx.Claims(c)
	if claims == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	var req bindDomainPHPPoolRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_body"})
		return
	}

	ctx := c.Request.Context()
	domainID := c.Param("id")

	dom, err := h.cfg.Domains.FindByID(ctx, domainID)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "domain_not_found"})
			return
		}
		slog.ErrorContext(ctx, "bind php-pool: load domain", "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal"})
		return
	}
	if !claims.IsAdmin && dom.UserID != claims.UserID {
		c.JSON(http.StatusForbidden, gin.H{"error": "forbidden"})
		return
	}

	pool, err := h.cfg.PHPPools.FindByID(ctx, req.PoolID)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "pool_not_found"})
			return
		}
		slog.ErrorContext(ctx, "bind php-pool: load pool", "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal"})
		return
	}

	// Cross-user binding is refused even for admins — an admin who wants
	// to run alice's domain in bob's pool almost certainly has a bug, not
	// an intent. If the use case ever comes up, it gets its own endpoint.
	if pool.UserID != dom.UserID {
		c.JSON(http.StatusForbidden, gin.H{"error": "pool_not_owned_by_domain_user"})
		return
	}

	dom.PHPPoolID = &req.PoolID
	if err := h.cfg.Domains.Update(ctx, dom); err != nil {
		slog.ErrorContext(ctx, "bind php-pool: update domain", "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"domain_id":   dom.ID,
		"php_pool_id": req.PoolID,
	})
}

func (h *domainPHPPoolHandler) unbind(c *gin.Context) {
	claims := ginctx.Claims(c)
	if claims == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	ctx := c.Request.Context()
	domainID := c.Param("id")

	dom, err := h.cfg.Domains.FindByID(ctx, domainID)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "domain_not_found"})
			return
		}
		slog.ErrorContext(ctx, "unbind php-pool: load domain", "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal"})
		return
	}
	if !claims.IsAdmin && dom.UserID != claims.UserID {
		c.JSON(http.StatusForbidden, gin.H{"error": "forbidden"})
		return
	}

	dom.PHPPoolID = nil
	if err := h.cfg.Domains.Update(ctx, dom); err != nil {
		slog.ErrorContext(ctx, "unbind php-pool: update domain", "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"domain_id":   dom.ID,
		"php_pool_id": nil,
	})
}
