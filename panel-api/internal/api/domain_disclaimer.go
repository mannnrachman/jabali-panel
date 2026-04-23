// domain_disclaimer.go — M6.5 Step 6 per-domain outbound disclaimer.
//
// Wire contract: GET/PUT/DELETE /domains/:id/disclaimer
// Stalwart surface: x:SieveSystemScript named jabali-disclaimer-<domain>.
// ADR-0052: HTML-body coverage deferred pending live spike A/B.

package api

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/agent"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/ginctx"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/repository"
)

type DomainDisclaimerHandlerConfig struct {
	Domains repository.DomainRepository
	Agent   agent.AgentInterface
}

type disclaimerResponse struct {
	DomainID   string `json:"domain_id"`
	DomainName string `json:"domain_name"`
	Enabled    bool   `json:"enabled"`
	Text       string `json:"text"`
	UpdatedAt  string `json:"updated_at"`
}

type disclaimerUpdateRequest struct {
	Enabled bool   `json:"enabled"`
	Text    string `json:"text"`
}

type domainDisclaimerHandler struct {
	cfg DomainDisclaimerHandlerConfig
}

func RegisterDomainDisclaimerRoutes(g *gin.RouterGroup, cfg DomainDisclaimerHandlerConfig) {
	if cfg.Domains == nil {
		return
	}
	h := &domainDisclaimerHandler{cfg: cfg}
	g.GET("/domains/:id/disclaimer", h.get)
	g.PUT("/domains/:id/disclaimer", h.put)
	g.DELETE("/domains/:id/disclaimer", h.del)
}

func (h *domainDisclaimerHandler) loadDomain(c *gin.Context) (string, string, bool) {
	ctx := c.Request.Context()
	claims := ginctx.Claims(c)
	dom, err := h.cfg.Domains.FindByID(ctx, c.Param("id"))
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "not_found"})
		} else {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal"})
		}
		return "", "", false
	}
	if !claims.IsAdmin && dom.UserID != claims.UserID {
		c.JSON(http.StatusForbidden, gin.H{"error": "forbidden"})
		return "", "", false
	}
	if !dom.EmailEnabled {
		c.JSON(http.StatusForbidden, gin.H{"error": "email_not_enabled"})
		return "", "", false
	}
	text := ""
	if dom.DisclaimerText != nil {
		text = *dom.DisclaimerText
	}
	_ = text
	return dom.ID, dom.Name, true
}

func (h *domainDisclaimerHandler) get(c *gin.Context) {
	ctx := c.Request.Context()
	dom, err := h.cfg.Domains.FindByID(ctx, c.Param("id"))
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "not_found"})
		return
	}
	claims := ginctx.Claims(c)
	if !claims.IsAdmin && dom.UserID != claims.UserID {
		c.JSON(http.StatusForbidden, gin.H{"error": "forbidden"})
		return
	}
	text := ""
	if dom.DisclaimerText != nil {
		text = *dom.DisclaimerText
	}
	c.JSON(http.StatusOK, disclaimerResponse{
		DomainID:   dom.ID,
		DomainName: dom.Name,
		Enabled:    dom.DisclaimerEnabled,
		Text:       text,
		UpdatedAt:  dom.UpdatedAt.Format("2006-01-02T15:04:05Z07:00"),
	})
}

func (h *domainDisclaimerHandler) put(c *gin.Context) {
	ctx := c.Request.Context()
	id, domainName, ok := h.loadDomain(c)
	if !ok {
		return
	}
	var req disclaimerUpdateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_body"})
		return
	}
	if req.Enabled && strings.TrimSpace(req.Text) == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "text_required_when_enabled"})
		return
	}
	text := strings.TrimSpace(req.Text)
	if err := h.cfg.Domains.UpdateDisclaimer(ctx, id, req.Enabled, &text); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal"})
		return
	}
	if h.cfg.Agent != nil {
		agentCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		_, _ = h.cfg.Agent.Call(agentCtx, "domain.disclaimer_apply", map[string]any{
			"domain_name": domainName,
			"enabled":     req.Enabled,
			"text":        text,
		})
	}
	c.JSON(http.StatusOK, disclaimerResponse{
		DomainID:   id,
		DomainName: domainName,
		Enabled:    req.Enabled,
		Text:       text,
		UpdatedAt:  time.Now().UTC().Format("2006-01-02T15:04:05Z07:00"),
	})
}

func (h *domainDisclaimerHandler) del(c *gin.Context) {
	ctx := c.Request.Context()
	id, domainName, ok := h.loadDomain(c)
	if !ok {
		return
	}
	empty := ""
	if err := h.cfg.Domains.UpdateDisclaimer(ctx, id, false, &empty); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal"})
		return
	}
	if h.cfg.Agent != nil {
		agentCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		_, _ = h.cfg.Agent.Call(agentCtx, "domain.disclaimer_apply", map[string]any{
			"domain_name": domainName,
			"enabled":     false,
			"text":        "",
		})
	}
	c.JSON(http.StatusNoContent, nil)
}
