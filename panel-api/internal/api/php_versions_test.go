package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/agent"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/auth"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/ginctx"
)

func TestPHPVersions_HappyPath(t *testing.T) {
	t.Parallel()

	gin.SetMode(gin.TestMode)
	r := gin.New()
	v1 := r.Group("/api/v1")

	// Inject authenticated claims (non-admin user)
	v1.Use(func(c *gin.Context) {
		ginctx.SetClaims(c, &auth.AccessClaims{UserID: "test-user", IsAdmin: false})
		c.Next()
	})

	mockAgent := agent.NewMockClient().On("php.version.list", map[string]interface{}{
		"versions": []string{"8.1", "8.3"},
	})

	RegisterPHPVersionRoutes(v1, mockAgent)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/php/versions", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "application/json; charset=utf-8", rec.Header().Get("Content-Type"))

	var body map[string]interface{}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	assert.NotNil(t, body["versions"])
	versions := body["versions"].([]interface{})
	assert.Len(t, versions, 2)
}

func TestPHPVersions_AgentError(t *testing.T) {
	t.Parallel()

	gin.SetMode(gin.TestMode)
	r := gin.New()
	v1 := r.Group("/api/v1")

	// Inject authenticated claims (admin user)
	v1.Use(func(c *gin.Context) {
		ginctx.SetClaims(c, &auth.AccessClaims{UserID: "test-admin", IsAdmin: true})
		c.Next()
	})

	mockAgent := agent.NewMockClient().OnError("php.version.list", &agent.AgentError{
		Code:    "command_failed",
		Message: "PHP agent unreachable",
	})

	RegisterPHPVersionRoutes(v1, mockAgent)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/php/versions", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	require.Equal(t, http.StatusBadGateway, rec.Code)
	var body map[string]interface{}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	assert.NotNil(t, body["error"])
}
