package api

import (
	"errors"
	"log/slog"
	"net/http"
	"regexp"

	"github.com/gin-gonic/gin"

	ginctx "git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/ginctx"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/repository"
)

// DomainPHPSettingsHandlerConfig wires the domain PHP settings routes.
type DomainPHPSettingsHandlerConfig struct {
	Domains  repository.DomainRepository
	PHPPools repository.PHPPoolRepository
}

// RegisterDomainPHPSettingsRoutes adds the PHP settings endpoints:
//   - GET  /domains/:id/php-settings
//   - PATCH /domains/:id/php-settings
func RegisterDomainPHPSettingsRoutes(g *gin.RouterGroup, cfg DomainPHPSettingsHandlerConfig) {
	h := &domainPHPSettingsHandler{cfg: cfg}
	g.GET("/domains/:id/php-settings", h.get)
	g.PATCH("/domains/:id/php-settings", h.patch)
}

type domainPHPSettingsHandler struct{ cfg DomainPHPSettingsHandlerConfig }

// getDomainPHPSettingsRequest is unused for GET; response is below.
type getDomainPHPSettingsResponse struct {
	PHPPoolID            *string `json:"php_pool_id,omitempty"`
	PHPVersion           *string `json:"php_version,omitempty"`
	PHPMemoryLimit       *string `json:"php_memory_limit,omitempty"`
	PHPUploadMaxFilesize *string `json:"php_upload_max_filesize,omitempty"`
	PHPPostMaxSize       *string `json:"php_post_max_size,omitempty"`
	PHPMaxInputVars      *int    `json:"php_max_input_vars,omitempty"`
	PHPMaxExecutionTime  *int    `json:"php_max_execution_time,omitempty"`
	PHPMaxInputTime      *int    `json:"php_max_input_time,omitempty"`
}

// updateDomainPHPSettingsRequest mirrors the six overridable fields.
// NULL values clear the override.
type updateDomainPHPSettingsRequest struct {
	PHPMemoryLimit       *string `json:"php_memory_limit"`
	PHPUploadMaxFilesize *string `json:"php_upload_max_filesize"`
	PHPPostMaxSize       *string `json:"php_post_max_size"`
	PHPMaxInputVars      *int    `json:"php_max_input_vars"`
	PHPMaxExecutionTime  *int    `json:"php_max_execution_time"`
	PHPMaxInputTime      *int    `json:"php_max_input_time"`
}

// regexes for input validation
var (
	regexSizeParam = regexp.MustCompile(`^(\d{1,8})([KMG]?)$`)
)

// validateSizeParam validates memory_limit, upload_max_filesize, post_max_size.
// Regex: ^\d+[KMG]?$ (case-insensitive), max 8 chars total, no special characters.
func validateSizeParam(s string) error {
	if len(s) > 8 {
		return errInvalidPHPSetting("too long (max 8 chars)")
	}
	if !regexSizeParam.MatchString(s) {
		return errInvalidPHPSetting("invalid format (digits + optional K/M/G)")
	}
	return nil
}

// validateIntParam validates max_input_vars, max_execution_time, max_input_time.
// Range: 1..86400 seconds (or vars).
func validateIntParam(i int, fieldName string) error {
	if i < 1 || i > 86400 {
		return errInvalidPHPSetting("out of range (1..86400)")
	}
	return nil
}

type errInvalidPHPSetting string

func (e errInvalidPHPSetting) Error() string {
	return "invalid_php_setting: " + string(e)
}

func (h *domainPHPSettingsHandler) get(c *gin.Context) {
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
		slog.ErrorContext(ctx, "get php-settings: load domain", "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal"})
		return
	}

	// Owner or admin check
	if !claims.IsAdmin && dom.UserID != claims.UserID {
		c.JSON(http.StatusForbidden, gin.H{"error": "forbidden"})
		return
	}

	resp := getDomainPHPSettingsResponse{
		PHPPoolID:            dom.PHPPoolID,
		PHPMemoryLimit:       dom.PHPMemoryLimit,
		PHPUploadMaxFilesize: dom.PHPUploadMaxFilesize,
		PHPPostMaxSize:       dom.PHPPostMaxSize,
		PHPMaxInputVars:      dom.PHPMaxInputVars,
		PHPMaxExecutionTime:  dom.PHPMaxExecutionTime,
		PHPMaxInputTime:      dom.PHPMaxInputTime,
	}

	// Resolve the effective PHP version. If the domain is bound to a
	// user pool, return that pool's version. If unbound, fall back to the
	// user's own pool (ADR-0023: one pool per user). If neither exists,
	// leave nil and the UI renders "Server default".
	if dom.PHPPoolID != nil && *dom.PHPPoolID != "" {
		if pool, perr := h.cfg.PHPPools.FindByID(ctx, *dom.PHPPoolID); perr == nil && pool != nil {
			v := pool.PHPVersion
			resp.PHPVersion = &v
		}
	} else if pool, perr := h.cfg.PHPPools.FindByUserID(ctx, dom.UserID); perr == nil && pool != nil {
		v := pool.PHPVersion
		resp.PHPVersion = &v
	}

	c.JSON(http.StatusOK, resp)
}

func (h *domainPHPSettingsHandler) patch(c *gin.Context) {
	claims := ginctx.Claims(c)
	if claims == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	var req updateDomainPHPSettingsRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_body"})
		return
	}

	// Validate each field
	if req.PHPMemoryLimit != nil {
		if err := validateSizeParam(*req.PHPMemoryLimit); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
	}
	if req.PHPUploadMaxFilesize != nil {
		if err := validateSizeParam(*req.PHPUploadMaxFilesize); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
	}
	if req.PHPPostMaxSize != nil {
		if err := validateSizeParam(*req.PHPPostMaxSize); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
	}
	if req.PHPMaxInputVars != nil {
		if err := validateIntParam(*req.PHPMaxInputVars, "max_input_vars"); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
	}
	if req.PHPMaxExecutionTime != nil {
		if err := validateIntParam(*req.PHPMaxExecutionTime, "max_execution_time"); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
	}
	if req.PHPMaxInputTime != nil {
		if err := validateIntParam(*req.PHPMaxInputTime, "max_input_time"); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
	}

	ctx := c.Request.Context()
	domainID := c.Param("id")

	dom, err := h.cfg.Domains.FindByID(ctx, domainID)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "domain_not_found"})
			return
		}
		slog.ErrorContext(ctx, "patch php-settings: load domain", "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal"})
		return
	}

	// Owner or admin check
	if !claims.IsAdmin && dom.UserID != claims.UserID {
		c.JSON(http.StatusForbidden, gin.H{"error": "forbidden"})
		return
	}

	// Update settings
	settings := repository.DomainPHPSettings{
		MemoryLimit:       req.PHPMemoryLimit,
		UploadMaxFilesize: req.PHPUploadMaxFilesize,
		PostMaxSize:       req.PHPPostMaxSize,
		MaxInputVars:      req.PHPMaxInputVars,
		MaxExecutionTime:  req.PHPMaxExecutionTime,
		MaxInputTime:      req.PHPMaxInputTime,
	}

	if err := h.cfg.Domains.UpdatePHPSettings(ctx, domainID, settings); err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "domain_not_found"})
			return
		}
		slog.ErrorContext(ctx, "patch php-settings: update", "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal"})
		return
	}

	// Trigger reconciler to re-provision this domain
	// (Placeholder: caller should pass Reconciler and invoke ReconcileOne)
	// For now, return 200 OK.

	c.JSON(http.StatusOK, gin.H{"success": true})
}
