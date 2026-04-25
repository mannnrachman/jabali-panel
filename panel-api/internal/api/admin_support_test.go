package api_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"

	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/agent"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/api"
)

func newSupportRouter(mock *agent.MockClient, isAdmin bool) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	v1 := r.Group("/api/v1")
	v1.Use(injectAdminClaims(isAdmin))
	api.RegisterAdminSupportRoutes(v1, api.AdminSupportHandlerConfig{Agent: mock})
	return r
}

func TestAdminSupport_RBAC(t *testing.T) {
	mock := agent.NewMockClient()
	r := newSupportRouter(mock, false)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/support/diagnostic", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusForbidden, rec.Code)
}

func TestAdminSupport_Diagnostic_HappyPath(t *testing.T) {
	mock := agent.NewMockClient().On("system.diagnostic_report", map[string]any{
		"url":             "https://enclosed.jabali-panel.com/01abc#pw:k",
		"password":        "supersecret",
		"note_id":         "01abc",
		"byte_count":      9999,
		"generated_at":    "2026-04-25T10:00:00Z",
		"redaction_count": 7,
		"file_count":      32,
	})
	r := newSupportRouter(mock, true)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/support/diagnostic", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), `"redaction_count":7`)
	assert.Contains(t, rec.Body.String(), `"password":"supersecret"`)
}
