// M35 admin REST endpoints — read-only for now (list/get/stages).
// Mutation endpoints (create/cancel/resume) land alongside the
// admin UI Step 8 once the JMAP-push + CreateUser orchestrator
// pieces stabilise. v1 ships read-only because operators can
// already create + run jobs via the cobra CLI (jabali migrate
// import); the REST adds a UI-friendly observation surface.
//
// Routes mounted under /admin/migrations:
//   GET    /admin/migrations               list (paginated envelope)
//   GET    /admin/migrations/:id           one job + recent stages
//   GET    /admin/migrations/:id/stages    full stage timeline
//   DELETE /admin/migrations/:id           soft-revoke (state→cancelled)
//
// Admin-gated. RequireAdmin already mounted by the parent group.
package api

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"

	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/middleware"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/migrate"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/models"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/repository"
)

// AdminMigrationsHandlerConfig is the dep set the routes need.
// Jobs is required; nil disables route registration.
type AdminMigrationsHandlerConfig struct {
	Jobs repository.MigrationJobRepository
}

// RegisterAdminMigrationRoutes mounts /admin/migrations* on g.
// g must already enforce RequireAuth + RequireAdmin upstream.
func RegisterAdminMigrationRoutes(g *gin.RouterGroup, cfg AdminMigrationsHandlerConfig) {
	if cfg.Jobs == nil {
		return
	}
	h := &adminMigrationsHandler{cfg: cfg}
	rg := g.Group("/admin/migrations")
	rg.Use(middleware.RequireAdmin())
	rg.GET("", h.list)
	rg.GET("/:id", h.get)
	rg.GET("/:id/stages", h.stages)
	rg.DELETE("/:id", h.cancel)
}

type adminMigrationsHandler struct{ cfg AdminMigrationsHandlerConfig }

type migrationJobListResponse struct {
	Data     []models.MigrationJob `json:"data"`
	Total    int64                 `json:"total"`
	Page     int                   `json:"page"`
	PageSize int                   `json:"page_size"`
}

func (h *adminMigrationsHandler) list(c *gin.Context) {
	page, pageSize := paginationParams(c)
	rows, total, err := h.cfg.Jobs.List(c.Request.Context(), page, pageSize)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal"})
		return
	}
	if rows == nil {
		rows = []models.MigrationJob{}
	}
	c.JSON(http.StatusOK, migrationJobListResponse{
		Data: rows, Total: total, Page: page, PageSize: pageSize,
	})
}

// migrationJobDetailResponse bundles the job header + the most
// recent stage rows so the UI can render the timeline without
// a follow-up call.
type migrationJobDetailResponse struct {
	Job    *models.MigrationJob     `json:"job"`
	Stages []models.MigrationStage  `json:"stages"`
}

func (h *adminMigrationsHandler) get(c *gin.Context) {
	job, err := h.cfg.Jobs.FindByID(c.Request.Context(), c.Param("id"))
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "not_found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal"})
		return
	}
	stages, err := h.cfg.Jobs.ListStages(c.Request.Context(), job.ID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal"})
		return
	}
	if stages == nil {
		stages = []models.MigrationStage{}
	}
	c.JSON(http.StatusOK, migrationJobDetailResponse{Job: job, Stages: stages})
}

func (h *adminMigrationsHandler) stages(c *gin.Context) {
	stages, err := h.cfg.Jobs.ListStages(c.Request.Context(), c.Param("id"))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal"})
		return
	}
	if stages == nil {
		stages = []models.MigrationStage{}
	}
	c.JSON(http.StatusOK, gin.H{"data": stages, "total": len(stages)})
}

// cancel soft-revokes a job — transitions to MigrationStateCancelled
// when the current state allows. Already-terminal jobs (done /
// failed / cancelled) return 409 so the operator knows the row was
// never advanced.
//
// NOTE: cancel does NOT kill an in-flight `jabali migrate import`
// process. v1 transient-unit invoker is the operator's cobra
// shell; cancelling at the panel layer just stamps the DB row.
// A follow-up agent-level kill switch is M35 Step 8 follow-up.
func (h *adminMigrationsHandler) cancel(c *gin.Context) {
	id := c.Param("id")
	job, err := h.cfg.Jobs.FindByID(c.Request.Context(), id)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "not_found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal"})
		return
	}
	if !migrate.IsValidJobTransition(job.State, models.MigrationStateCancelled) {
		c.JSON(http.StatusConflict, gin.H{
			"error":  "illegal_transition",
			"detail": "job in state '" + job.State + "' cannot be cancelled (already terminal?)",
		})
		return
	}
	reason := "cancelled by admin"
	if err := h.cfg.Jobs.UpdateState(c.Request.Context(), id, models.MigrationStateCancelled, &reason); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"id": id, "state": models.MigrationStateCancelled})
}

// paginationParams pulls ?page= + ?page_size= with sane defaults.
// Reused shape from M44 + M30 admin endpoints; not extracted into
// a shared helper because each handler tunes its own caps.
func paginationParams(c *gin.Context) (page, pageSize int) {
	page = 1
	pageSize = 50
	if v := c.Query("page"); v != "" {
		if n, err := atoiNonNeg(v); err == nil && n > 0 {
			page = n
		}
	}
	if v := c.Query("page_size"); v != "" {
		if n, err := atoiNonNeg(v); err == nil && n > 0 && n <= 200 {
			pageSize = n
		}
	}
	return page, pageSize
}

// atoiNonNeg is strconv.Atoi with a non-negative refusal so a
// negative `page` doesn't underflow the offset calculation in the
// repo.
func atoiNonNeg(s string) (int, error) {
	n := 0
	for _, r := range s {
		if r < '0' || r > '9' {
			return 0, errors.New("non-digit")
		}
		n = n*10 + int(r-'0')
	}
	return n, nil
}
