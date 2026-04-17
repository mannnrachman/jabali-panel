package api

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/ginctx"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/models"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/repository"
)

// DatabaseHandlerConfig plugs the database handlers into the router.
type DatabaseHandlerConfig struct {
	Databases      repository.DatabaseRepository
	DatabaseUsers  repository.DatabaseUserRepository
	Users          repository.UserRepository
	Packages       repository.PackageRepository
}

const (
	defaultDatabasesPageSize = 20
	maxDatabasesPageSize     = 200
)

// RegisterDatabaseRoutes mounts /databases* under g.
// - GET /databases (admin: all; user: scoped to self)
// - GET /databases/:id (admin: all; user: scoped to self)
func RegisterDatabaseRoutes(g *gin.RouterGroup, cfg DatabaseHandlerConfig) {
	h := &databaseHandler{cfg: cfg}

	databases := g.Group("/databases")
	databases.GET("", h.list)
	databases.GET("/:id", h.get)
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

	c.JSON(http.StatusOK, gin.H{
		"items":      dbs,
		"total":      total,
		"page":       page,
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
