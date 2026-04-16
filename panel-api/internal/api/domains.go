package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/agent"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/ginctx"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/ids"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/middleware"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/models"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/repository"
)

// DomainHandlerConfig holds dependencies for domain handlers.
type DomainHandlerConfig struct {
	Domains  repository.DomainRepository
	Users    repository.UserRepository
	Packages repository.PackageRepository
	Agent    agent.AgentInterface
}

type domainHandler struct {
	cfg DomainHandlerConfig
}

// RegisterDomainRoutes mounts domain routes.
func RegisterDomainRoutes(g *gin.RouterGroup, cfg DomainHandlerConfig) {
	h := &domainHandler{cfg: cfg}

	domains := g.Group("/domains")
	domains.GET("", h.list)
	domains.POST("", h.create)
	domains.GET("/:id", h.get)
	domains.PATCH("/:id", h.update)
	domains.DELETE("/:id", middleware.RequireAdmin(), h.delete)
}

// listRequest holds optional query parameters for listing.
type listRequest struct {
	Offset int `form:"offset,default=0"`
	Limit  int `form:"limit,default=10"`
}

// getDomainResponse wraps a domain with metadata.
type getDomainResponse struct {
	Data *models.Domain `json:"data"`
}

// listDomainsResponse wraps a list of domains with pagination metadata.
type listDomainsResponse struct {
	Data  []models.Domain `json:"data"`
	Total int64           `json:"total"`
}

// createDomainRequest is the request body for creating a domain.
type createDomainRequest struct {
	Name   string `json:"name" binding:"required"`
	UserID string `json:"user_id"`
	DocRoot string `json:"doc_root"`
}

// updateDomainRequest is the request body for updating a domain.
type updateDomainRequest struct {
	IsEnabled             *bool   `json:"is_enabled,omitempty"`
	NginxCustomDirectives *string `json:"nginx_custom_directives,omitempty"`
}

// list returns domains the user can access.
// - Admins see all domains
// - Users see only their own domains
func (h *domainHandler) list(c *gin.Context) {
	claims := ginctx.Claims(c)
	if claims == nil {
		c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "unauthenticated"})
		return
	}

	var req listRequest
	if err := c.BindQuery(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Second)
	defer cancel()

	var domains []models.Domain
	var total int64
	var err error

	if claims.IsAdmin {
		domains, total, err = h.cfg.Domains.List(ctx, int(req.Offset), int(req.Limit))
	} else {
		domains, total, err = h.cfg.Domains.ListByUserID(ctx, claims.UserID, int(req.Offset), int(req.Limit))
	}

	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to list domains"})
		return
	}

	if domains == nil {
		domains = []models.Domain{}
	}

	c.JSON(http.StatusOK, listDomainsResponse{
		Data:  domains,
		Total: total,
	})
}

// get returns a single domain if the requester is admin or the owner.
func (h *domainHandler) get(c *gin.Context) {
	claims := ginctx.Claims(c)
	if claims == nil {
		c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "unauthenticated"})
		return
	}

	id := c.Param("id")
	if id == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "domain id required"})
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Second)
	defer cancel()

	domain, err := h.cfg.Domains.FindByID(ctx, id)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to fetch domain"})
		return
	}
	if domain == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "domain not found"})
		return
	}

	// Check access: admin or owner
	if !claims.IsAdmin && domain.UserID != claims.UserID {
		c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "forbidden"})
		return
	}

	c.JSON(http.StatusOK, getDomainResponse{Data: domain})
}

// create creates a new domain.
// Admins can specify user_id; non-admins are limited to their own user_id.
// Quota is checked: non-unlimited packages have a MaxDomains limit.
func (h *domainHandler) create(c *gin.Context) {
	claims := ginctx.Claims(c)
	if claims == nil {
		c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "unauthenticated"})
		return
	}

	var req createDomainRequest
	if err := c.BindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if req.Name == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "name is required"})
		return
	}

	// Determine target user_id
	targetUserID := req.UserID
	if !claims.IsAdmin {
		targetUserID = claims.UserID
	}
	if targetUserID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "user_id is required"})
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Second)
	defer cancel()

	// Check quota
	count, err := h.cfg.Domains.CountByUserID(ctx, targetUserID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to check quota"})
		return
	}

	// Look up user's package to get max domains
	user, err := h.cfg.Users.FindByID(ctx, targetUserID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to fetch user"})
		return
	}
	if user == nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "user not found"})
		return
	}

	if user.PackageID != nil && *user.PackageID != "" {
		pkg, err := h.cfg.Packages.FindByID(ctx, *user.PackageID)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to fetch package"})
			return
		}
		if pkg != nil && pkg.MaxDomains > 0 && count >= int64(pkg.MaxDomains) {
			c.JSON(http.StatusConflict, gin.H{"error": "domain quota exceeded"})
			return
		}
	}

	// Generate doc_root if not provided
	docRoot := req.DocRoot
	if docRoot == "" {
		docRoot = "/home/" + deriveLinuxUsername(user.Email) + "/public_html/" + req.Name
	}

	// Create domain in DB
	domain := &models.Domain{
		ID:        ids.NewULID(),
		UserID:    targetUserID,
		Name:      req.Name,
		DocRoot:   docRoot,
		IsEnabled: true,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}

	if err := h.cfg.Domains.Create(ctx, domain); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create domain"})
		return
	}

	// Call agent to create the domain
	agentParams := map[string]interface{}{
		"username":     deriveLinuxUsername(user.Email),
		"domain":       req.Name,
		"doc_root":     docRoot,
		"php_version":  "8.3",
	}
	paramsJSON, _ := json.Marshal(agentParams)

	_, err = h.cfg.Agent.Call(ctx, "domain.create", json.RawMessage(paramsJSON))
	if err != nil {
		// Rollback the DB creation on agent error
		_ = h.cfg.Domains.Delete(ctx, domain.ID)
		status, body := translateAgentError(err)
		c.JSON(status, body)
		return
	}

	c.JSON(http.StatusCreated, getDomainResponse{Data: domain})
}

// update updates a domain (admin or owner only).
// - is_enabled: calls agent domain.enable or domain.disable
// - nginx_custom_directives: validated before save
func (h *domainHandler) update(c *gin.Context) {
	claims := ginctx.Claims(c)
	if claims == nil {
		c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "unauthenticated"})
		return
	}

	id := c.Param("id")
	if id == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "domain id required"})
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Second)
	defer cancel()

	domain, err := h.cfg.Domains.FindByID(ctx, id)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to fetch domain"})
		return
	}
	if domain == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "domain not found"})
		return
	}

	// Check access: admin or owner
	if !claims.IsAdmin && domain.UserID != claims.UserID {
		c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "forbidden"})
		return
	}

	var req updateDomainRequest
	if err := c.BindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Update is_enabled if provided
	if req.IsEnabled != nil && *req.IsEnabled != domain.IsEnabled {
		domain.IsEnabled = *req.IsEnabled

		// Call agent
		cmd := "domain.enable"
		if !*req.IsEnabled {
			cmd = "domain.disable"
		}
		agentParams := map[string]interface{}{
			"domain": domain.Name,
		}
		paramsJSON, _ := json.Marshal(agentParams)

		_, err := h.cfg.Agent.Call(ctx, cmd, json.RawMessage(paramsJSON))
		if err != nil {
			status, body := translateAgentError(err)
			c.JSON(status, body)
			return
		}
	}

	// Update nginx_custom_directives if provided
	if req.NginxCustomDirectives != nil {
		if errMsg := validateNginxDirectives(*req.NginxCustomDirectives); errMsg != "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": errMsg})
			return
		}
		domain.NginxCustomDirectives = req.NginxCustomDirectives
	}

	domain.UpdatedAt = time.Now()

	if err := h.cfg.Domains.Update(ctx, domain); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to update domain"})
		return
	}

	c.JSON(http.StatusOK, getDomainResponse{Data: domain})
}

// delete removes a domain (admin only).
// Calls agent to remove the domain, then soft-deletes the DB row.
func (h *domainHandler) delete(c *gin.Context) {
	id := c.Param("id")
	if id == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "domain id required"})
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Second)
	defer cancel()

	domain, err := h.cfg.Domains.FindByID(ctx, id)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to fetch domain"})
		return
	}
	if domain == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "domain not found"})
		return
	}

	// Call agent to remove the domain
	agentParams := map[string]interface{}{
		"domain": domain.Name,
	}
	paramsJSON, _ := json.Marshal(agentParams)

	_, err = h.cfg.Agent.Call(ctx, "domain.delete", json.RawMessage(paramsJSON))
	if err != nil {
		status, body := translateAgentError(err)
		c.JSON(status, body)
		return
	}

	// Soft-delete the DB row
	if err := h.cfg.Domains.Delete(ctx, id); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to delete domain"})
		return
	}

	c.JSON(http.StatusNoContent, nil)
}

// validateNginxDirectives rejects dangerous directives.
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

// deriveLinuxUsername extracts a linux username from an email address.
// For now, use the part before @ or user id.
func deriveLinuxUsername(email string) string {
	if email != "" {
		parts := strings.Split(email, "@")
		if len(parts) > 0 {
			return parts[0]
		}
	}
	return "user"
}
