// M46 — Database server admin ops (Server Settings ▸ Databases tab).
// One admin route family for cPanel/WHM-parity DB administration:
//
//	POST /admin/databases/root-password   set/rotate root|superuser pw (ADR-0097)
//
// Later M46 steps append config / maintenance / processes / admin-SSO
// methods to this same handler. Every route is RequireAdmin and every
// privileged action writes a db_admin_audit row (never a secret).
package api

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"log/slog"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/agent"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/ginctx"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/middleware"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/models"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/notifications"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/repository"
)

type DatabaseAdminOpsHandlerConfig struct {
	Agent   agent.AgentInterface
	DBAdmin repository.DBAdminRepository
	Log     *slog.Logger
	// Queue is optional — M14 events are best-effort; nil disables them.
	Queue *notifications.Queue
}

func RegisterDatabaseAdminOpsRoutes(g *gin.RouterGroup, cfg DatabaseAdminOpsHandlerConfig) {
	if cfg.DBAdmin == nil {
		panic("api.RegisterDatabaseAdminOpsRoutes: cfg.DBAdmin is nil")
	}
	h := &databaseAdminOpsHandler{cfg: cfg}
	grp := g.Group("/admin/databases")
	grp.Use(middleware.RequireAdmin())
	grp.POST("/root-password", h.rootPassword)
}

type databaseAdminOpsHandler struct{ cfg DatabaseAdminOpsHandlerConfig }

// dbEngineValid gates every M46 op to the two supported engines.
func dbEngineValid(e string) bool { return e == "mariadb" || e == "postgres" }

// audit writes a best-effort privileged-action row. A failed audit
// insert is logged, never surfaced as the operation's outcome — but
// detail must never carry a secret (caller contract).
func (h *databaseAdminOpsHandler) audit(ctx context.Context, actor, engine, action, target, outcome, detail string) {
	if h.cfg.DBAdmin == nil {
		return
	}
	if err := h.cfg.DBAdmin.Audit(ctx, models.DBAdminAudit{
		ActorUserID: actor,
		Engine:      engine,
		Action:      action,
		Target:      target,
		Outcome:     outcome,
		Detail:      detail,
	}); err != nil {
		h.cfg.Log.Error("db admin audit write failed", "action", action, "err", err)
	}
}

func (h *databaseAdminOpsHandler) publish(ctx context.Context, env notifications.Envelope) {
	if h.cfg.Queue == nil {
		return
	}
	if _, err := h.cfg.Queue.Publish(ctx, env); err != nil {
		h.cfg.Log.Warn("db admin M14 publish failed", "event", env.EventKind, "err", err)
	}
}

// genPassword returns a 32-char URL-safe random secret (192 bits).
func genPassword() (string, error) {
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

type rootPasswordRequest struct {
	Engine string `json:"engine"`
}

type rootPasswordResponse struct {
	// Password is revealed exactly once (mirrors M7
	// database_users.rotatePassword). Never stored in the panel DB,
	// never returned by any GET.
	Password string `json:"password"`
}

// rootPassword sets/rotates the MariaDB root / PostgreSQL postgres
// password ALONGSIDE socket/peer auth (ADR-0097). The agent enforces
// that the panel's socket/peer path survives; this handler never sees
// or persists the secret beyond the one-shot response.
func (h *databaseAdminOpsHandler) rootPassword(c *gin.Context) {
	ctx := c.Request.Context()
	claims := ginctx.Claims(c)
	if claims == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}
	var req rootPasswordRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_request"})
		return
	}
	if !dbEngineValid(req.Engine) {
		c.JSON(http.StatusUnprocessableEntity, gin.H{"error": "invalid_engine"})
		return
	}
	if h.cfg.Agent == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "agent_unavailable"})
		return
	}

	pw, err := genPassword()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal"})
		return
	}

	cmd := "db.root.set_password"
	if req.Engine == "postgres" {
		cmd = "db.postgres.superuser.set_password"
	}
	agentCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if _, err := h.cfg.Agent.Call(agentCtx, cmd, map[string]any{"new_password": pw}); err != nil {
		h.cfg.Log.Error("root password agent call failed", "engine", req.Engine, "err", err)
		h.audit(ctx, claims.UserID, req.Engine, "root_password.rotate", "", "error", "agent call failed")
		c.JSON(http.StatusBadGateway, gin.H{"error": "agent_failed"})
		return
	}

	h.audit(ctx, claims.UserID, req.Engine, "root_password.rotate", "", "ok", "")
	h.publish(ctx, notifications.Envelope{
		EventKind: "db.admin.root_password_rotated",
		Severity:  "warning",
		Title:     "Database root password rotated",
		Body:      "The " + req.Engine + " root/superuser password was rotated from the admin panel.",
		Deeplink:  "/jabali-admin/settings",
		UserID:    claims.UserID,
	})
	c.JSON(http.StatusOK, rootPasswordResponse{Password: pw})
}
