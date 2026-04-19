package app_test

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/app"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/auth"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/config"
)

func init() {
	gin.SetMode(gin.TestMode)
}

func TestNew_ReturnsEngineWithHealth(t *testing.T) {
	t.Parallel()

	r := app.New()
	require.NotNil(t, r)

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
}

func TestNew_HasMethodNotAllowedEnabled(t *testing.T) {
	t.Parallel()

	r := app.New()

	req := httptest.NewRequest(http.MethodPost, "/health", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusMethodNotAllowed, rec.Code)
}

func TestNewWith_ProductionSetsReleaseMode(t *testing.T) {
	// Not t.Parallel: gin.SetMode mutates a package global.
	cfg := config.Defaults()
	cfg.Server.Env = config.EnvProduction

	_ = app.NewWithDeps(cfg, app.Deps{})
	assert.Equal(t, gin.ReleaseMode, gin.Mode())

	// Put it back so other tests see a consistent default.
	gin.SetMode(gin.TestMode)
}

func TestNewWith_DevelopmentSetsDebugMode(t *testing.T) {
	// Not t.Parallel: same reason as above.
	cfg := config.Defaults()
	cfg.Server.Env = config.EnvDevelopment

	_ = app.NewWithDeps(cfg, app.Deps{})
	assert.Equal(t, gin.DebugMode, gin.Mode())

	gin.SetMode(gin.TestMode)
}

func TestNewWithDeps_ProtectedRouteRequiresAuth(t *testing.T) {
	t.Parallel()

	// A JWTIssuer is enough to mount /api/v1/me behind RequireAuth; no
	// AuthService needed for this probe — we're only checking the gate.
	iss, err := auth.NewJWTIssuer(auth.JWTConfig{
		Secret:    []byte("integration-test-secret-xxxxxxxxxx"),
		Issuer:    "jabali-panel-test",
		KeyID:     "v1",
		AccessTTL: time.Minute,
	})
	require.NoError(t, err)

	r := app.NewWithDeps(config.Defaults(), app.Deps{JWTIssuer: iss})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/me", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestNewWithDeps_LegacyAuthProviderUsesJWT(t *testing.T) {
	t.Parallel()

	// When provider="legacy" (the default), /api/v1/* must use RequireAuth (JWT).
	iss, err := auth.NewJWTIssuer(auth.JWTConfig{
		Secret:    []byte("integration-test-secret-xxxxxxxxxx"),
		Issuer:    "jabali-panel-test",
		KeyID:     "v1",
		AccessTTL: time.Minute,
	})
	require.NoError(t, err)

	cfg := config.Defaults()
	cfg.Auth.Provider = "legacy"

	r := app.NewWithDeps(cfg, app.Deps{JWTIssuer: iss})

	// Missing Authorization header should return 401 (from RequireAuth).
	req := httptest.NewRequest(http.MethodGet, "/api/v1/me", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestNewWithDeps_KratosAuthProviderInstantiatesClient(t *testing.T) {
	t.Parallel()

	// When provider="kratos" and Kratos URLs are set, kratosclient.Client
	// must be instantiated and bound to the Deps. The middleware should be
	// RequireKratosSession, which validates session cookies via the Kratos
	// /sessions/whoami endpoint.
	cfg := config.Defaults()
	cfg.Auth.Provider = "kratos"
	cfg.Auth.Kratos.PublicURL = "http://localhost:4433"
	cfg.Auth.Kratos.AdminURL = "http://localhost:4434"

	r := app.NewWithDeps(cfg, app.Deps{})

	// Missing or invalid session cookie should return 401 (from RequireKratosSession).
	req := httptest.NewRequest(http.MethodGet, "/api/v1/me", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestNewWithDeps_KratosProviderWithoutURLsFallsBack(t *testing.T) {
	t.Parallel()

	// If provider="kratos" but PublicURL is empty, the client should not be
	// instantiated. Routes should not mount (no JWTIssuer, no KratosClient).
	cfg := config.Defaults()
	cfg.Auth.Provider = "kratos"
	cfg.Auth.Kratos.PublicURL = ""
	cfg.Auth.Kratos.AdminURL = ""

	r := app.NewWithDeps(cfg, app.Deps{})

	// /api/v1/* should not exist; request should hit 404 (no route registered).
	req := httptest.NewRequest(http.MethodGet, "/api/v1/me", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestNewWithDeps_LegacyProviderIgnoresKratosConfig(t *testing.T) {
	t.Parallel()

	// If provider="legacy" (or default), Kratos config should be ignored even
	// if PublicURL and AdminURL are set. JWTIssuer should be the only auth mechanism.
	iss, err := auth.NewJWTIssuer(auth.JWTConfig{
		Secret:    []byte("integration-test-secret-xxxxxxxxxx"),
		Issuer:    "jabali-panel-test",
		KeyID:     "v1",
		AccessTTL: time.Minute,
	})
	require.NoError(t, err)

	cfg := config.Defaults()
	cfg.Auth.Provider = "legacy"
	cfg.Auth.Kratos.PublicURL = "http://localhost:4433"
	cfg.Auth.Kratos.AdminURL = "http://localhost:4434"

	r := app.NewWithDeps(cfg, app.Deps{JWTIssuer: iss})

	// Should require JWT auth (RequireAuth), not Kratos.
	req := httptest.NewRequest(http.MethodGet, "/api/v1/me", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusUnauthorized, rec.Code)
}
