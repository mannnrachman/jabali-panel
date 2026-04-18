package api

import (
	"encoding/base64"
	"log/slog"
	"net/http"

	"github.com/gin-gonic/gin"

	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/sso"
)

// SSOFileBrowserValidateHandlerConfig plugs the UDS validate handler into the router.
type SSOFileBrowserValidateHandlerConfig struct {
	SSO *sso.Service
	Log *slog.Logger
}

// RegisterSSOFileBrowserValidateRoutes mounts POST /sso/filebrowser/validate
// on the given router (typically a UDS listener, no JWT auth).
func RegisterSSOFileBrowserValidateRoutes(g *gin.RouterGroup, cfg SSOFileBrowserValidateHandlerConfig) {
	h := &ssoFileBrowserValidateHandler{cfg: cfg}
	g.POST("/sso/filebrowser/validate", h.validate)
}

type ssoFileBrowserValidateHandler struct {
	cfg SSOFileBrowserValidateHandlerConfig
}

type ssoValidateFileBrowserRequest struct {
	Token string `json:"token" binding:"required"`
}

type ssoValidateFileBrowserResponse struct {
	User string `json:"user"`
}

// ssoErrorResponse is already declared in sso_phpmyadmin_validate.go.

// validate handles POST /sso/filebrowser/validate.
// No JWT auth; Unix socket ACL is the boundary.
// Body: {\"token\":\"<base64url>\"}
// Returns 200 with {\"user\":\"<linux-username>\"}, 401 for invalid/expired/used/unknown token.
func (h *ssoFileBrowserValidateHandler) validate(c *gin.Context) {
	ctx := c.Request.Context()

	var req ssoValidateFileBrowserRequest
	// Body is primary, but nginx auth_request subrequests can't carry a
	// JSON body reliably across versions (proxy_set_body + auth_request is
	// fragile). Accept ?token=… query fallback so the nginx config can
	// just pass the token in the URL when body forwarding fails.
	if err := c.ShouldBindJSON(&req); err != nil || req.Token == "" {
		if qt := c.Query("token"); qt != "" {
			req.Token = qt
		} else {
			c.JSON(http.StatusBadRequest, ssoErrorResponse{Error: "invalid_request"})
			return
		}
	}

	// Decode base64url token to raw bytes to verify encoding is valid
	_, err := base64.RawURLEncoding.DecodeString(req.Token)
	if err != nil {
		h.cfg.Log.DebugContext(ctx, "sso_filebrowser_validate",
			"outcome", "invalid_token_encoding",
		)
		c.JSON(http.StatusUnauthorized, ssoErrorResponse{Error: "invalid"})
		return
	}

	// Validate and consume token
	username, err := h.cfg.SSO.ValidateFileBrowserToken(ctx, req.Token)
	if err != nil {
		// Map all errors to 401 Unauthorized (expired, used, not found, etc.)
		h.cfg.Log.DebugContext(ctx, "sso_filebrowser_validate",
			"error", err,
			"outcome", "unauthorized",
		)
		c.JSON(http.StatusUnauthorized, ssoErrorResponse{Error: "invalid"})
		return
	}

	// Log successful validation
	h.cfg.Log.DebugContext(ctx, "sso_filebrowser_validate",
		"username", username,
		"outcome", "validated",
	)

	// Set X-Auth-User header for nginx auth_request subrequests to consume.
	// nginx's auth_request_set can only read response headers, not body JSON,
	// so we expose the validated username as a header for nginx's proxy use.
	c.Header("X-Auth-User", username)
	c.JSON(http.StatusOK, ssoValidateFileBrowserResponse{User: username})
}
