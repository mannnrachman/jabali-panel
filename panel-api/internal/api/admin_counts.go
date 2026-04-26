// admin_counts.go — single-shot resource totals for the admin Dashboard.
//
// The Dashboard landing card needs three small numbers (users / domains
// / mailboxes). Routing each through its own list endpoint with limit=1
// works for users + domains but mailboxes only ships per-domain lists,
// so the SPA would have to N+1 across domains. /admin/counts collapses
// that into one round-trip with three COUNT(*) queries.

package api

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/middleware"
)

// AdminCountsHandlerConfig holds the dependencies for the counts handler.
// Pass the raw *gorm.DB; we do bare COUNT(*) queries against the model
// tables rather than route through the typed repos because the repos
// don't expose a "count everything" method and adding one to each would
// be more surface area than this single handler needs.
type AdminCountsHandlerConfig struct {
	DB *gorm.DB
}

// adminCountsResponse is the wire shape the Dashboard reads.
type adminCountsResponse struct {
	Users     int64 `json:"users"`
	Domains   int64 `json:"domains"`
	Mailboxes int64 `json:"mailboxes"`
}

// RegisterAdminCountsRoutes mounts GET /admin/counts behind RequireAdmin.
func RegisterAdminCountsRoutes(g *gin.RouterGroup, cfg AdminCountsHandlerConfig) {
	if cfg.DB == nil {
		return
	}
	h := &adminCountsHandler{cfg: cfg}
	g.GET("/admin/counts", middleware.RequireAdmin(), h.get)
}

type adminCountsHandler struct {
	cfg AdminCountsHandlerConfig
}

func (h *adminCountsHandler) get(c *gin.Context) {
	ctx := c.Request.Context()
	db := h.cfg.DB.WithContext(ctx)

	var resp adminCountsResponse
	if err := db.Table("users").Count(&resp.Users).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal"})
		return
	}
	if err := db.Table("domains").Count(&resp.Domains).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal"})
		return
	}
	if err := db.Table("mailboxes").Count(&resp.Mailboxes).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal"})
		return
	}
	c.JSON(http.StatusOK, resp)
}
