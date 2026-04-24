package api_test

import (
	"bytes"
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

// securityAdminRouter returns a Gin engine with admin claims pre-set;
// mounts CrowdSec + UFW route groups against the supplied agent mock.
func securityAdminRouter(cli agent.AgentInterface) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	v1 := r.Group("/api/v1")
	v1.Use(func(c *gin.Context) {
		ginctx.SetClaims(c, &auth.AccessClaims{UserID: "test-admin", IsAdmin: true})
		c.Next()
	})
	api.RegisterSecurityCrowdSecRoutes(v1, cli)
	api.RegisterSecurityUFWRoutes(v1, cli)
	return r
}

func securityUserRouter(cli agent.AgentInterface) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	v1 := r.Group("/api/v1")
	v1.Use(func(c *gin.Context) {
		ginctx.SetClaims(c, &auth.AccessClaims{UserID: "test-user", IsAdmin: false})
		c.Next()
	})
	api.RegisterSecurityCrowdSecRoutes(v1, cli)
	api.RegisterSecurityUFWRoutes(v1, cli)
	return r
}

func TestSecurityCrowdSecStatus_AdminOK(t *testing.T) {
	t.Parallel()
	m := agent.NewMockClient().On("security.crowdsec.status", map[string]any{
		"running": true, "lapi_reachable": true, "version": "1.7.7",
	})
	r := securityAdminRouter(m)
	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/admin/security/crowdsec/status", nil)
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)
}

func TestSecurityCrowdSecDecisions_RejectsBadScope(t *testing.T) {
	t.Parallel()
	m := agent.NewMockClient()
	r := securityAdminRouter(m)
	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/admin/security/crowdsec/decisions?scope=bogus", nil)
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusBadRequest, w.Code)
	var body map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	assert.Equal(t, "invalid_scope", body["error"])
}

func TestSecurityCrowdSecDecisions_AcceptsValidScope(t *testing.T) {
	t.Parallel()
	m := agent.NewMockClient().On("security.crowdsec.decisions.list", map[string]any{"decisions": []any{}})
	r := securityAdminRouter(m)
	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/admin/security/crowdsec/decisions?scope=ip", nil)
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)
}

func TestSecurityCrowdSecDecisions_NonAdminBlocked(t *testing.T) {
	t.Parallel()
	m := agent.NewMockClient()
	r := securityUserRouter(m)
	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/admin/security/crowdsec/status", nil)
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusForbidden, w.Code)
}

func TestSecurityUFWEnable_RequiresConfirm(t *testing.T) {
	t.Parallel()
	m := agent.NewMockClient()
	r := securityAdminRouter(m)
	w := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/v1/admin/security/ufw/enable", bytes.NewBufferString(`{}`))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusBadRequest, w.Code)
	var body map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	assert.Equal(t, "confirmation_required", body["error"])
}

func TestSecurityUFWEnable_ConfirmYESPasses(t *testing.T) {
	t.Parallel()
	m := agent.NewMockClient().On("security.ufw.enable", map[string]any{"active": true})
	r := securityAdminRouter(m)
	w := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/v1/admin/security/ufw/enable", bytes.NewBufferString(`{"confirm":"YES"}`))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)
}

func TestSecurityUFWDisable_RequiresConfirm(t *testing.T) {
	t.Parallel()
	m := agent.NewMockClient()
	r := securityAdminRouter(m)
	w := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/v1/admin/security/ufw/disable", bytes.NewBufferString(`{"confirm":"yes"}`))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestSecurityUFWStatus_AdminOK(t *testing.T) {
	t.Parallel()
	m := agent.NewMockClient().On("security.ufw.status", map[string]any{
		"active": true, "default_in": "deny", "default_out": "allow", "rules": []any{},
	})
	r := securityAdminRouter(m)
	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/admin/security/ufw/status", nil)
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)
}

func TestSecurityUFWRules_AddBadJSON(t *testing.T) {
	t.Parallel()
	m := agent.NewMockClient()
	r := securityAdminRouter(m)
	w := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/v1/admin/security/ufw/rules", bytes.NewBufferString(`not-json`))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestSecurityUFWRules_DeleteBadNum(t *testing.T) {
	t.Parallel()
	m := agent.NewMockClient()
	r := securityAdminRouter(m)
	w := httptest.NewRecorder()
	req := httptest.NewRequest("DELETE", "/api/v1/admin/security/ufw/rules/abc", nil)
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}
