package api

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/agent"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/ginctx"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/ids"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/models"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/repository"
)

// PHPPoolHandlerConfig plugs the PHP pool handlers into the router.
type PHPPoolHandlerConfig struct {
	PHPPools       repository.PHPPoolRepository
	PHPPoolIniOverrides repository.PHPPoolIniOverrideRepository
	Users          repository.UserRepository
	Packages       repository.PackageRepository
	Agent          agent.AgentInterface
}

const (
	defaultPHPPoolPageSize = 20
	maxPHPPoolPageSize     = 200
)

// RegisterPHPPoolRoutes mounts /php-pools* under g.
// - GET /php-pools (admin: all; user: scoped to self)
// - GET /php-pools/:id (admin: all; user: scoped to self)
// - POST /php-pools (admin: all; user: own only)
// - PUT /php-pools/:id (admin: all; user: scoped to self)
// - DELETE /php-pools/:id (admin: all; user: scoped to self)
// - GET /php-pools/:id/ini-overrides (admin: all; user: scoped to self)
// - POST /php-pools/:id/ini-overrides (admin: all; user: scoped to self)
// - PUT /php-pools/:id/ini-overrides/:override_id (admin: all; user: scoped to self)
// - DELETE /php-pools/:id/ini-overrides/:override_id (admin: all; user: scoped to self)
func RegisterPHPPoolRoutes(g *gin.RouterGroup, cfg PHPPoolHandlerConfig) {
	h := &phpPoolHandler{cfg: cfg}

	pools := g.Group("/php-pools")
	pools.GET("", h.list)
	pools.GET("/:id", h.get)
	pools.POST("", h.create)
	pools.PUT("/:id", h.update)
	pools.DELETE("/:id", h.delete)

	// INI overrides are nested under pools
	pools.GET("/:id/ini-overrides", h.listIniOverrides)
	pools.POST("/:id/ini-overrides", h.createIniOverride)
	pools.PUT("/:id/ini-overrides/:override_id", h.updateIniOverride)
	pools.DELETE("/:id/ini-overrides/:override_id", h.deleteIniOverride)
}

type phpPoolHandler struct{ cfg PHPPoolHandlerConfig }

// ---- Requests/Responses ----

type createPHPPoolRequest struct {
	UserID                    string `json:"user_id"`
	PHPVersion                string `json:"php_version"`
	PmMode                    string `json:"pm_mode"`
	PmMaxChildren             uint32 `json:"pm_max_children"`
	ProcessIdleTimeoutSeconds uint32 `json:"process_idle_timeout_seconds"`
}

type updatePHPPoolRequest struct {
	PmMode                    string `json:"pm_mode"`
	PmMaxChildren             uint32 `json:"pm_max_children"`
	ProcessIdleTimeoutSeconds uint32 `json:"process_idle_timeout_seconds"`
}

type createPHPPoolIniOverrideRequest struct {
	Directive string `json:"directive"`
	Value     string `json:"value"`
	Kind      string `json:"kind"` // "value" or "flag"
}

type updatePHPPoolIniOverrideRequest struct {
	Value string `json:"value"`
}

// ---- List ----

// list returns all PHP pools for the authenticated user (or all if admin).
// Supports pagination and filtering.
func (h *phpPoolHandler) list(c *gin.Context) {
	claims := ginctx.Claims(c)
	if claims == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	page, pageSize, opts := parseListOptions(c, defaultPHPPoolPageSize, maxPHPPoolPageSize)
	ctx := c.Request.Context()

	var pools []models.PHPPool
	var total int64
	var err error

	if claims.IsAdmin {
		// Admins see all pools
		pools, total, err = h.cfg.PHPPools.ListAll(ctx, opts)
	} else {
		// Users see only their own pools
		pool, err := h.cfg.PHPPools.FindByUserID(ctx, claims.UserID)
		if err != nil && !isNotFound(err) {
			slog.ErrorContext(ctx, "failed to find PHP pool", "error", err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal"})
			return
		}
		if pool != nil {
			pools = []models.PHPPool{*pool}
			total = 1
		} else {
			pools = []models.PHPPool{}
			total = 0
		}
	}

	if err != nil {
		slog.ErrorContext(ctx, "failed to list PHP pools", "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"data": pools,
		"meta": gin.H{
			"total":  total,
			"page":   page,
			"limit":  pageSize,
			"offset": opts.Offset,
		},
	})
}

// ---- Get ----

// get returns a single PHP pool by ID.
func (h *phpPoolHandler) get(c *gin.Context) {
	claims := ginctx.Claims(c)
	if claims == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	poolID := c.Param("id")
	ctx := c.Request.Context()

	pool, err := h.cfg.PHPPools.FindByID(ctx, poolID)
	if err != nil {
		if isNotFound(err) {
			c.JSON(http.StatusNotFound, gin.H{"error": "pool_not_found"})
			return
		}
		slog.ErrorContext(ctx, "failed to find PHP pool", "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal"})
		return
	}

	// Check authorization: user can only see their own pools
	if !claims.IsAdmin && pool.UserID != claims.UserID {
		c.JSON(http.StatusForbidden, gin.H{"error": "forbidden"})
		return
	}

	c.JSON(http.StatusOK, pool)
}

// ---- Create ----

// create creates a new PHP pool for a user.
// The pool is created with status="pending" and must be reconciled by the agent.
func (h *phpPoolHandler) create(c *gin.Context) {
	claims := ginctx.Claims(c)
	if claims == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	var req createPHPPoolRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_request", "detail": err.Error()})
		return
	}

	ctx := c.Request.Context()

	// Determine target user ID
	targetUserID := req.UserID
	if !claims.IsAdmin {
		// Non-admin users can only create pools for themselves
		targetUserID = claims.UserID
	}

	// If target user is not the requestor, must be admin
	if targetUserID != claims.UserID && !claims.IsAdmin {
		c.JSON(http.StatusForbidden, gin.H{"error": "forbidden"})
		return
	}

	// Validate target user exists
	_,  err := h.cfg.Users.FindByID(ctx, targetUserID)
	if err != nil {
		if isNotFound(err) {
			c.JSON(http.StatusBadRequest, gin.H{"error": "user_not_found"})
			return
		}
		slog.ErrorContext(ctx, "failed to find user", "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal"})
		return
	}

	// Validate PHP version format: X.Y (handled by agent validation)
	if req.PHPVersion == "" {
		c.JSON(http.StatusBadRequest, gin.H{
			"error":  "invalid_php_version",
			"detail": "php_version is required",
		})
		return
	}

	// Validate pm_mode
	validPmModes := map[string]bool{"static": true, "ondemand": true, "dynamic": true}
	if req.PmMode == "" {
		req.PmMode = "ondemand" // default
	} else if !validPmModes[req.PmMode] {
		c.JSON(http.StatusBadRequest, gin.H{
			"error":  "invalid_pm_mode",
			"detail": "pm_mode must be static, ondemand, or dynamic",
		})
		return
	}

	// Validate pm_max_children
	if req.PmMaxChildren == 0 {
		req.PmMaxChildren = 20 // default
	}

	// Validate process_idle_timeout_seconds
	if req.ProcessIdleTimeoutSeconds == 0 {
		req.ProcessIdleTimeoutSeconds = 60 // default
	}

	// Check if user already has a pool (MVP constraint: one pool per user)
	existingPool, err := h.cfg.PHPPools.FindByUserID(ctx, targetUserID)
	if err == nil && existingPool != nil {
		c.JSON(http.StatusConflict, gin.H{
			"error":  "pool_already_exists",
			"detail": "user already has a PHP pool assigned (MVP constraint)",
		})
		return
	}
	if err != nil && !isNotFound(err) {
		slog.ErrorContext(ctx, "failed to check existing pool", "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal"})
		return
	}

	// Create pool record with status="pending"
	now := time.Now().UTC()
	pool := &models.PHPPool{
		ID:                        ids.NewULID(),
		UserID:                    targetUserID,
		PHPVersion:                req.PHPVersion,
		PmMode:                    req.PmMode,
		PmMaxChildren:             req.PmMaxChildren,
		ProcessIdleTimeoutSeconds: req.ProcessIdleTimeoutSeconds,
		Status:                    "pending",
		CreatedAt:                 now,
		UpdatedAt:                 now,
	}

	if err := h.cfg.PHPPools.Create(ctx, pool); err != nil {
		if isConflict(err) {
			c.JSON(http.StatusConflict, gin.H{"error": "pool_already_exists"})
			return
		}
		slog.ErrorContext(ctx, "failed to create PHP pool", "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal"})
		return
	}

	// Trigger agent to reconcile the pool asynchronously (fire-and-forget)
	go h.reconcilePoolAsync(pool)

	c.JSON(http.StatusCreated, pool)
}

// ---- Update ----

// update updates a PHP pool's configuration.
// Changes to settings trigger a re-reconciliation of the pool.
func (h *phpPoolHandler) update(c *gin.Context) {
	claims := ginctx.Claims(c)
	if claims == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	poolID := c.Param("id")
	ctx := c.Request.Context()

	var req updatePHPPoolRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_request", "detail": err.Error()})
		return
	}

	// Load existing pool
	pool, err := h.cfg.PHPPools.FindByID(ctx, poolID)
	if err != nil {
		if isNotFound(err) {
			c.JSON(http.StatusNotFound, gin.H{"error": "pool_not_found"})
			return
		}
		slog.ErrorContext(ctx, "failed to find PHP pool", "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal"})
		return
	}

	// Check authorization: user can only update their own pools
	if !claims.IsAdmin && pool.UserID != claims.UserID {
		c.JSON(http.StatusForbidden, gin.H{"error": "forbidden"})
		return
	}

	// Validate pm_mode
	if req.PmMode != "" {
		validPmModes := map[string]bool{"static": true, "ondemand": true, "dynamic": true}
		if !validPmModes[req.PmMode] {
			c.JSON(http.StatusBadRequest, gin.H{
				"error":  "invalid_pm_mode",
				"detail": "pm_mode must be static, ondemand, or dynamic",
			})
			return
		}
		pool.PmMode = req.PmMode
	}

	// Validate pm_max_children
	if req.PmMaxChildren > 0 {
		pool.PmMaxChildren = req.PmMaxChildren
	}

	// Validate process_idle_timeout_seconds
	if req.ProcessIdleTimeoutSeconds > 0 {
		pool.ProcessIdleTimeoutSeconds = req.ProcessIdleTimeoutSeconds
	}

	pool.UpdatedAt = time.Now().UTC()
	pool.Status = "pending" // Mark for re-reconciliation

	if err := h.cfg.PHPPools.Update(ctx, pool); err != nil {
		slog.ErrorContext(ctx, "failed to update PHP pool", "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal"})
		return
	}

	// Trigger agent to re-reconcile the pool asynchronously (fire-and-forget)
	go h.reconcilePoolAsync(pool)

	c.JSON(http.StatusOK, pool)
}

// ---- Delete ----

// delete removes a PHP pool and all associated ini overrides.
func (h *phpPoolHandler) delete(c *gin.Context) {
	claims := ginctx.Claims(c)
	if claims == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	poolID := c.Param("id")
	ctx := c.Request.Context()

	pool, err := h.cfg.PHPPools.FindByID(ctx, poolID)
	if err != nil {
		if isNotFound(err) {
			c.JSON(http.StatusNotFound, gin.H{"error": "pool_not_found"})
			return
		}
		slog.ErrorContext(ctx, "failed to find PHP pool", "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal"})
		return
	}

	// Check authorization
	if !claims.IsAdmin && pool.UserID != claims.UserID {
		c.JSON(http.StatusForbidden, gin.H{"error": "forbidden"})
		return
	}

	// Delete all ini overrides first
	overrides, err := h.cfg.PHPPoolIniOverrides.ListByPool(ctx, poolID)
	if err != nil {
		slog.ErrorContext(ctx, "failed to list ini overrides", "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal"})
		return
	}
	for _, override := range overrides {
		if err := h.cfg.PHPPoolIniOverrides.Delete(ctx, override.ID); err != nil {
			slog.ErrorContext(ctx, "failed to delete ini override", "error", err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal"})
			return
		}
	}

	// Delete the pool record
	if err := h.cfg.PHPPools.Delete(ctx, poolID); err != nil {
		slog.ErrorContext(ctx, "failed to delete PHP pool", "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal"})
		return
	}

	// Trigger agent to remove the pool asynchronously (fire-and-forget)
	go func() {
		agentCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		// Load user details
		user, err := h.cfg.Users.FindByID(agentCtx, pool.UserID)
		if err != nil {
			slog.ErrorContext(agentCtx, "failed to find user for removal", "error", err)
			return
		}

		// Call agent to remove the pool
		_, err = h.cfg.Agent.Call(agentCtx, "php.pool.remove", map[string]any{
			"username": user.Username,
		})
		if err != nil {
			slog.ErrorContext(agentCtx, "agent failed to remove pool", "error", err)
		}
	}()

	c.JSON(http.StatusNoContent, nil)
}

// ---- INI Overrides: List ----

// listIniOverrides returns all ini overrides for a PHP pool.
func (h *phpPoolHandler) listIniOverrides(c *gin.Context) {
	claims := ginctx.Claims(c)
	if claims == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	poolID := c.Param("id")
	ctx := c.Request.Context()

	// Check authorization: verify pool ownership
	pool, err := h.cfg.PHPPools.FindByID(ctx, poolID)
	if err != nil {
		if isNotFound(err) {
			c.JSON(http.StatusNotFound, gin.H{"error": "pool_not_found"})
			return
		}
		slog.ErrorContext(ctx, "failed to find PHP pool", "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal"})
		return
	}

	if !claims.IsAdmin && pool.UserID != claims.UserID {
		c.JSON(http.StatusForbidden, gin.H{"error": "forbidden"})
		return
	}

	page, pageSize, opts := parseListOptions(c, defaultPHPPoolPageSize, maxPHPPoolPageSize)
	overrides, err := h.cfg.PHPPoolIniOverrides.ListByPool(ctx, poolID)
	if err != nil {
		slog.ErrorContext(ctx, "failed to list ini overrides", "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal"})
		return
	}

	total := int64(len(overrides))
	c.JSON(http.StatusOK, gin.H{
		"data": overrides,
		"meta": gin.H{
			"total":  total,
			"page":   page,
			"limit":  pageSize,
			"offset": opts.Offset,
		},
	})
}

// ---- INI Overrides: Create ----

// createIniOverride creates a new ini override for a PHP pool.
func (h *phpPoolHandler) createIniOverride(c *gin.Context) {
	claims := ginctx.Claims(c)
	if claims == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	poolID := c.Param("id")
	ctx := c.Request.Context()

	var req createPHPPoolIniOverrideRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_request", "detail": err.Error()})
		return
	}

	// Check authorization: verify pool ownership
	pool, err := h.cfg.PHPPools.FindByID(ctx, poolID)
	if err != nil {
		if isNotFound(err) {
			c.JSON(http.StatusNotFound, gin.H{"error": "pool_not_found"})
			return
		}
		slog.ErrorContext(ctx, "failed to find PHP pool", "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal"})
		return
	}

	if !claims.IsAdmin && pool.UserID != claims.UserID {
		c.JSON(http.StatusForbidden, gin.H{"error": "forbidden"})
		return
	}

	// Validate directive and kind
	if req.Directive == "" {
		c.JSON(http.StatusBadRequest, gin.H{
			"error":  "invalid_directive",
			"detail": "directive is required",
		})
		return
	}

	if req.Kind == "" {
		req.Kind = "value" // default
	}

	validKinds := map[string]bool{"value": true, "flag": true}
	if !validKinds[req.Kind] {
		c.JSON(http.StatusBadRequest, gin.H{
			"error":  "invalid_kind",
			"detail": "kind must be 'value' or 'flag'",
		})
		return
	}

	// Create ini override record
	now := time.Now().UTC()
	override := &models.PHPPoolIniOverride{
		ID:        ids.NewULID(),
		PoolID:    poolID,
		Directive: req.Directive,
		Value:     req.Value,
		Kind:      req.Kind,
		CreatedAt: now,
		UpdatedAt: now,
	}

	if err := h.cfg.PHPPoolIniOverrides.Create(ctx, override); err != nil {
		slog.ErrorContext(ctx, "failed to create ini override", "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal"})
		return
	}

	// Mark pool for re-reconciliation
	pool.Status = "pending"
	pool.UpdatedAt = time.Now().UTC()
	_ = h.cfg.PHPPools.Update(ctx, pool)

	// Trigger agent to re-reconcile the pool
	go h.reconcilePoolAsync(pool)

	c.JSON(http.StatusCreated, override)
}

// ---- INI Overrides: Update ----

// updateIniOverride updates an ini override value.
func (h *phpPoolHandler) updateIniOverride(c *gin.Context) {
	claims := ginctx.Claims(c)
	if claims == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	poolID := c.Param("id")
	overrideID := c.Param("override_id")
	ctx := c.Request.Context()

	var req updatePHPPoolIniOverrideRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_request", "detail": err.Error()})
		return
	}

	// Check authorization: verify pool ownership
	pool, err := h.cfg.PHPPools.FindByID(ctx, poolID)
	if err != nil {
		if isNotFound(err) {
			c.JSON(http.StatusNotFound, gin.H{"error": "pool_not_found"})
			return
		}
		slog.ErrorContext(ctx, "failed to find PHP pool", "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal"})
		return
	}

	if !claims.IsAdmin && pool.UserID != claims.UserID {
		c.JSON(http.StatusForbidden, gin.H{"error": "forbidden"})
		return
	}

	// Find the override
	override, err := h.cfg.PHPPoolIniOverrides.FindByID(ctx, overrideID)
	if err != nil {
		if isNotFound(err) {
			c.JSON(http.StatusNotFound, gin.H{"error": "override_not_found"})
			return
		}
		slog.ErrorContext(ctx, "failed to find ini override", "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal"})
		return
	}

	// Verify override belongs to this pool
	if override.PoolID != poolID {
		c.JSON(http.StatusNotFound, gin.H{"error": "override_not_found"})
		return
	}

	// Update the value
	override.Value = req.Value
	override.UpdatedAt = time.Now().UTC()

	if err := h.cfg.PHPPoolIniOverrides.Update(ctx, override); err != nil {
		slog.ErrorContext(ctx, "failed to update ini override", "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal"})
		return
	}

	// Mark pool for re-reconciliation
	pool.Status = "pending"
	pool.UpdatedAt = time.Now().UTC()
	_ = h.cfg.PHPPools.Update(ctx, pool)

	// Trigger agent to re-reconcile the pool
	go h.reconcilePoolAsync(pool)

	c.JSON(http.StatusOK, override)
}

// ---- INI Overrides: Delete ----

// deleteIniOverride removes an ini override from a PHP pool.
func (h *phpPoolHandler) deleteIniOverride(c *gin.Context) {
	claims := ginctx.Claims(c)
	if claims == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	poolID := c.Param("id")
	overrideID := c.Param("override_id")
	ctx := c.Request.Context()

	// Check authorization: verify pool ownership
	pool, err := h.cfg.PHPPools.FindByID(ctx, poolID)
	if err != nil {
		if isNotFound(err) {
			c.JSON(http.StatusNotFound, gin.H{"error": "pool_not_found"})
			return
		}
		slog.ErrorContext(ctx, "failed to find PHP pool", "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal"})
		return
	}

	if !claims.IsAdmin && pool.UserID != claims.UserID {
		c.JSON(http.StatusForbidden, gin.H{"error": "forbidden"})
		return
	}

	// Find the override to verify pool ownership
	override, err := h.cfg.PHPPoolIniOverrides.FindByID(ctx, overrideID)
	if err != nil {
		if isNotFound(err) {
			c.JSON(http.StatusNotFound, gin.H{"error": "override_not_found"})
			return
		}
		slog.ErrorContext(ctx, "failed to find ini override", "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal"})
		return
	}

	if override.PoolID != poolID {
		c.JSON(http.StatusNotFound, gin.H{"error": "override_not_found"})
		return
	}

	// Delete the override
	if err := h.cfg.PHPPoolIniOverrides.Delete(ctx, overrideID); err != nil {
		slog.ErrorContext(ctx, "failed to delete ini override", "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal"})
		return
	}

	// Mark pool for re-reconciliation
	pool.Status = "pending"
	pool.UpdatedAt = time.Now().UTC()
	_ = h.cfg.PHPPools.Update(ctx, pool)

	// Trigger agent to re-reconcile the pool
	go h.reconcilePoolAsync(pool)

	c.JSON(http.StatusNoContent, nil)
}

// ---- Helpers ----

// reconcilePoolAsync triggers a pool reconciliation in the background.
func (h *phpPoolHandler) reconcilePoolAsync(pool *models.PHPPool) {
	agentCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Load user details
	user, err := h.cfg.Users.FindByID(agentCtx, pool.UserID)
	if err != nil {
		slog.ErrorContext(agentCtx, "failed to find user for reconciliation", "error", err)
		return
	}

	// Fetch all ini overrides for this pool
	overrides, err := h.cfg.PHPPoolIniOverrides.ListByPool(agentCtx, pool.ID)
	if err != nil {
		slog.ErrorContext(agentCtx, "failed to fetch ini overrides", "error", err)
		return
	}

	// Convert overrides to agent format
	var adminValues []map[string]string
	var adminFlags []map[string]string

	for _, override := range overrides {
		kv := map[string]string{
			"name":  override.Directive,
			"value": override.Value,
		}
		if override.Kind == "flag" {
			adminFlags = append(adminFlags, kv)
		} else {
			adminValues = append(adminValues, kv)
		}
	}

	if adminValues == nil {
		adminValues = []map[string]string{}
	}
	if adminFlags == nil {
		adminFlags = []map[string]string{}
	}

	// Call agent to apply the pool
	_, err = h.cfg.Agent.Call(agentCtx, "php.pool.apply", map[string]any{
		"username":                      user.Username,
		"php_version":                   pool.PHPVersion,
		"pm_mode":                       pool.PmMode,
		"pm_max_children":               pool.PmMaxChildren,
		"process_idle_timeout_seconds":  pool.ProcessIdleTimeoutSeconds,
		"admin_values":                  adminValues,
		"admin_flags":                   adminFlags,
	})
	if err != nil {
		// Update pool status to "error"
		pool.Status = "error"
		errMsg := fmt.Sprintf("agent failed: %v", err)
		pool.LastError = &errMsg
		_ = h.cfg.PHPPools.Update(agentCtx, pool)
	} else {
		// Update pool status to "ready"
		pool.Status = "ready"
		pool.LastError = nil
		_ = h.cfg.PHPPools.Update(agentCtx, pool)
	}
}
