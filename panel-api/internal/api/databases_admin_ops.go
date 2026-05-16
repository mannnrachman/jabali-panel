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
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"git.linux-hosting.co.il/shukivaknin/jabali2/internal/dbtuning"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/agent"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/ginctx"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/middleware"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/models"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/notifications"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/repository"
)

// ssoAdminAllSentinel is stored as the token's DatabaseID for an
// admin-scope SSO handoff. The per-user SSO path ALWAYS carries a real
// DatabaseID, so the validate handlers' early sentinel branch (ADR-0099)
// cannot regress it. pmaAdminPasswordFile / pgSuperuserPasswordFile are
// the agent-written 0640 root:jabali secret files the validator reads.
const (
	ssoAdminAllSentinel     = "__M46_ADMIN_ALL__"
	pmaAdminPasswordFile    = "/etc/jabali-panel/pma-admin.password"
	pgSuperuserPasswordFile = "/etc/jabali-panel/postgres.password"
)

type DatabaseAdminOpsHandlerConfig struct {
	Agent   agent.AgentInterface
	DBAdmin repository.DBAdminRepository
	Log     *slog.Logger
	// Queue is optional — M14 events are best-effort; nil disables them.
	Queue *notifications.Queue
	// SSO / AdminerSSO power the admin all-DBs handoff (ADR-0099).
	// Optional — nil disables the corresponding admin-SSO route.
	// Minimal interfaces so *sso.Service / *sso.AdminerService satisfy
	// them without this package depending on their concrete shape.
	SSO        adminTokenMinter
	AdminerSSO adminerTokenMinter
}

type adminTokenMinter interface {
	MintToken(ctx context.Context, userID, databaseID, dbName string) (string, error)
}

type adminerTokenMinter interface {
	MintAdminerToken(ctx context.Context, userID, databaseID, engine string) (string, error)
}

func RegisterDatabaseAdminOpsRoutes(g *gin.RouterGroup, cfg DatabaseAdminOpsHandlerConfig) {
	if cfg.DBAdmin == nil {
		panic("api.RegisterDatabaseAdminOpsRoutes: cfg.DBAdmin is nil")
	}
	h := &databaseAdminOpsHandler{cfg: cfg}
	grp := g.Group("/admin/databases")
	grp.Use(middleware.RequireAdmin())
	grp.POST("/root-password", h.rootPassword)
	grp.GET("/config", h.getConfig)
	grp.PUT("/config", h.putConfig)
	if cfg.SSO != nil {
		grp.POST("/sso/phpmyadmin", h.ssoPhpMyAdminAdmin)
	}
	if cfg.AdminerSSO != nil {
		grp.POST("/sso/adminer", h.ssoAdminerAdmin)
	}
}

// sameOriginOK is a lightweight CSRF guard (ADR-0099): the request must
// originate from the panel itself. Mirrors the per-user SSO handler's
// intent without importing its unexported helpers.
func sameOriginOK(c *gin.Context) bool {
	origin := c.GetHeader("Origin")
	if origin == "" {
		// Some browsers omit Origin on top-level navigations; fall back
		// to Referer host match.
		ref := c.GetHeader("Referer")
		return ref == "" || strings.Contains(ref, c.Request.Host)
	}
	return strings.Contains(origin, c.Request.Host)
}

func panelBaseURL(c *gin.Context) string {
	scheme := "https"
	if p := c.GetHeader("X-Forwarded-Proto"); p == "http" {
		scheme = "http"
	}
	return scheme + "://" + c.Request.Host
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

// ---- M46 Step 3: curated config tuner (ADR-0098) ----

type configParamOut struct {
	Name            string  `json:"name"`
	Kind            string  `json:"kind"`
	Min             float64 `json:"min"`
	Max             float64 `json:"max"`
	Unit            string  `json:"unit"`
	RestartRequired bool    `json:"restart_required"`
	Default         string  `json:"default"`
	Help            string  `json:"help"`
	Value           string  `json:"value"` // current persisted value, or default if unset
}

// getConfig returns the allowlist for an engine plus the currently
// persisted value (or default) for each key. No secrets involved.
func (h *databaseAdminOpsHandler) getConfig(c *gin.Context) {
	engine := c.Query("engine")
	if !dbEngineValid(engine) {
		c.JSON(http.StatusUnprocessableEntity, gin.H{"error": "invalid_engine"})
		return
	}
	rows, err := h.cfg.DBAdmin.ListTuning(c.Request.Context(), engine)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal"})
		return
	}
	persisted := make(map[string]string, len(rows))
	for _, r := range rows {
		persisted[r.Param] = r.Value
	}
	out := make([]configParamOut, 0)
	for _, p := range dbtuning.List(engine) {
		v := p.Default
		if pv, ok := persisted[p.Name]; ok {
			v = pv
		}
		out = append(out, configParamOut{
			Name: p.Name, Kind: string(p.Kind), Min: p.Min, Max: p.Max,
			Unit: p.Unit, RestartRequired: p.RestartRequired,
			Default: p.Default, Help: p.Help, Value: v,
		})
	}
	c.JSON(http.StatusOK, gin.H{"data": out, "total": len(out)})
}

type putConfigRequest struct {
	Engine   string            `json:"engine"`
	Settings map[string]string `json:"settings"`
}

// putConfig validates the desired set against the allowlist, persists
// it (DB = source of truth, ADR-0098), then dispatches the agent apply
// over the FULL desired set so the rendered file is complete.
func (h *databaseAdminOpsHandler) putConfig(c *gin.Context) {
	ctx := c.Request.Context()
	claims := ginctx.Claims(c)
	if claims == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}
	var req putConfigRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_request"})
		return
	}
	if !dbEngineValid(req.Engine) {
		c.JSON(http.StatusUnprocessableEntity, gin.H{"error": "invalid_engine"})
		return
	}
	if err := dbtuning.ValidateSet(req.Engine, req.Settings); err != nil {
		c.JSON(http.StatusUnprocessableEntity, gin.H{"error": "invalid_value", "detail": err.Error()})
		return
	}
	if h.cfg.Agent == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "agent_unavailable"})
		return
	}

	// Persist first (source of truth), then build the full desired
	// set from everything persisted for this engine.
	for k, v := range req.Settings {
		if err := h.cfg.DBAdmin.UpsertTuning(ctx, req.Engine, k, v); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal"})
			return
		}
	}
	rows, err := h.cfg.DBAdmin.ListTuning(ctx, req.Engine)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal"})
		return
	}
	desired := make(map[string]string, len(rows))
	for _, r := range rows {
		desired[r.Param] = r.Value
	}

	cmd := "db.config.apply"
	if req.Engine == "postgres" {
		cmd = "db.postgres.config.apply"
	}
	agentCtx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	_, aerr := h.cfg.Agent.Call(agentCtx, cmd, map[string]any{
		"settings":         desired,
		"restart_required": dbtuning.RestartRequired(req.Engine, desired),
	})
	if aerr != nil {
		if strings.Contains(aerr.Error(), "UNRECOVERABLE") {
			h.audit(ctx, claims.UserID, req.Engine, "config.apply", "", "error", "unrecoverable")
			h.publish(ctx, notifications.Envelope{
				EventKind: "db.admin.config_apply_failed_unrecoverable",
				Severity:  "critical",
				Title:     "DATABASE DOWN after config apply",
				Body:      req.Engine + " did not recover after a config change AND rollback. Manual intervention required (see /var/lib/jabali-agent/db-config-broken.json).",
				Deeplink:  "/jabali-admin/settings",
			})
			c.JSON(http.StatusBadGateway, gin.H{"error": "unrecoverable", "detail": aerr.Error()})
			return
		}
		h.audit(ctx, claims.UserID, req.Engine, "config.apply", "", "error", "agent rejected")
		h.publish(ctx, notifications.Envelope{
			EventKind: "db.admin.config_applied",
			Severity:  "warning",
			Title:     "Database config change rejected",
			Body:      "The " + req.Engine + " config change was rejected/rolled back: " + aerr.Error(),
			Deeplink:  "/jabali-admin/settings",
			UserID:    claims.UserID,
		})
		c.JSON(http.StatusBadGateway, gin.H{"error": "agent_failed", "detail": aerr.Error()})
		return
	}

	_ = h.cfg.DBAdmin.MarkTuningApplied(ctx, req.Engine, claims.UserID, time.Now().UTC())
	h.audit(ctx, claims.UserID, req.Engine, "config.apply", "", "ok", "")
	h.publish(ctx, notifications.Envelope{
		EventKind: "db.admin.config_applied",
		Severity:  "info",
		Title:     "Database config applied",
		Body:      "Applied " + req.Engine + " tuning from the admin panel.",
		Deeplink:  "/jabali-admin/settings",
		UserID:    claims.UserID,
	})
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

// ---- M46 Step 4: admin all-DBs phpMyAdmin / Adminer SSO (ADR-0099) ----
//
// Honest: these mint a handoff into a root-equivalent DB web shell.
// The control is the gating — RequireAdmin (group middleware) +
// same-origin + the single-use short-TTL token + scope=admin audit —
// NOT a trimmed grant. The token carries the ssoAdminAllSentinel as
// its DatabaseID; the validate handlers branch on that BEFORE the
// per-user shadow path, so per-user SSO is byte-unchanged.

type ssoRedirectResponse struct {
	RedirectURL string `json:"redirect_url"`
}

func (h *databaseAdminOpsHandler) ssoPhpMyAdminAdmin(c *gin.Context) {
	ctx := c.Request.Context()
	claims := ginctx.Claims(c)
	if claims == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}
	if !sameOriginOK(c) {
		h.audit(ctx, claims.UserID, "mariadb", "sso.admin", "phpmyadmin", "forbidden", "same_origin")
		c.JSON(http.StatusForbidden, gin.H{"error": "forbidden"})
		return
	}
	if h.cfg.Agent == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "agent_unavailable"})
		return
	}
	// Ensure the privileged shadow exists / rotate its password.
	actx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	if _, err := h.cfg.Agent.Call(actx, "db.pma_admin.ensure", map[string]any{}); err != nil {
		h.audit(ctx, claims.UserID, "mariadb", "sso.admin", "phpmyadmin", "error", "ensure failed")
		c.JSON(http.StatusBadGateway, gin.H{"error": "agent_failed"})
		return
	}
	token, err := h.cfg.SSO.MintToken(ctx, claims.UserID, ssoAdminAllSentinel, "")
	if err != nil {
		h.audit(ctx, claims.UserID, "mariadb", "sso.admin", "phpmyadmin", "error", "mint failed")
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal"})
		return
	}
	h.audit(ctx, claims.UserID, "mariadb", "sso.admin", "phpmyadmin", "ok", "scope=admin")
	c.JSON(http.StatusOK, ssoRedirectResponse{
		RedirectURL: panelBaseURL(c) + "/phpmyadmin/sso.php?token=" + token,
	})
}

func (h *databaseAdminOpsHandler) ssoAdminerAdmin(c *gin.Context) {
	ctx := c.Request.Context()
	claims := ginctx.Claims(c)
	if claims == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}
	if !sameOriginOK(c) {
		h.audit(ctx, claims.UserID, "postgres", "sso.admin", "adminer", "forbidden", "same_origin")
		c.JSON(http.StatusForbidden, gin.H{"error": "forbidden"})
		return
	}
	token, err := h.cfg.AdminerSSO.MintAdminerToken(ctx, claims.UserID, ssoAdminAllSentinel, "postgres")
	if err != nil {
		h.audit(ctx, claims.UserID, "postgres", "sso.admin", "adminer", "error", "mint failed")
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal"})
		return
	}
	h.audit(ctx, claims.UserID, "postgres", "sso.admin", "adminer", "ok", "scope=admin")
	c.JSON(http.StatusOK, ssoRedirectResponse{
		RedirectURL: panelBaseURL(c) + "/jabali-adminer/?token=" + token,
	})
}
