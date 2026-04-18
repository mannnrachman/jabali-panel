package api

import (
	"crypto/sha256"
	"encoding/hex"
	"log/slog"
	"net/http"
	"net/url"
	"strings"

	"github.com/gin-gonic/gin"

	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/ginctx"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/sso"
)

// SSOFileBrowserHandlerConfig plugs the filebrowser SSO handler into the router.
type SSOFileBrowserHandlerConfig struct {
	SSO *sso.Service
	Log *slog.Logger
}

// RegisterSSOFileBrowserRoutes mounts the POST /api/v1/sso/filebrowser endpoint.
func RegisterSSOFileBrowserRoutes(g *gin.RouterGroup, cfg SSOFileBrowserHandlerConfig) {
	h := &ssoFileBrowserHandler{cfg: cfg}
	g.POST("/sso/filebrowser", h.issueSSOToken)
}

type ssoFileBrowserHandler struct{ cfg SSOFileBrowserHandlerConfig }

type ssoFileBrowserResponse struct {
	RedirectURL string `json:"redirect_url"`
}

// issueSSOToken handles POST /api/v1/sso/filebrowser.
// Auth: JWT. CSRF: same-origin check. Body: empty (uses JWT claims for user_id).
// Returns: {\"redirect_url\":\"<absolute-url>/files/?token=<base64url>\"}.
//
// CONTRACT FOR STEP 5 (nginx reverse proxy):
//   - Token comes via query param: ?token=<base64url>
//   - Nginx subrequest to validator endpoint: POST /sso/filebrowser/validate
//   - Validator request body: JSON {\"token\": \"...\"}
//   - Validator response on 200: JSON {\"user\": \"<linux-username>\"}
//   - Nginx injects: X-Forwarded-User: <linux-username>
func (h *ssoFileBrowserHandler) issueSSOToken(c *gin.Context) {
	ctx := c.Request.Context()
	claims := ginctx.Claims(c)
	if claims == nil {
		c.JSON(http.StatusForbidden, gin.H{"error": "forbidden"})
		return
	}

	// CSRF: same-origin check via Origin/Referer headers
	if !h.validateSameOrigin(c) {
		c.JSON(http.StatusForbidden, gin.H{"error": "forbidden"})
		return
	}

	// Mint SSO token
	token, expiresAt, err := h.cfg.SSO.MintFileBrowserToken(ctx, claims.UserID)
	if err != nil {
		h.cfg.Log.ErrorContext(ctx, "mint filebrowser token failed", "user_id", claims.UserID, "err", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal"})
		return
	}

	// Log successful issue with token hash prefix
	tokenHash := sha256.Sum256([]byte(token))
	hashPrefix := hex.EncodeToString(tokenHash[:8])
	h.cfg.Log.DebugContext(ctx, "sso_filebrowser",
		"user_id", claims.UserID,
		"token_hash_prefix", hashPrefix,
		"expires_at", expiresAt,
		"outcome", "issued",
	)

	// Build redirect URL with absolute base
	baseURL := h.getFileBrowserBaseURL(c)
	redirectURL := baseURL + "/files/?token=" + token

	c.JSON(http.StatusOK, ssoFileBrowserResponse{RedirectURL: redirectURL})
}

// getFileBrowserBaseURL derives the base URL for filebrowser redirects.
// Derives from request Host header: strip port, always use https.
func (h *ssoFileBrowserHandler) getFileBrowserBaseURL(c *gin.Context) string {
	// Derive from request Host header: strip port, always use https.
	// The panel is https-only.
	host := c.Request.Host
	if idx := strings.LastIndex(host, ":"); idx != -1 {
		host = host[:idx]
	}
	return "https://" + host
}

// validateSameOrigin checks that Origin or Referer header matches the request host.
// Rejects cross-origin state-changing requests.
func (h *ssoFileBrowserHandler) validateSameOrigin(c *gin.Context) bool {
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
	return false
}

func (h *ssoFileBrowserHandler) getOrigin(c *gin.Context) string {
	scheme := "https"
	if c.Request.TLS == nil {
		scheme = "http"
	}
	return scheme + "://" + c.Request.Host
}

func (h *ssoFileBrowserHandler) refererMatchesHost(c *gin.Context, referer string) bool {
	parsedURL, err := url.Parse(referer)
	if err != nil {
		return false
	}
	return parsedURL.Host == c.Request.Host
}
