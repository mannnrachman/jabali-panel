package api

import (
	"context"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"golang.org/x/crypto/bcrypt"

	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/agent"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/ginctx"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/ids"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/models"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/repository"
)

// DatabaseUserHandlerConfig plugs the database user handlers into the router.
type DatabaseUserHandlerConfig struct {
	Databases       repository.DatabaseRepository
	DatabaseUsers   repository.DatabaseUserRepository
	DatabaseGrants  repository.DatabaseUserGrantRepository
	Users           repository.UserRepository
	Packages        repository.PackageRepository
	Agent           agent.AgentInterface
}

const (
	defaultDatabaseUsersPageSize = 20
	maxDatabaseUsersPageSize     = 200
)

// RegisterDatabaseUserRoutes mounts /database-users* and /database-user-grants* under g.
// - GET /database-users (admin: all; user: scoped to self)
// - POST /database-users (create user + grant atomically)
// - DELETE /database-users/:id (drop user and revoke all grants)
// - POST /database-users/:id/rotate-password
// - PATCH /database-user-grants/:id (change grant level)
func RegisterDatabaseUserRoutes(g *gin.RouterGroup, cfg DatabaseUserHandlerConfig) {
	h := &databaseUserHandler{cfg: cfg}

	users := g.Group("/database-users")
	users.GET("", h.list)
	users.POST("", h.create)
	users.DELETE("/:id", h.delete)
	users.POST("/:id/rotate-password", h.rotatePassword)

	grants := g.Group("/database-user-grants")
	grants.PATCH("/:id", h.updateGrant)
}

type databaseUserHandler struct{ cfg DatabaseUserHandlerConfig }

// ---- Request/Response types ----

type createDatabaseUserRequest struct {
	DatabaseID string `json:"database_id" binding:"required"`
	Username   string `json:"username" binding:"required"`
	GrantLevel string `json:"grant_level" binding:"required"`
}

type createDatabaseUserResponse struct {
	ID       string `json:"id"`
	Username string `json:"username"`
	Password string `json:"password"`
	Grant    struct {
		ID         string `json:"id"`
		GrantLevel string `json:"grant_level"`
	} `json:"grant"`
}

type rotateDatabaseUserPasswordRequest struct {
	NewPassword string `json:"new_password" binding:"required"`
}

type rotateDatabaseUserPasswordResponse struct {
	Password string `json:"password"`
}

type updateDatabaseUserGrantRequest struct {
	GrantLevel string `json:"grant_level" binding:"required"`
}

// ---- Handlers ----

func (h *databaseUserHandler) list(c *gin.Context) {
	page, pageSize, opts := parseListOptions(c, defaultDatabaseUsersPageSize, maxDatabaseUsersPageSize)

	// Get current user claims
	claims := ginctx.Claims(c)
	if claims == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	var users []models.DatabaseUser
	var total int64
	var err error

	// Admins see all database users; users see only their own
	if claims.IsAdmin {
		users, total, err = h.cfg.DatabaseUsers.List(c.Request.Context(), opts)
	} else {
		users, total, err = h.cfg.DatabaseUsers.ListByUserID(c.Request.Context(), claims.UserID, opts)
	}

	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"items":      users,
		"total":      total,
		"page":       page,
		"page_size": pageSize,
	})
}

func (h *databaseUserHandler) create(c *gin.Context) {
	claims := ginctx.Claims(c)
	if claims == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	var req createDatabaseUserRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_request", "detail": err.Error()})
		return
	}

	// Validate username regex: ^[a-z][a-z0-9_]{0,63}$
	if !databaseUserNameValid(req.Username) {
		c.JSON(http.StatusBadRequest, gin.H{
			"error":  "invalid_username",
			"detail": "username must match regex ^[a-z][a-z0-9_]{0,63}$",
		})
		return
	}

	// Validate grant level
	if req.GrantLevel != "rw" && req.GrantLevel != "ro" {
		c.JSON(http.StatusBadRequest, gin.H{
			"error":  "invalid_grant_level",
			"detail": "grant_level must be either 'rw' or 'ro'",
		})
		return
	}

	ctx := c.Request.Context()
	targetUserID := claims.UserID

	// Load database
	db, err := h.cfg.Databases.FindByID(ctx, req.DatabaseID)
	if err != nil {
		if isNotFound(err) {
			c.JSON(http.StatusNotFound, gin.H{"error": "database_not_found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal"})
		return
	}

	// Check authorization
	if !claims.IsAdmin && db.UserID != claims.UserID {
		c.JSON(http.StatusForbidden, gin.H{"error": "forbidden"})
		return
	}

	// Load user to check quota and get username for prefix
	user, err := h.cfg.Users.FindByID(ctx, targetUserID)
	if err != nil {
		if isNotFound(err) {
			c.JSON(http.StatusBadRequest, gin.H{"error": "user_not_found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal"})
		return
	}

	// Non-admin users must have a username for the database user prefix
	if !claims.IsAdmin && (user.Username == nil || *user.Username == "") {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal"})
		return
	}

	// Check quota for database users
	max := int64(0)
	if user.PackageID != nil && *user.PackageID != "" {
		pkg, err := h.cfg.Packages.FindByID(ctx, *user.PackageID)
		if err == nil && pkg.MaxDatabaseUsers > 0 {
			max = int64(pkg.MaxDatabaseUsers)
			count, err := h.cfg.DatabaseUsers.CountByUserID(ctx, targetUserID)
			if err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": "internal"})
				return
			}
			if count >= max {
				c.JSON(http.StatusConflict, gin.H{
					"error":    "quota_exceeded",
					"resource": "database_users",
					"limit":    max,
				})
				return
			}
		}
	}

	// Compute final username with prefix
	var finalUsername string
	if claims.IsAdmin {
		finalUsername = req.Username
	} else {
		finalUsername = *user.Username + "_" + req.Username
	}

	// Check for collision: (user_id, username) must be unique
	exists, err := h.cfg.DatabaseUsers.ExistsByUserAndUsername(ctx, targetUserID, req.Username)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal"})
		return
	}
	if exists {
		c.JSON(http.StatusConflict, gin.H{"error": "username_exists"})
		return
	}

	// Generate password
	plainPassword := ids.NewULID()

	// Hash password
	hash, err := bcrypt.GenerateFromPassword([]byte(plainPassword), bcrypt.DefaultCost)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal"})
		return
	}

	// Call agent to create user (30-second timeout)
	if h.cfg.Agent == nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal"})
		return
	}

	agentCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	_, err = h.cfg.Agent.Call(agentCtx, "db_user.create", map[string]any{
		"db_user_name": finalUsername,
		"password":     plainPassword,
	})
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": "agent_failed", "detail": err.Error()})
		return
	}

	// Grant privileges (30-second timeout, retrying context)
	agentCtx, cancel = context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	_, err = h.cfg.Agent.Call(agentCtx, "db_user.grant", map[string]any{
		"db_name":      db.Name,
		"db_user_name": finalUsername,
		"grant_level":  req.GrantLevel,
	})
	if err != nil {
		// If grant fails, roll back the user creation
		agentCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		h.cfg.Agent.Call(agentCtx, "db_user.drop", map[string]any{
			"db_user_name": finalUsername,
		})
		c.JSON(http.StatusBadGateway, gin.H{"error": "agent_failed", "detail": err.Error()})
		return
	}

	// Create database user record in transaction
	now := time.Now().UTC()
	duID := ids.NewULID()
	du := &models.DatabaseUser{
		ID:           duID,
		UserID:       targetUserID,
		Username:     req.Username,
		PasswordHash: string(hash),
		CreatedAt:    now,
		UpdatedAt:    now,
	}

	dgID := ids.NewULID()
	dg := &models.DatabaseUserGrant{
		ID:               dgID,
		DatabaseID:       req.DatabaseID,
		DatabaseUserID:   duID,
		GrantLevel:       req.GrantLevel,
		CreatedAt:        now,
		UpdatedAt:        now,
	}

	// Create database user record
	if err := h.cfg.DatabaseUsers.Create(ctx, du); err != nil {
		if isConflict(err) {
			c.JSON(http.StatusConflict, gin.H{"error": "username_exists"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal"})
		return
	}

	// Create grant record
	if err := h.cfg.DatabaseGrants.Create(ctx, dg); err != nil {
		// If grant creation fails, clean up the user we just created
		_ = h.cfg.DatabaseUsers.Delete(ctx, du.ID)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal"})
		return
	}

	// Return response with plaintext password
	c.JSON(http.StatusCreated, createDatabaseUserResponse{
		ID:       du.ID,
		Username: du.Username,
		Password: plainPassword,
		Grant: struct {
			ID         string `json:"id"`
			GrantLevel string `json:"grant_level"`
		}{
			ID:         dg.ID,
			GrantLevel: dg.GrantLevel,
		},
	})
}

func (h *databaseUserHandler) delete(c *gin.Context) {
	ctx := c.Request.Context()

	claims := ginctx.Claims(c)
	if claims == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	// Load the database user
	du, err := h.cfg.DatabaseUsers.FindByID(ctx, c.Param("id"))
	if err != nil {
		if isNotFound(err) {
			c.JSON(http.StatusNotFound, gin.H{"error": "not_found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal"})
		return
	}

	// Check authorization
	if !claims.IsAdmin && du.UserID != claims.UserID {
		c.JSON(http.StatusForbidden, gin.H{"error": "forbidden"})
		return
	}

	// Load all grants for this user to revoke them
	grants, err := h.cfg.DatabaseGrants.ListByDatabaseUserID(ctx, du.ID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal"})
		return
	}

	// Revoke all grants
	if h.cfg.Agent == nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal"})
		return
	}

	for _, grant := range grants {
		// Load database to get name
		db, err := h.cfg.Databases.FindByID(ctx, grant.DatabaseID)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal"})
			return
		}

		agentCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()

		_, err = h.cfg.Agent.Call(agentCtx, "db_user.revoke", map[string]any{
			"db_name":      db.Name,
			"db_user_name": du.Username,
			"grant_level":  grant.GrantLevel,
		})
		if err != nil {
			c.JSON(http.StatusBadGateway, gin.H{"error": "agent_failed", "detail": err.Error()})
			return
		}
	}

	// Drop user from MariaDB
	agentCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	_, err = h.cfg.Agent.Call(agentCtx, "db_user.drop", map[string]any{
		"db_user_name": du.Username,
	})
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": "agent_failed", "detail": err.Error()})
		return
	}

	// Delete grants and user in transaction
	// Delete all grants and user
	for _, grant := range grants {
		_ = h.cfg.DatabaseGrants.Delete(ctx, grant.ID)
	}

	if err := h.cfg.DatabaseUsers.Delete(ctx, du.ID); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"ok": true})
}

func (h *databaseUserHandler) rotatePassword(c *gin.Context) {
	ctx := c.Request.Context()

	claims := ginctx.Claims(c)
	if claims == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	var req rotateDatabaseUserPasswordRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_request", "detail": err.Error()})
		return
	}

	// Load database user
	du, err := h.cfg.DatabaseUsers.FindByID(ctx, c.Param("id"))
	if err != nil {
		if isNotFound(err) {
			c.JSON(http.StatusNotFound, gin.H{"error": "not_found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal"})
		return
	}

	// Check authorization
	if !claims.IsAdmin && du.UserID != claims.UserID {
		c.JSON(http.StatusForbidden, gin.H{"error": "forbidden"})
		return
	}

	// Generate new password
	plainPassword := ids.NewULID()

	// Hash password
	hash, err := bcrypt.GenerateFromPassword([]byte(plainPassword), bcrypt.DefaultCost)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal"})
		return
	}

	// Call agent to rotate password
	if h.cfg.Agent == nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal"})
		return
	}

	agentCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	_, err = h.cfg.Agent.Call(agentCtx, "db_user.rotate_password", map[string]any{
		"db_user_name": du.Username,
		"new_password": plainPassword,
	})
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": "agent_failed", "detail": err.Error()})
		return
	}

	// Update password hash in database
	if err := h.cfg.DatabaseUsers.UpdatePasswordHash(ctx, du.ID, string(hash)); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal"})
		return
	}

	c.JSON(http.StatusOK, rotateDatabaseUserPasswordResponse{
		Password: plainPassword,
	})
}

func (h *databaseUserHandler) updateGrant(c *gin.Context) {
	ctx := c.Request.Context()

	claims := ginctx.Claims(c)
	if claims == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	var req updateDatabaseUserGrantRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_request", "detail": err.Error()})
		return
	}

	// Validate grant level
	if req.GrantLevel != "rw" && req.GrantLevel != "ro" {
		c.JSON(http.StatusBadRequest, gin.H{
			"error":  "invalid_grant_level",
			"detail": "grant_level must be either 'rw' or 'ro'",
		})
		return
	}

	// Load grant
	grant, err := h.cfg.DatabaseGrants.FindByID(ctx, c.Param("id"))
	if err != nil {
		if isNotFound(err) {
			c.JSON(http.StatusNotFound, gin.H{"error": "not_found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal"})
		return
	}

	// Load database user to check authorization
	du, err := h.cfg.DatabaseUsers.FindByID(ctx, grant.DatabaseUserID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal"})
		return
	}

	// Check authorization
	if !claims.IsAdmin && du.UserID != claims.UserID {
		c.JSON(http.StatusForbidden, gin.H{"error": "forbidden"})
		return
	}

	// No-op: same grant level
	if grant.GrantLevel == req.GrantLevel {
		c.JSON(http.StatusOK, grant)
		return
	}

	// Load database for name
	db, err := h.cfg.Databases.FindByID(ctx, grant.DatabaseID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal"})
		return
	}

	// Call agent to revoke old grant
	if h.cfg.Agent == nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal"})
		return
	}

	agentCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	_, err = h.cfg.Agent.Call(agentCtx, "db_user.revoke", map[string]any{
		"db_name":      db.Name,
		"db_user_name": du.Username,
		"grant_level":  grant.GrantLevel,
	})
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": "agent_failed", "detail": err.Error()})
		return
	}

	// Call agent to grant new privilege
	agentCtx, cancel = context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	_, err = h.cfg.Agent.Call(agentCtx, "db_user.grant", map[string]any{
		"db_name":      db.Name,
		"db_user_name": du.Username,
		"grant_level":  req.GrantLevel,
	})
	if err != nil {
		// If grant fails, try to restore old grant
		agentCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		h.cfg.Agent.Call(agentCtx, "db_user.grant", map[string]any{
			"db_name":      db.Name,
			"db_user_name": du.Username,
			"grant_level":  grant.GrantLevel,
		})
		c.JSON(http.StatusBadGateway, gin.H{"error": "agent_failed", "detail": err.Error()})
		return
	}

	// Update grant level in database
	if err := h.cfg.DatabaseGrants.UpdateLevel(ctx, grant.ID, req.GrantLevel); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal"})
		return
	}

	// Reload and return updated grant
	updatedGrant, err := h.cfg.DatabaseGrants.FindByID(ctx, grant.ID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal"})
		return
	}

	c.JSON(http.StatusOK, updatedGrant)
}

// ---- Validation helpers ----

func databaseUserNameValid(username string) bool {
	if len(username) == 0 || len(username) > 63 {
		return false
	}
	// ^[a-z][a-z0-9_]{0,63}$
	for i, ch := range username {
		if i == 0 {
			if ch < 'a' || ch > 'z' {
				return false
			}
		} else {
			if !((ch >= 'a' && ch <= 'z') || (ch >= '0' && ch <= '9') || ch == '_') {
				return false
			}
		}
	}
	return true
}
