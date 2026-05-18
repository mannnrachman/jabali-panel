// Admin Automation API token management (M44).
//
// Endpoints under /api/v1/admin/automation/tokens:
//   POST          mint a new token (returns plaintext secret ONCE)
//   GET           list tokens (no secrets in response)
//   DELETE /:id   revoke a token (soft delete via revoked_at)
//
// Mint flow: admin posts {name, scopes}; server generates a 32-byte
// secret, encrypts via ssokey, persists, returns the plaintext secret
// in the response body. The admin UI shows the plaintext in a
// one-time-reveal modal; subsequent reads of the token list omit it.
package api

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/audit"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/ginctx"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/ids"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/middleware"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/models"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/repository"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/ssokey"
)

// AdminAutomationTokensConfig wires the admin endpoints.
type AdminAutomationTokensConfig struct {
	Repo repository.AutomationTokenRepository
	Key  *ssokey.Key
	// Audit is the M49 unified audit recorder (ADR-0106). Optional —
	// nil disables emission (the recorder itself is fire-and-forget).
	Audit audit.Recorder
}

// RegisterAdminAutomationTokens mounts the admin endpoints behind
// RequireAdmin. The middleware ensures only authenticated admins can
// mint / list / revoke automation tokens.
func RegisterAdminAutomationTokens(rg *gin.RouterGroup, cfg AdminAutomationTokensConfig) {
	if cfg.Repo == nil || cfg.Key == nil {
		return
	}
	g := rg.Group("/admin/automation/tokens", middleware.RequireAdmin())
	h := &adminAutoTokensHandler{cfg: cfg}
	g.POST("", h.create)
	g.GET("", h.list)
	g.DELETE("/:id", h.revoke)
}

type adminAutoTokensHandler struct{ cfg AdminAutomationTokensConfig }

type createAutoTokenRequest struct {
	Name   string   `json:"name" binding:"required"`
	Scopes []string `json:"scopes" binding:"required"`
}

func (h *adminAutoTokensHandler) create(c *gin.Context) {
	var req createAutoTokenRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusUnprocessableEntity, gin.H{"error": "validation_failed", "detail": err.Error()})
		return
	}
	if len(req.Name) > 100 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "name must be ≤ 100 chars"})
		return
	}
	if len(req.Scopes) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "at least one scope required"})
		return
	}
	for _, s := range req.Scopes {
		if !isAllowedAutoScope(s) {
			c.JSON(http.StatusBadRequest, gin.H{"error": "scope not allowed: " + s})
			return
		}
	}

	// Generate 32 bytes of secret entropy + hex-encode for the
	// returned plaintext. Hex (not base64) keeps the value
	// shell-safe in curl + bash without %-encoding.
	secretRaw := make([]byte, 32)
	if _, err := rand.Read(secretRaw); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal"})
		return
	}
	plaintext := hex.EncodeToString(secretRaw)

	encBytes, err := h.cfg.Key.Seal([]byte(plaintext))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal"})
		return
	}

	claims := ginctx.Claims(c)
	var creator *string
	if claims != nil && claims.UserID != "" {
		uid := claims.UserID
		creator = &uid
	}

	tok := &models.AutomationToken{
		ID:        ids.NewULID(),
		Name:      req.Name,
		Scopes:    models.AutomationScopes(req.Scopes),
		SecretEnc: encBytes,
		CreatedBy: creator,
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()
	if err := h.cfg.Repo.Create(ctx, tok); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "create token: " + err.Error()})
		return
	}
	if h.cfg.Audit != nil {
		actor := ""
		if creator != nil {
			actor = *creator
		}
		h.cfg.Audit.Record(audit.TokenMint(actor, tok.ID, tok.Name, []string(tok.Scopes), c.ClientIP(), ginctx.RequestID(c)))
	}
	c.JSON(http.StatusCreated, gin.H{
		"id":         tok.ID,
		"name":       tok.Name,
		"scopes":     tok.Scopes,
		"created_at": tok.CreatedAt,
		// Plaintext returned ONCE — UI must show in a one-time-reveal
		// modal and never persist client-side.
		"secret": plaintext,
	})
}

func (h *adminAutoTokensHandler) list(c *gin.Context) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()
	rows, err := h.cfg.Repo.List(ctx)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "list: " + err.Error()})
		return
	}
	// AutomationToken's `secret_enc` field has json:"-" so it won't
	// leak; respond with the plain rows.
	c.JSON(http.StatusOK, gin.H{"data": rows, "total": len(rows)})
}

func (h *adminAutoTokensHandler) revoke(c *gin.Context) {
	id := c.Param("id")
	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()
	if err := h.cfg.Repo.Revoke(ctx, id); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "revoke: " + err.Error()})
		return
	}
	if h.cfg.Audit != nil {
		actor := ""
		if cl := ginctx.Claims(c); cl != nil {
			actor = cl.UserID
		}
		h.cfg.Audit.Record(audit.TokenRevoke(actor, id, c.ClientIP(), ginctx.RequestID(c)))
	}
	c.JSON(http.StatusOK, gin.H{"id": id, "revoked": true})
}

// isAllowedAutoScope filters mint requests to known scopes. Reject
// anything else so a typo doesn't slip in and silently fail
// authorization checks. write:* is explicitly denied at mint time —
// no write routes exist yet (per plans/automation-api-tokens.md).
func isAllowedAutoScope(s string) bool {
	switch s {
	case "read:*",
		"read:domains",
		"read:users",
		"read:applications",
		"read:status":
		return true
	}
	return false
}
