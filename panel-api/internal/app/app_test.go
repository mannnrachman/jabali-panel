package app_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/app"
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

	_ = app.NewWith(cfg)
	assert.Equal(t, gin.ReleaseMode, gin.Mode())

	// Put it back so other tests see a consistent default.
	gin.SetMode(gin.TestMode)
}

func TestNewWith_DevelopmentSetsDebugMode(t *testing.T) {
	// Not t.Parallel: same reason as above.
	cfg := config.Defaults()
	cfg.Server.Env = config.EnvDevelopment

	_ = app.NewWith(cfg)
	assert.Equal(t, gin.DebugMode, gin.Mode())

	gin.SetMode(gin.TestMode)
}
