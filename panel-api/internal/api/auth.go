package api

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/auth"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/models"
)

// AuthService is the subset of auth.Service that the HTTP layer needs. An
// interface here keeps the handler testable with fakes without pulling in
// the whole concrete service.
type AuthService interface {
	Login(ctx context.Context, in auth.LoginInput) (*auth.LoginOutput, error)
	Refresh(ctx context.Context, in auth.RefreshInput) (*auth.LoginOutput, error)
	Logout(ctx context.Context, raw string) error
	RedeemCLIToken(ctx context.Context, cliToken string, deviceID string) (*auth.LoginOutput, error)
	GenerateImpersonationLoginURL(ctx context.Context, targetUser *models.User, adminID string, scheme string, hostname string, port string) (string, error)
	ChallengeTOTP(ctx context.Context, in auth.ChallengeTOTPInput) (*auth.LoginOutput, error)
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

	// StrictRateLimit, if non-nil, is applied to /login and /logout only
	// (credential endpoints where brute-force throttling matters). /refresh
	// is gated by the HttpOnly cookie and rides the global default limiter.
	StrictRateLimit gin.HandlerFunc
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

	// Strict rate limit (credential brute-force protection) applies to
	// login + logout only. /refresh is gated by the HttpOnly cookie
	// and is hit on every page load by normal browsing, so it rides
	// the global default limiter instead — throttling it here caused
	// users to get logged out when they double-reloaded quickly.
	loginChain := []gin.HandlerFunc{}
	logoutChain := []gin.HandlerFunc{}
	if cfg.StrictRateLimit != nil {
		loginChain = append(loginChain, cfg.StrictRateLimit)
		logoutChain = append(logoutChain, cfg.StrictRateLimit)
	}
	g.POST("/login", append(loginChain, h.login)...)
	g.POST("/refresh", h.refresh)
	g.POST("/logout", append(logoutChain, h.logout)...)
	g.POST("/cli-login", h.cliLogin)
	// 2FA challenge rides the strict rate limit because it's a credential
	// endpoint — unbounded brute-force would burn through the TOTP 30-sec
	// window or the 10 backup codes.
	g.POST("/2fa/challenge", append(loginChain, h.twofaChallenge)...)
}

type authHandler struct{ cfg AuthHandlerConfig }

// loginRequest mirrors the public JSON shape expected from the SPA.
type loginRequest struct {
	Email    string `json:"email"    binding:"required,email"`
	Password string `json:"password" binding:"required,min=1"`
}

// loginResponse never includes the refresh token — that lives in the cookie.
// When 2FA is pending, AccessToken / ExpiresIn are zero-value and the client
// reads TwoFAPending + PendingToken to continue via /auth/2fa/challenge.
type loginResponse struct {
	AccessToken  string        `json:"access_token,omitempty"`
	TokenType    string        `json:"token_type,omitempty"`
	ExpiresIn    int64         `json:"expires_in,omitempty"`
	User         *userResponse `json:"user,omitempty"`
	TwoFAPending bool          `json:"twofa_pending,omitempty"`
	PendingToken string        `json:"twofa_pending_token,omitempty"`
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
	// 2FA-pending: DO NOT set refresh cookie — the second leg (/auth/2fa/challenge)
	// will mint the full pair after the code verifies.
	if out.TwoFAPending {
		c.JSON(http.StatusOK, h.buildLoginResponse(out))
		return
	}
	h.setRefreshCookie(c, out.RawRefresh)
	c.JSON(http.StatusOK, h.buildLoginResponse(out))
}

// twofaChallengeRequest is the body for POST /auth/2fa/challenge. Client
// sends exactly one of `code` (6 digits from authenticator app) or
// `backup_code` (8-digit one-time recovery code).
type twofaChallengeRequest struct {
	PendingToken string `json:"twofa_pending_token" binding:"required"`
	Code         string `json:"code,omitempty"`
	BackupCode   string `json:"backup_code,omitempty"`
}

func (h *authHandler) twofaChallenge(c *gin.Context) {
	var req twofaChallengeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_request", "detail": err.Error()})
		return
	}
	code := req.Code
	if code == "" {
		code = req.BackupCode
	}
	if code == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "code_required"})
		return
	}
	deviceID := auth.DeriveDeviceID(
		c.GetHeader("X-Device-Id"),
		c.Request.UserAgent(),
		c.ClientIP(),
	)
	out, err := h.cfg.Service.ChallengeTOTP(c.Request.Context(), auth.ChallengeTOTPInput{
		PendingToken: req.PendingToken,
		Code:         code,
		DeviceID:     deviceID,
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
	case errors.Is(err, auth.ErrInvalid2FACode):
		// Deliberately indistinguishable from invalid_token below — the
		// client already knows it was in the 2FA leg, and we don't want
		// to leak whether the token or the code was wrong.
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid_2fa"})
	case errors.Is(err, auth.ErrInvalidToken):
		h.clearRefreshCookie(c)
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid_token"})
	default:
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal"})
	}
}

func (h *authHandler) buildLoginResponse(out *auth.LoginOutput) loginResponse {
	resp := loginResponse{}
	if out.User != nil {
		resp.User = &userResponse{
			ID: out.User.ID, Email: out.User.Email, IsAdmin: out.User.IsAdmin,
		}
	}
	if out.TwoFAPending {
		resp.TwoFAPending = true
		resp.PendingToken = out.PendingToken
		return resp
	}
	resp.AccessToken = out.AccessToken
	resp.TokenType = "Bearer"
	resp.ExpiresIn = int64(h.cfg.AccessTTL.Seconds())
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
