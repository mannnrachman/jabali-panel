package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/auth"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/ginctx"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/models"
)

func cacheRouter(userID string, isAdmin bool, ag *mockAgent) (*gin.Engine, *mockDomainRepo, *mockDNSReconciler) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	v1 := r.Group("/api/v1")
	if userID != "" {
		v1.Use(func(c *gin.Context) {
			ginctx.SetClaims(c, &auth.AccessClaims{UserID: userID, IsAdmin: isAdmin})
			c.Next()
		})
	}
	dr := newMockDomainRepo()
	rc := &mockDNSReconciler{}
	RegisterDomainCacheRoutes(v1, DomainCacheHandlerConfig{
		Agent:      ag,
		Domains:    dr,
		Reconciler: rc,
	})
	return r, dr, rc
}

func seedCacheDomain(dr *mockDomainRepo) {
	dr.Create(context.Background(), &models.Domain{
		ID: "dom1", UserID: "owner", Name: "example.com",
	})
}

func TestCacheToggle_OwnerEnables_SchedulesReconcile(t *testing.T) {
	r, dr, rc := cacheRouter("owner", false, &mockAgent{})
	seedCacheDomain(dr)

	body, _ := json.Marshal(map[string]any{"enabled": true})
	req := httptest.NewRequest(http.MethodPut, "/api/v1/domains/dom1/cache", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	var resp map[string]any
	json.NewDecoder(w.Body).Decode(&resp)
	assert.Equal(t, true, resp["enabled"])
	assert.Equal(t, []string{"dom1"}, rc.scheduled)
}

func TestCacheToggle_NonOwnerForbidden(t *testing.T) {
	r, dr, rc := cacheRouter("intruder", false, &mockAgent{})
	seedCacheDomain(dr)

	body, _ := json.Marshal(map[string]any{"enabled": true})
	req := httptest.NewRequest(http.MethodPut, "/api/v1/domains/dom1/cache", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusForbidden, w.Code)
	assert.Empty(t, rc.scheduled)
}

func TestCacheToggle_AdminOnOthersDomain_OK(t *testing.T) {
	r, dr, _ := cacheRouter("admin", true, &mockAgent{})
	seedCacheDomain(dr) // owned by "owner"

	body, _ := json.Marshal(map[string]any{"enabled": true})
	req := httptest.NewRequest(http.MethodPut, "/api/v1/domains/dom1/cache", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
}

func TestCachePurge_DispatchesAgentCommand(t *testing.T) {
	ag := &mockAgent{}
	r, dr, _ := cacheRouter("owner", false, ag)
	seedCacheDomain(dr)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/domains/dom1/cache/purge", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, 1, ag.callCount)
	assert.Equal(t, "nginx.cache.purge", ag.lastCommand)
}

func TestCacheGet_ReturnsState(t *testing.T) {
	r, dr, _ := cacheRouter("owner", false, &mockAgent{})
	dr.Create(context.Background(), &models.Domain{
		ID: "dom1", UserID: "owner", Name: "example.com", CacheEnabled: true,
	})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/domains/dom1/cache", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	var resp map[string]any
	json.NewDecoder(w.Body).Decode(&resp)
	assert.Equal(t, true, resp["enabled"])
	assert.Equal(t, "example.com", resp["domain_name"])
}
