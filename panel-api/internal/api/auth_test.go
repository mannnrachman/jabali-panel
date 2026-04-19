package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/api"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/auth"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/models"
)

// fakeAuthService implements the small interface api.AuthHandler depends on.
type fakeAuthService struct {
	loginOut    *auth.LoginOutput
	loginErr    error
	refreshOut  *auth.LoginOutput
	refreshErr  error
	logoutErr   error
	lastDevice  string
	lastRefresh string
}

func (f *fakeAuthService) Login(_ context.Context, in auth.LoginInput) (*auth.LoginOutput, error) {
	f.lastDevice = in.DeviceID
	return f.loginOut, f.loginErr
}
func (f *fakeAuthService) Refresh(_ context.Context, in auth.RefreshInput) (*auth.LoginOutput, error) {
	f.lastRefresh = in.RawRefresh
	f.lastDevice = in.DeviceID
	return f.refreshOut, f.refreshErr
}
func (f *fakeAuthService) Logout(_ context.Context, raw string) error {
	f.lastRefresh = raw
	return f.logoutErr
}

func (f *fakeAuthService) RedeemCLIToken(_ context.Context, cliToken, deviceID string) (*auth.LoginOutput, error) {
	f.lastRefresh = cliToken
	f.lastDevice = deviceID
	return f.refreshOut, f.refreshErr
}

func (f *fakeAuthService) GenerateImpersonationLoginURL(_ context.Context, _ *models.User, _ string, scheme string, hostname string, port string) (string, error) {
	return scheme + "://" + hostname + ":" + port + "/login?cli_token=test-token", nil
}

func (f *fakeAuthService) ChallengeTOTP(_ context.Context, _ auth.ChallengeTOTPInput) (*auth.LoginOutput, error) {
	return f.loginOut, f.loginErr
}







func newAuthRouter(t *testing.T, svc *fakeAuthService) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	api.RegisterAuthRoutes(r, api.AuthHandlerConfig{
		Service:            svc,
		AccessTTL:          15 * time.Minute,
		RefreshTTL:         24 * time.Hour,
		CookieName:         "jabali_refresh",
		CookieSecure:       false, // plain http in tests
		CookieSameSiteNone: false,
	})
	return r
}

func TestLogin_Success(t *testing.T) {
	t.Parallel()
	svc := &fakeAuthService{
		loginOut: &auth.LoginOutput{
			AccessToken: "jwt-fake",
			RawRefresh:  "refresh-raw",
			User:        nil,
		},
	}
	r := newAuthRouter(t, svc)

	body, _ := json.Marshal(map[string]string{"email": "alice@example.com", "password": "p"})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Device-Id", "dev-42")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code, "body=%s", rec.Body.String())
	assert.Equal(t, "dev-42", svc.lastDevice)

	var got map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	assert.Equal(t, "jwt-fake", got["access_token"])

	// Refresh should NOT appear in the body.
	_, inBody := got["refresh_token"]
	assert.False(t, inBody, "refresh token must not leak into JSON body")

	// Set-Cookie with HttpOnly must carry the raw refresh.
	cookies := rec.Result().Cookies()
	var rc *http.Cookie
	for _, c := range cookies {
		if c.Name == "jabali_refresh" {
			rc = c
		}
	}
	require.NotNil(t, rc, "refresh cookie must be set")
	assert.Equal(t, "refresh-raw", rc.Value)
	assert.True(t, rc.HttpOnly)
	assert.Equal(t, "/", rc.Path)
	assert.Equal(t, http.SameSiteStrictMode, rc.SameSite)
}

func TestLogin_InvalidCredentialsReturns401(t *testing.T) {
	t.Parallel()
	svc := &fakeAuthService{loginErr: auth.ErrInvalidCredentials}
	r := newAuthRouter(t, svc)

	body, _ := json.Marshal(map[string]string{"email": "alice@example.com", "password": "bad"})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	require.Equal(t, http.StatusUnauthorized, rec.Code)
	var got map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	assert.Equal(t, "invalid_credentials", got["error"])
}

func TestLogin_RejectsMalformedBody(t *testing.T) {
	t.Parallel()
	svc := &fakeAuthService{}
	r := newAuthRouter(t, svc)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", bytes.NewBufferString("{"))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestRefresh_ReadsCookie_RotatesTokens(t *testing.T) {
	t.Parallel()
	svc := &fakeAuthService{
		refreshOut: &auth.LoginOutput{AccessToken: "jwt-v2", RawRefresh: "refresh-v2"},
	}
	r := newAuthRouter(t, svc)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/refresh", nil)
	req.AddCookie(&http.Cookie{Name: "jabali_refresh", Value: "refresh-v1"})
	req.Header.Set("X-Device-Id", "dev-7")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code, "body=%s", rec.Body.String())
	assert.Equal(t, "refresh-v1", svc.lastRefresh)
	assert.Equal(t, "dev-7", svc.lastDevice)

	var got map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	assert.Equal(t, "jwt-v2", got["access_token"])

	// Verify the new refresh cookie is set and replaces the old one.
	var rc *http.Cookie
	for _, c := range rec.Result().Cookies() {
		if c.Name == "jabali_refresh" {
			rc = c
		}
	}
	require.NotNil(t, rc)
	assert.Equal(t, "refresh-v2", rc.Value)
}

func TestRefresh_MissingCookieReturns401(t *testing.T) {
	t.Parallel()
	svc := &fakeAuthService{}
	r := newAuthRouter(t, svc)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/refresh", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	require.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestLogout_ClearsCookie(t *testing.T) {
	t.Parallel()
	svc := &fakeAuthService{}
	r := newAuthRouter(t, svc)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/logout", nil)
	req.AddCookie(&http.Cookie{Name: "jabali_refresh", Value: "some-token"})
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "some-token", svc.lastRefresh)

	// Logout must send Set-Cookie with MaxAge<=0 (or past Expires) to clear.
	var rc *http.Cookie
	for _, c := range rec.Result().Cookies() {
		if c.Name == "jabali_refresh" {
			rc = c
		}
	}
	require.NotNil(t, rc, "logout must set a clearing cookie")
	assert.Equal(t, "", rc.Value)
	assert.LessOrEqual(t, rc.MaxAge, 0)
}
