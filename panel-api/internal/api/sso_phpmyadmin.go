package api

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"log/slog"
	"net/http"
	"net/url"

	"github.com/gin-gonic/gin"

	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/ginctx"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/repository"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/sso"
)

// SSOPhpMyAdminHandlerConfig plugs the phpMyAdmin SSO handler into the router.
type SSOPhpMyAdminHandlerConfig struct {
	Databases repository.DatabaseRepository
	SSO       *sso.Service
	Log       *slog.Logger
}

// RegisterSSOPhpMyAdminRoutes mounts the POST /api/v1/sso/phpmyadmin endpoint.
func RegisterSSOPhpMyAdminRoutes(g *gin.RouterGroup, cfg SSOPhpMyAdminHandlerConfig) {
	h := &ssoPhpMyAdminHandler{cfg: cfg}
	g.POST("/sso/phpmyadmin", h.issueSSOToken)
}

type ssoPhpMyAdminHandler struct{ cfg SSOPhpMyAdminHandlerConfig }

type ssoPhpMyAdminRequest struct {
	DatabaseID string `json:"database_id" binding:"required"`
}

type ssoPhpMyAdminResponse struct {
	RedirectURL string `json:"redirect_url"`
}

// issueSSOToken handles POST /api/v1/sso/phpmyadmin.
// Auth: JWT. CSRF: same-origin check. Body: {"database_id":"<ulid>"}.
// Returns: {"redirect_url":"/phpmyadmin/sso.php?token=<base64url>&db=<name>"}.
func (h *ssoPhpMyAdminHandler) issueSSOToken(c *gin.Context) {
	ctx := c.Request.Context()
	claims := ginctx.Claims(c)
	if claims == nil {
		h.auditLog(ctx, "", "", "", "unauthorized")
		c.JSON(http.StatusForbidden, gin.H{"error": "forbidden"})
		return
	}

	// CSRF: same-origin check via Origin/Referer headers
	if !h.validateSameOrigin(c) {
		h.auditLog(ctx, claims.UserID, "", "", "unauthorized")
		c.JSON(http.StatusForbidden, gin.H{"error": "forbidden"})
		return
	}

	var req ssoPhpMyAdminRequest
	if err := c.BindJSON(&req); err != nil {
		h.auditLog(ctx, claims.UserID, "", "", "unauthorized")
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_request"})
		return
	}

	// Verify ownership: database.user_id == JWT sub
	db, err := h.cfg.Databases.FindByID(ctx, req.DatabaseID)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			h.auditLog(ctx, claims.UserID, req.DatabaseID, "", "unauthorized")
			c.JSON(http.StatusNotFound, gin.H{"error": "not_found"})
			return
		}
		h.cfg.Log.ErrorContext(ctx, "database lookup failed", "err", err)
		h.auditLog(ctx, claims.UserID, req.DatabaseID, "", "unauthorized")
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal"})
		return
	}

	if db.UserID != claims.UserID {
		h.auditLog(ctx, claims.UserID, req.DatabaseID, "", "unauthorized")
		c.JSON(http.StatusForbidden, gin.H{"error": "forbidden"})
		return
	}

	// Ensure shadow account and get credentials
	if err := h.cfg.SSO.EnsureShadow(ctx, claims.UserID); err != nil {
		h.cfg.Log.ErrorContext(ctx, "ensure shadow account failed", "err", err)
		h.auditLog(ctx, claims.UserID, req.DatabaseID, "", "unauthorized")
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal"})
		return
	}

	// Mint SSO token
	token, err := h.cfg.SSO.MintToken(ctx, claims.UserID, req.DatabaseID, db.Name)
	if err != nil {
		h.cfg.Log.ErrorContext(ctx, "mint token failed", "err", err)
		h.auditLog(ctx, claims.UserID, req.DatabaseID, "", "unauthorized")
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal"})
		return
	}

	// Log successful issue with token hash prefix
	tokenHash := sha256.Sum256([]byte(token))
	hashPrefix := hex.EncodeToString(tokenHash[:8])
	h.auditLog(ctx, claims.UserID, req.DatabaseID, hashPrefix, "issued")

	// Build redirect URL
	query := url.Values{}
	query.Set("token", token)
	query.Set("db", db.Name)
	redirectURL := "/phpmyadmin/sso.php?" + query.Encode()

	c.JSON(http.StatusOK, ssoPhpMyAdminResponse{RedirectURL: redirectURL})
}

// auditLog emits a structured slog line for SSO operations.
func (h *ssoPhpMyAdminHandler) auditLog(ctx context.Context, userID, databaseID, tokenHashPrefix, outcome string) {
	h.cfg.Log.DebugContext(ctx, "sso_phpmyadmin",
		"user_id", userID,
		"database_id", databaseID,
		"token_hash_prefix", tokenHashPrefix,
		"outcome", outcome,
	)
}

// validateSameOrigin checks that Origin or Referer header matches the request host.
// Rejects cross-origin state-changing requests.
func (h *ssoPhpMyAdminHandler) validateSameOrigin(c *gin.Context) bool {
	origin := c.GetHeader("Origin")
	referer := c.GetHeader("Referer")

	// If Origin header is present (sent by browsers on state-changing requests),
	// verify it matches the target host.
	if origin != "" {
		return origin == h.getOrigin(c)
	}

	// Fallback to Referer header.
	if referer != "" {
		return h.refererMatchesHost(c, referer)
	}

	// No origin/referer headers. Conservative: reject.
	// (POST from old browsers or curl without headers is less critical than
	// blocking cross-origin attacks.)
	return false
}

func (h *ssoPhpMyAdminHandler) getOrigin(c *gin.Context) string {
	scheme := "https"
	if c.Request.TLS == nil {
		scheme = "http"
	}
	return scheme + "://" + c.Request.Host
}

func (h *ssoPhpMyAdminHandler) refererMatchesHost(c *gin.Context, referer string) bool {
	u, err := url.Parse(referer)
	if err != nil {
		return false
	}
	return u.Host == c.Request.Host
}
