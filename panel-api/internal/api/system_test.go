package api_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/agent"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/api"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/auth"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/ginctx"
)

// adminRouter returns a Gin engine where the given group already carries
// admin claims, simulating what RequireAuth + RequireAdmin would do in
// production.
func adminRouter(cli agent.AgentInterface) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	v1 := r.Group("/api/v1")
	// Inject admin claims so RequireAdmin passes.
	v1.Use(func(c *gin.Context) {
		ginctx.SetClaims(c, &auth.AccessClaims{UserID: "test-admin", IsAdmin: true})
		c.Next()
	})
	api.RegisterSystemRoutes(v1, cli)
	return r
}

func userRouter(cli agent.AgentInterface) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	v1 := r.Group("/api/v1")
	v1.Use(func(c *gin.Context) {
		ginctx.SetClaims(c, &auth.AccessClaims{UserID: "test-user", IsAdmin: false})
		c.Next()
	})
	api.RegisterSystemRoutes(v1, cli)
	return r
}

func TestSystemInfo_OK(t *testing.T) {
	t.Parallel()

	m := agent.NewMockClient().On("system.info", map[string]any{
		"hostname":       "web01",
		"uptime_seconds": 86400,
		"load_avg":       [3]float64{0.5, 0.3, 0.2},
		"cpu_count":      4,
		"mem_total_kb":   16384000,
		"mem_available_kb": 8192000,
		"mem_used_kb":    8192000,
		"partitions":     []map[string]any{},
	})

	r := adminRouter(m)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/system/info", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)

	var body map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	assert.Equal(t, "web01", body["hostname"])
	assert.Equal(t, float64(4), body["cpu_count"])
}

func TestSystemInfo_AgentUnavailable(t *testing.T) {
	t.Parallel()

	m := agent.NewMockClient().OnError("system.info", &agent.AgentError{
		Code:    agent.CodeUnavailable,
		Message: "socket not found",
	})

	r := adminRouter(m)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/system/info", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusServiceUnavailable, rec.Code)
}

func TestSystemInfo_ForbiddenForNonAdmin(t *testing.T) {
	t.Parallel()

	m := agent.NewMockClient().On("system.info", map[string]any{})

	r := userRouter(m)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/system/info", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusForbidden, rec.Code)
}

func TestSystemServices_OK(t *testing.T) {
	t.Parallel()

	m := agent.NewMockClient().On("service.list", map[string]any{
		"services": []map[string]string{
			{"name": "nginx", "active": "active"},
			{"name": "mariadb", "active": "inactive"},
		},
	})

	r := adminRouter(m)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/system/services", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)

	var body struct {
		Services []struct {
			Name   string `json:"name"`
			Active string `json:"active"`
		} `json:"services"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	require.Len(t, body.Services, 2)
	assert.Equal(t, "nginx", body.Services[0].Name)
	assert.Equal(t, "active", body.Services[0].Active)
}

func TestSystemServices_ForbiddenForNonAdmin(t *testing.T) {
	t.Parallel()

	m := agent.NewMockClient().On("service.list", map[string]any{})

	r := userRouter(m)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/system/services", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusForbidden, rec.Code)
}
