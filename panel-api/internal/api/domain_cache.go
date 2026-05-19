package api

import (
	"context"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/agent"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/ginctx"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/models"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/repository"
)

// DomainCacheHandlerConfig holds deps for the per-domain nginx
// FastCGI micro-cache toggle (ADR-0108). Reconciler converges the
// vhost (the agent renders the cache directives from domain.create);
// the toggle itself is just DB-as-truth + a reconcile schedule, same
// shape as the SSL/Email per-domain switches.
type DomainCacheHandlerConfig struct {
	Agent      agent.AgentInterface
	Domains    repository.DomainRepository
	Reconciler DNSScheduler
}

// RegisterDomainCacheRoutes mounts the cache endpoints (ADR-0108).
func RegisterDomainCacheRoutes(g *gin.RouterGroup, cfg DomainCacheHandlerConfig) {
	if cfg.Domains == nil {
		return
	}
	h := &domainCacheHandler{cfg: cfg}
	g.GET("/domains/:id/cache", h.get)
	g.PUT("/domains/:id/cache", h.update)
	g.POST("/domains/:id/cache/purge", h.purge)
}

type domainCacheHandler struct{ cfg DomainCacheHandlerConfig }

type cacheResponse struct {
	DomainID   string `json:"domain_id"`
	DomainName string `json:"domain_name"`
	Enabled    bool   `json:"enabled"`
}

type cacheUpdateRequest struct {
	Enabled bool `json:"enabled"`
}

// loadAndAuth returns the domain after owner-or-admin authz; writes the
// HTTP response on failure. Mirrors domain_dnssec.go's loadAndAuth.
func (h *domainCacheHandler) loadAndAuth(c *gin.Context) *models.Domain {
	ctx := c.Request.Context()
	claims := ginctx.Claims(c)
	if claims == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return nil
	}
	dom, err := h.cfg.Domains.FindByID(ctx, c.Param("id"))
	if err != nil {
		if isNotFound(err) {
			c.JSON(http.StatusNotFound, gin.H{"error": "domain_not_found"})
			return nil
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal"})
		return nil
	}
	if !claims.IsAdmin && dom.UserID != claims.UserID {
		c.JSON(http.StatusForbidden, gin.H{"error": "forbidden"})
		return nil
	}
	return dom
}

func (h *domainCacheHandler) get(c *gin.Context) {
	dom := h.loadAndAuth(c)
	if dom == nil {
		return
	}
	c.JSON(http.StatusOK, cacheResponse{
		DomainID: dom.ID, DomainName: dom.Name, Enabled: dom.CacheEnabled,
	})
}

// update flips the cache switch. DB-as-truth: persist, then schedule a
// reconcile — the reconciler re-renders the vhost via domain.create
// with the new CacheEnabled and the agent reloads nginx. No synchronous
// agent call (unlike DNSSEC): the cache directives are vhost-rendered,
// not a separate agent op.
func (h *domainCacheHandler) update(c *gin.Context) {
	ctx := c.Request.Context()
	dom := h.loadAndAuth(c)
	if dom == nil {
		return
	}
	var req cacheUpdateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_request", "details": err.Error()})
		return
	}
	if err := h.cfg.Domains.UpdateCacheEnabled(ctx, dom.ID, req.Enabled); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "update_failed", "details": err.Error()})
		return
	}
	if h.cfg.Reconciler != nil {
		h.cfg.Reconciler.Schedule(dom.ID)
	}
	c.JSON(http.StatusOK, cacheResponse{
		DomainID: dom.ID, DomainName: dom.Name, Enabled: req.Enabled,
	})
}

// purge clears this domain's cached pages. v1: host-key grep-unlink in
// the shared keyzone (ADR-0108); no nginx reload needed.
func (h *domainCacheHandler) purge(c *gin.Context) {
	ctx := c.Request.Context()
	dom := h.loadAndAuth(c)
	if dom == nil {
		return
	}
	if h.cfg.Agent == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "agent_unavailable"})
		return
	}
	agentCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if _, err := h.cfg.Agent.Call(agentCtx, "nginx.cache.purge", map[string]any{"domain": dom.Name}); err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": "agent_error", "details": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true, "domain": dom.Name})
}
