package api

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/auth"
)

// AuthService is the subset of auth.Service that the HTTP layer needs. An
// interface here keeps the handler testable with fakes without pulling in
// the whole concrete service.
type AuthService interface {
	Login(ctx context.Context, in auth.LoginInput) (*auth.LoginOutput, error)
	Refresh(ctx context.Context, in auth.RefreshInput) (*auth.LoginOutput, error)
	Logout(ctx context.Context, raw string) error
}

// AuthHandlerConfig captures everything the handler needs to emit cookies
// correctly. RefreshTTL bounds the cookie Max-Age (and DB row lifetime);
// AccessTTL is reported to the client as expires_in per OAuth2 semantics.
type AuthHandlerConfig struct {
	Service    AuthService
	AccessTTL  time.Duration
	RefreshTTL time.Duration

	// CookieName: the refresh cookie name. Default "jabali_refresh".
	CookieName string
	// CookieSecure marks the cookie Secure. Set true in production (HTTPS).
	CookieSecure bool
	// CookieSameSiteNone relaxes SameSite from Strict to None — required if
	// the SPA is served from a different origin than the API. Default false
	// (Strict).
	CookieSameSiteNone bool
}

// DefaultRefreshCookieName is used when AuthHandlerConfig.CookieName is blank.
const DefaultRefreshCookieName = "jabali_refresh"

// RegisterAuthRoutes mounts POST /api/v1/auth/{login,refresh,logout} on r.
func RegisterAuthRoutes(r *gin.Engine, cfg AuthHandlerConfig) {
	if cfg.CookieName == "" {
		cfg.CookieName = DefaultRefreshCookieName
	}
	h := &authHandler{cfg: cfg}
	g := r.Group("/api/v1/auth")
	g.POST("/login", h.login)
	g.POST("/refresh", h.refresh)
	g.POST("/logout", h.logout)
}

type authHandler struct{ cfg AuthHandlerConfig }

// loginRequest mirrors the public JSON shape expected from the SPA.
type loginRequest struct {
	Email    string `json:"email"    binding:"required,email"`
	Password string `json:"password" binding:"required,min=1"`
}

// loginResponse never includes the refresh token — that lives in the cookie.
type loginResponse struct {
	AccessToken string        `json:"access_token"`
	TokenType   string        `json:"token_type"`
	ExpiresIn   int64         `json:"expires_in"`
	User        *userResponse `json:"user,omitempty"`
}

// userResponse is a minimal safe view of models.User for auth payloads.
// Fuller shapes live in the users handler (Phase 6).
type userResponse struct {
	ID      string `json:"id"`
	Email   string `json:"email"`
	IsAdmin bool   `json:"is_admin"`
}

func (h *authHandler) login(c *gin.Context) {
	var req loginRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_request", "detail": err.Error()})
		return
	}
	deviceID := auth.DeriveDeviceID(
		c.GetHeader("X-Device-Id"),
		c.Request.UserAgent(),
		c.ClientIP(),
	)

	out, err := h.cfg.Service.Login(c.Request.Context(), auth.LoginInput{
		Email: req.Email, Password: req.Password, DeviceID: deviceID,
	})
	if err != nil {
		h.handleAuthErr(c, err)
		return
	}
	h.setRefreshCookie(c, out.RawRefresh)
	c.JSON(http.StatusOK, h.buildLoginResponse(out))
}

func (h *authHandler) refresh(c *gin.Context) {
	raw, err := c.Cookie(h.cfg.CookieName)
	if err != nil || raw == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "missing_refresh_cookie"})
		return
	}
	deviceID := auth.DeriveDeviceID(
		c.GetHeader("X-Device-Id"),
		c.Request.UserAgent(),
		c.ClientIP(),
	)

	out, err := h.cfg.Service.Refresh(c.Request.Context(), auth.RefreshInput{
		RawRefresh: raw, DeviceID: deviceID,
	})
	if err != nil {
		h.handleAuthErr(c, err)
		return
	}
	h.setRefreshCookie(c, out.RawRefresh)
	c.JSON(http.StatusOK, h.buildLoginResponse(out))
}

func (h *authHandler) logout(c *gin.Context) {
	raw, _ := c.Cookie(h.cfg.CookieName)
	// Logout is best-effort even without a cookie — always clear + 200.
	if raw != "" {
		_ = h.cfg.Service.Logout(c.Request.Context(), raw)
	}
	h.clearRefreshCookie(c)
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

// ---------- helpers ----------

func (h *authHandler) handleAuthErr(c *gin.Context, err error) {
	switch {
	case errors.Is(err, auth.ErrInvalidCredentials):
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid_credentials"})
	case errors.Is(err, auth.ErrInvalidToken):
		h.clearRefreshCookie(c)
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid_token"})
	default:
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal"})
	}
}

func (h *authHandler) buildLoginResponse(out *auth.LoginOutput) loginResponse {
	resp := loginResponse{
		AccessToken: out.AccessToken,
		TokenType:   "Bearer",
		ExpiresIn:   int64(h.cfg.AccessTTL.Seconds()),
	}
	if out.User != nil {
		resp.User = &userResponse{
			ID: out.User.ID, Email: out.User.Email, IsAdmin: out.User.IsAdmin,
		}
	}
	return resp
}

func (h *authHandler) setRefreshCookie(c *gin.Context, raw string) {
	http.SetCookie(c.Writer, &http.Cookie{
		Name:     h.cfg.CookieName,
		Value:    raw,
		Path:     "/",
		MaxAge:   int(h.cfg.RefreshTTL.Seconds()),
		HttpOnly: true,
		Secure:   h.cfg.CookieSecure,
		SameSite: h.sameSite(),
	})
}

func (h *authHandler) clearRefreshCookie(c *gin.Context) {
	http.SetCookie(c.Writer, &http.Cookie{
		Name:     h.cfg.CookieName,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   h.cfg.CookieSecure,
		SameSite: h.sameSite(),
	})
}

func (h *authHandler) sameSite() http.SameSite {
	if h.cfg.CookieSameSiteNone {
		return http.SameSiteNoneMode
	}
	return http.SameSiteStrictMode
}
