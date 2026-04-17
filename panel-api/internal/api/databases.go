package api

import (
	"context"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/agent"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/ginctx"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/ids"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/models"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/repository"
)

// DatabaseHandlerConfig plugs the database handlers into the router.
type DatabaseHandlerConfig struct {
	Databases      repository.DatabaseRepository
	DatabaseUsers  repository.DatabaseUserRepository
	Users          repository.UserRepository
	Packages       repository.PackageRepository
	Agent          agent.AgentInterface
}

const (
	defaultDatabasesPageSize = 20
	maxDatabasesPageSize     = 200
)

// RegisterDatabaseRoutes mounts /databases* under g.
// - GET /databases (admin: all; user: scoped to self)
// - GET /databases/:id (admin: all; user: scoped to self)
// - POST /databases (admin: all; user: own only)
// - DELETE /databases/:id (admin: all; user: own only)
func RegisterDatabaseRoutes(g *gin.RouterGroup, cfg DatabaseHandlerConfig) {
	h := &databaseHandler{cfg: cfg}

	databases := g.Group("/databases")
	databases.GET("", h.list)
	databases.GET("/:id", h.get)
	databases.POST("", h.create)
	databases.DELETE("/:id", h.delete)
}

type databaseHandler struct{ cfg DatabaseHandlerConfig }

// ---- handlers ----

func (h *databaseHandler) list(c *gin.Context) {
	page, pageSize, opts := parseListOptions(c, defaultDatabasesPageSize, maxDatabasesPageSize)

	// Get current user claims
	claims := ginctx.Claims(c)
	if claims == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	var dbs []models.Database
	var total int64
	var err error

	// Admins see all databases; users see only their own
	if claims.IsAdmin {
		dbs, total, err = h.cfg.Databases.List(c.Request.Context(), opts)
	} else {
		dbs, total, err = h.cfg.Databases.ListByUserID(c.Request.Context(), claims.UserID, opts)
	}

	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal"})
		return
	}

	if dbs == nil {
		dbs = []models.Database{}
	}
	c.JSON(http.StatusOK, gin.H{
		"data":      dbs,
		"total":     total,
		"page":      page,
		"page_size": pageSize,
	})
}

func (h *databaseHandler) get(c *gin.Context) {
	claims := ginctx.Claims(c)
	if claims == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	db, err := h.cfg.Databases.FindByID(c.Request.Context(), c.Param("id"))
	if err != nil {
		if isNotFound(err) {
			c.JSON(http.StatusNotFound, gin.H{"error": "not_found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal"})
		return
	}

	// Check authorization: admins can access any database; users can only access their own
	if !claims.IsAdmin && db.UserID != claims.UserID {
		c.JSON(http.StatusForbidden, gin.H{"error": "forbidden"})
		return
	}

	c.JSON(http.StatusOK, db)
}

type createDatabaseRequest struct {
	Name string `json:"name" binding:"required"`
}

func (h *databaseHandler) create(c *gin.Context) {
	claims := ginctx.Claims(c)
	if claims == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	var req createDatabaseRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_request", "detail": err.Error()})
		return
	}

	// Validate database name: ^[a-z][a-z0-9_]{0,30}$ (max 30 leaves room for username_ prefix)
	if !databaseNameValid(req.Name) {
		c.JSON(http.StatusBadRequest, gin.H{
			"error":  "invalid_database_name",
			"detail": "database name must match regex ^[a-z][a-z0-9_]{0,30}$",
		})
		return
	}

	ctx := c.Request.Context()
	targetUserID := claims.UserID

	// Load user and check for package/quota
	user, err := h.cfg.Users.FindByID(ctx, targetUserID)
	if err != nil {
		if isNotFound(err) {
			c.JSON(http.StatusBadRequest, gin.H{"error": "user_not_found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal"})
		return
	}

	// Non-admin users must have a username for the database prefix
	if !claims.IsAdmin && (user.Username == nil || *user.Username == "") {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal"})
		return
	}

	// Check quota
	max := int64(0)
	if user.PackageID != nil && *user.PackageID != "" {
		pkg, err := h.cfg.Packages.FindByID(ctx, *user.PackageID)
		if err == nil && pkg.MaxDatabases > 0 {
			max = int64(pkg.MaxDatabases)
			count, err := h.cfg.Databases.CountByUserID(ctx, targetUserID)
			if err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": "internal"})
				return
			}
			if count >= max {
				c.JSON(http.StatusConflict, gin.H{
					"error":    "quota_exceeded",
					"resource": "databases",
					"limit":    max,
				})
				return
			}
		}
	}

	// Compute final name with username prefix
	var finalName string
	if claims.IsAdmin {
		finalName = req.Name
	} else {
		finalName = *user.Username + "_" + req.Name
	}

	// Check for collision on the FINAL (prefixed) name — that's what
	// MariaDB sees and what we store in the row, so uniqueness is meaningful.
	exists, err := h.cfg.Databases.ExistsByUserAndName(ctx, targetUserID, finalName)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal"})
		return
	}
	if exists {
		c.JSON(http.StatusConflict, gin.H{"error": "database_name_exists"})
		return
	}

	// Call agent to create the database
	if h.cfg.Agent == nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal"})
		return
	}

	agentCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	_, err = h.cfg.Agent.Call(agentCtx, "db.create", map[string]any{
		"db_name":   finalName,
		"charset":   "utf8mb4",
		"collation": "utf8mb4_unicode_ci",
	})
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": "agent_failed", "detail": err.Error()})
		return
	}

	// Persist to database
	now := time.Now().UTC()
	d := &models.Database{
		ID:        ids.NewULID(),
		UserID:    targetUserID,
		Name:      finalName,
		Engine:    "mariadb",
		Charset:   "utf8mb4",
		Collation: "utf8mb4_unicode_ci",
		CreatedAt: now,
		UpdatedAt: now,
	}

	if err := h.cfg.Databases.Create(ctx, d); err != nil {
		if isConflict(err) {
			c.JSON(http.StatusConflict, gin.H{"error": "database_name_exists"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal"})
		return
	}

	c.JSON(http.StatusCreated, d)
}

func (h *databaseHandler) delete(c *gin.Context) {
	ctx := c.Request.Context()

	// Load the database first
	d, err := h.cfg.Databases.FindByID(ctx, c.Param("id"))
	if err != nil {
		if isNotFound(err) {
			c.JSON(http.StatusNotFound, gin.H{"error": "not_found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal"})
		return
	}

	claims := ginctx.Claims(c)
	if claims == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	// Check authorization: admins can delete any; users only their own
	if !claims.IsAdmin && d.UserID != claims.UserID {
		c.JSON(http.StatusForbidden, gin.H{"error": "forbidden"})
		return
	}

	// Call agent to drop the database
	if h.cfg.Agent == nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal"})
		return
	}

	agentCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	// d.Name is the full MariaDB-side name (prefix already baked in at
	// create time) so we pass it to the agent verbatim.
	_, err = h.cfg.Agent.Call(agentCtx, "db.drop", map[string]any{
		"db_name": d.Name,
	})
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": "agent_failed", "detail": err.Error()})
		return
	}

	// Delete from database
	if err := h.cfg.Databases.Delete(ctx, d.ID); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal"})
		return
	}

	c.Status(http.StatusNoContent)
}

// databaseNameValid validates a database name against the required pattern
func databaseNameValid(name string) bool {
	if len(name) == 0 || len(name) > 30 {
		return false
	}
	// Must start with lowercase letter, followed by lowercase letters, digits, or underscores
	if name[0] < 'a' || name[0] > 'z' {
		return false
	}
	for _, ch := range name {
		if !((ch >= 'a' && ch <= 'z') || (ch >= '0' && ch <= '9') || ch == '_') {
			return false
		}
	}
	return true
}
